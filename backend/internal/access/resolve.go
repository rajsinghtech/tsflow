package access

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

const (
	headerUserLogin = "Tailscale-User-Login"
	headerUserName  = "Tailscale-User-Name"
	headerForwarded = "X-Forwarded-For"
)

var (
	errMissingGrant        = errors.New("missing access grant")
	errInvalidGrant        = errors.New("invalid access grant")
	errIdentityUnavailable = errors.New("tailscale identity unavailable")
	errMissingIdentity     = errors.New("missing tailscale identity")
)

// WhoIsClient is the subset of tailscale.com/client/local.Client used
// to identify the peer that opened a connection.
type WhoIsClient interface {
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
}

// Identity is the viewer attached to one request.
type Identity struct {
	Login       string
	Name        string
	Node        string
	Groups      []string
	Allow       Allow
	Autoscope   string
	DeviceScope *DeviceScope
}

// Resolve identifies the caller and the tailnets they may see.
// A missing grant and an invalid grant both fail closed.
func Resolve(r *http.Request, cfg config.Access, who WhoIsClient) (Identity, error) {
	if r == nil {
		return Identity{}, errIdentityUnavailable
	}
	var (
		ident Identity
		err   error
	)
	switch cfg.Mode {
	case config.AccessModeTsnet:
		if who == nil {
			return Identity{}, errIdentityUnavailable
		}
		ident, err = resolveWhoIs(r.Context(), who, r.RemoteAddr, cfg, nil)
	case config.AccessModeHeader:
		if !fromTrustedProxy(r, cfg.TrustedPrefixes) {
			return Identity{}, errMissingGrant
		}
		if who != nil {
			peer, peerErr := forwardedPeer(r)
			if peerErr != nil {
				return Identity{}, errIdentityUnavailable
			}
			// WhoIs replaces identity and capability headers. The groups
			// header is still read when the proxy is trusted.
			ident, err = resolveWhoIs(r.Context(), who, peer, cfg, groupHeader(r, cfg))
		} else {
			ident, err = resolveHeaders(r, cfg)
		}
	default:
		return Identity{}, errIdentityUnavailable
	}
	if err != nil {
		return Identity{}, err
	}
	ident.Autoscope = cfg.Autoscope
	if cfg.Autoscope == config.AccessAutoscopeUser || cfg.Autoscope == config.AccessAutoscopeGroups {
		ident.DeviceScope = DeviceScopeFor(cfg.Autoscope, ident.Login, ident.Groups, cfg.GroupGrants)
	}
	return ident, nil
}

func resolveWhoIs(ctx context.Context, who WhoIsClient, addr string, cfg config.Access, headerGroups []string) (Identity, error) {
	if strings.TrimSpace(addr) == "" {
		return Identity{}, errIdentityUnavailable
	}
	resp, err := who.WhoIs(ctx, addr)
	if err != nil || resp == nil {
		return Identity{}, errIdentityUnavailable
	}
	ident := identityFromWhoIs(resp)
	if cfg.Grants == config.AccessGrantsIdentity {
		if !whoIsIdentified(ident) {
			return Identity{}, errMissingIdentity
		}
		ident.Groups = unionNames(ident.Groups, headerGroups)
		ident.Allow = Allow{All: true}
		return ident, nil
	}
	matched, allow, err := allowFromWhoIs(resp, cfg, ident.Groups, headerGroups)
	if err != nil {
		return Identity{}, err
	}
	if !matched {
		return Identity{}, errMissingGrant
	}
	ident.Groups = unionNames(ident.Groups, headerGroups)
	ident.Allow = allow
	return ident, nil
}

func resolveHeaders(r *http.Request, cfg config.Access) (Identity, error) {
	login := decodeHeader(r.Header.Get(headerUserLogin))
	name := decodeHeader(r.Header.Get(headerUserName))
	groups := groupHeader(r, cfg)
	if cfg.Grants == config.AccessGrantsIdentity {
		if login == "" {
			return Identity{}, errMissingIdentity
		}
		return Identity{
			Login:  login,
			Name:   name,
			Groups: groups,
			Allow:  Allow{All: true},
		}, nil
	}
	matched := false
	var allow Allow

	if cfg.Capability != "" {
		ok, capAllow, err := allowFromCapabilityHeader(r.Header.Get(cfg.CapabilityHeader), cfg.Capability)
		if err != nil {
			return Identity{}, err
		}
		if ok {
			matched = true
			allow = capAllow
		}
	}
	if groupMatched, groupAllow := allowFromGroups(groups, cfg.GroupGrants); groupMatched {
		matched = true
		mergeAllow(&allow, groupAllow)
	}
	if !matched {
		return Identity{}, errMissingGrant
	}
	return Identity{
		Login:  login,
		Name:   name,
		Groups: groups,
		Allow:  allow,
	}, nil
}

func identityFromWhoIs(resp *apitype.WhoIsResponse) Identity {
	ident := Identity{}
	if resp.UserProfile != nil {
		ident.Login = strings.TrimSpace(resp.UserProfile.LoginName)
		ident.Name = strings.TrimSpace(resp.UserProfile.DisplayName)
		ident.Groups = cleanNames(resp.UserProfile.Groups)
	}
	if resp.Node != nil {
		ident.Node = strings.TrimSpace(resp.Node.Name)
	}
	return ident
}

func whoIsIdentified(ident Identity) bool {
	return ident.Login != "" || ident.Name != "" || ident.Node != ""
}

func allowFromWhoIs(resp *apitype.WhoIsResponse, cfg config.Access, profileGroups, headerGroups []string) (bool, Allow, error) {
	matched := false
	var allow Allow
	if cfg.Capability != "" && resp.CapMap != nil {
		raw, ok := resp.CapMap[tailcfg.PeerCapability(cfg.Capability)]
		if ok {
			values := make([]json.RawMessage, 0, len(raw))
			for _, item := range raw {
				values = append(values, json.RawMessage(item))
			}
			capAllow, err := grantsFromValues(values)
			if err != nil {
				return false, Allow{}, errInvalidGrant
			}
			matched = true
			allow = capAllow
		}
	}
	groups := unionNames(profileGroups, headerGroups)
	if groupMatched, groupAllow := allowFromGroups(groups, cfg.GroupGrants); groupMatched {
		matched = true
		mergeAllow(&allow, groupAllow)
	}
	return matched, allow, nil
}

func allowFromCapabilityHeader(raw, capability string) (bool, Allow, error) {
	raw = decodeHeader(raw)
	if strings.TrimSpace(raw) == "" || capability == "" {
		return false, Allow{}, nil
	}
	values, ok, err := capabilityValues(raw, capability)
	if err != nil || !ok {
		if err != nil {
			return false, Allow{}, errInvalidGrant
		}
		return false, Allow{}, nil
	}
	allow, err := grantsFromValues(values)
	if err != nil {
		return false, Allow{}, errInvalidGrant
	}
	return true, allow, nil
}

func capabilityValues(raw, capability string) ([]json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace([]byte(raw))
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if trimmed[0] == '{' {
		var asMap map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &asMap); err != nil {
			return nil, false, err
		}
		value, ok := asMap[capability]
		if !ok {
			return nil, false, nil
		}
		return splitGrantValues(value)
	}
	return splitGrantValues(trimmed)
}

func splitGrantValues(raw json.RawMessage) ([]json.RawMessage, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, true, nil
	}
	if raw[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, false, err
		}
		return items, true, nil
	}
	return []json.RawMessage{raw}, true, nil
}

func allowFromGroups(groups []string, grants map[string]config.Grant) (bool, Allow) {
	var allow Allow
	matched := false
	for _, group := range groups {
		grant, ok := grants[group]
		if !ok {
			continue
		}
		matched = true
		allow.Add(grant)
	}
	return matched, allow
}

func mergeAllow(dst *Allow, src Allow) {
	if src.All {
		dst.All = true
	}
	for id := range src.ids {
		if dst.ids == nil {
			dst.ids = make(map[string]struct{})
		}
		dst.ids[id] = struct{}{}
	}
}

func groupHeader(r *http.Request, cfg config.Access) []string {
	if cfg.GroupsHeader == "" {
		return nil
	}
	return splitList(decodeHeader(r.Header.Get(cfg.GroupsHeader)))
}

func splitList(value string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func unionNames(lists ...[]string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, list := range lists {
		for _, name := range list {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func cleanNames(names []string) []string {
	return unionNames(names)
}

func decodeHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.Contains(value, "=?") {
		return value
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

func fromTrustedProxy(r *http.Request, prefixes []netip.Prefix) bool {
	addr, ok := remoteAddr(r)
	if !ok {
		return false
	}
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func remoteAddr(r *http.Request) (netip.Addr, bool) {
	host := strings.TrimSpace(r.RemoteAddr)
	if host == "" {
		return netip.Addr{}, false
	}
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr, true
}

// forwardedPeer returns the client address appended by the nearest proxy.
func forwardedPeer(r *http.Request) (string, error) {
	for _, part := range reverseSplit(r.Header.Get(headerForwarded)) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if host, _, err := net.SplitHostPort(part); err == nil {
			part = host
		}
		addr, err := netip.ParseAddr(part)
		if err != nil {
			continue
		}
		return addr.String(), nil
	}
	return "", fmt.Errorf("missing %s peer", headerForwarded)
}

func reverseSplit(value string) []string {
	parts := strings.Split(value, ",")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return parts
}

func publicError(err error) string {
	switch {
	case errors.Is(err, errInvalidGrant):
		return errInvalidGrant.Error()
	case errors.Is(err, errIdentityUnavailable):
		return errIdentityUnavailable.Error()
	case errors.Is(err, errMissingIdentity):
		return errMissingIdentity.Error()
	default:
		return errMissingGrant.Error()
	}
}
