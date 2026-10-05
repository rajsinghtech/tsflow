package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	// AccessModeTsnet reads identity from the embedded tsnet LocalClient.
	AccessModeTsnet = "tsnet"
	// AccessModeHeader trusts a reverse proxy, or WhoIs on X-Forwarded-For
	// when a local tailscaled socket is reachable.
	AccessModeHeader = "header"

	// DefaultCapabilityHeader is the header Tailscale Serve sets when
	// --accept-app-caps is configured.
	DefaultCapabilityHeader = "Tailscale-App-Capabilities"

	AccessAutoscopeOff    = "off"
	AccessAutoscopeUser   = "user"
	AccessAutoscopeGroups = "groups"

	localWhoIsAuto    = "auto"
	localWhoIsOff     = "off"
	localWhoIsRequire = "require"
)

// Grant is one capability or group rule. Tailnets is optional.
// An omitted list or "*" allows every configured tailnet id.
// An explicit empty list allows none from this rule.
type Grant struct {
	All      bool
	Tailnets []string
}

// Access is the parsed access-control configuration.
// Enabled is false unless the operator opts in. The zero value
// leaves every request on the existing open path.
type Access struct {
	Enabled          bool
	Mode             string
	Capability       string
	CapabilityHeader string
	TrustedProxies   string
	TrustedPrefixes  []netip.Prefix
	GroupsHeader     string
	GroupGrantsRaw   string
	GroupGrantsFile  string
	GroupGrants      map[string]Grant
	TailscaledSocket string
	LocalWhoIs       string
	Autoscope        string
	Debug            bool
}

func (c *Config) loadAccess() Access {
	return Access{
		Capability:       strings.TrimSpace(os.Getenv("TSFLOW_ACCESS_CAPABILITY")),
		Mode:             strings.TrimSpace(os.Getenv("TSFLOW_ACCESS_MODE")),
		CapabilityHeader: strings.TrimSpace(os.Getenv("TSFLOW_ACCESS_CAPABILITY_HEADER")),
		TrustedProxies:   strings.TrimSpace(os.Getenv("TSFLOW_ACCESS_TRUSTED_PROXIES")),
		GroupsHeader:     strings.TrimSpace(os.Getenv("TSFLOW_ACCESS_GROUPS_HEADER")),
		GroupGrantsRaw:   strings.TrimSpace(os.Getenv("TSFLOW_ACCESS_GROUP_GRANTS")),
		GroupGrantsFile:  strings.TrimSpace(os.Getenv("TSFLOW_ACCESS_GROUP_GRANTS_FILE")),
		TailscaledSocket: strings.TrimSpace(os.Getenv("TSFLOW_ACCESS_TAILSCALED_SOCKET")),
		LocalWhoIs:       strings.TrimSpace(os.Getenv("TSFLOW_ACCESS_LOCAL_WHOIS")),
		Autoscope:        strings.TrimSpace(os.Getenv("TSFLOW_ACCESS_AUTOSCOPE")),
		Debug:            strings.EqualFold(strings.TrimSpace(os.Getenv("TSFLOW_LOG_LEVEL")), "debug"),
	}
}

func (c *Config) prepareAccess() error {
	access := c.Access
	autoscope, err := parseAutoscope(access.Autoscope)
	if err != nil {
		return err
	}
	access.Autoscope = autoscope
	localWhoIs, err := parseLocalWhoIs(access.LocalWhoIs)
	if err != nil {
		return err
	}
	access.LocalWhoIs = localWhoIs

	if !accessRequested(access) {
		c.Access = Access{Autoscope: AccessAutoscopeOff, LocalWhoIs: localWhoIsAuto, Debug: access.Debug}
		return nil
	}

	mode, err := resolveAccessMode(access.Mode, c.TsnetServe)
	if err != nil {
		return err
	}
	access.Mode = mode
	access.Enabled = true

	if access.Capability != "" && !validCapabilityName(access.Capability) {
		return errors.New("TSFLOW_ACCESS_CAPABILITY must look like example.com/cap/tsflow")
	}
	if access.CapabilityHeader == "" {
		access.CapabilityHeader = DefaultCapabilityHeader
	}

	grants, err := loadGroupGrants(access.GroupGrantsRaw, access.GroupGrantsFile)
	if err != nil {
		return err
	}
	access.GroupGrants = grants

	if err := validateAccessMode(access, c.TsnetServe); err != nil {
		return err
	}
	if mode == AccessModeHeader {
		prefixes, err := parseTrustedPrefixes(access.TrustedProxies)
		if err != nil {
			return err
		}
		access.TrustedPrefixes = prefixes
	}

	c.Access = access
	return nil
}

func accessRequested(a Access) bool {
	if a.Capability != "" || a.Mode != "" || a.GroupsHeader != "" {
		return true
	}
	if a.GroupGrantsRaw != "" || a.GroupGrantsFile != "" || a.TrustedProxies != "" {
		return true
	}
	if a.Autoscope != "" && !strings.EqualFold(a.Autoscope, AccessAutoscopeOff) && a.Autoscope != "false" && a.Autoscope != "0" {
		return true
	}
	if a.TailscaledSocket != "" || a.CapabilityHeader != "" {
		return true
	}
	switch strings.ToLower(a.LocalWhoIs) {
	case "", localWhoIsAuto, localWhoIsOff, "false", "0":
		return false
	default:
		return true
	}
}

func resolveAccessMode(mode string, tsnetServe bool) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		if tsnetServe {
			return AccessModeTsnet, nil
		}
		return "", errors.New("access control requires TSFLOW_SERVE or TSFLOW_ACCESS_MODE=header")
	case AccessModeTsnet:
		if !tsnetServe {
			return "", errors.New("TSFLOW_ACCESS_MODE=tsnet requires TSFLOW_SERVE=true")
		}
		return AccessModeTsnet, nil
	case AccessModeHeader, "proxy":
		return AccessModeHeader, nil
	default:
		return "", errors.New("TSFLOW_ACCESS_MODE must be tsnet or header")
	}
}

func validateAccessMode(a Access, tsnetServe bool) error {
	if a.Mode == AccessModeTsnet {
		switch {
		case a.GroupsHeader != "":
			return errors.New("TSFLOW_ACCESS_GROUPS_HEADER is only valid with TSFLOW_ACCESS_MODE=header")
		case a.TrustedProxies != "":
			return errors.New("TSFLOW_ACCESS_TRUSTED_PROXIES is only valid with TSFLOW_ACCESS_MODE=header")
		case a.TailscaledSocket != "":
			return errors.New("TSFLOW_ACCESS_TAILSCALED_SOCKET is only valid with TSFLOW_ACCESS_MODE=header")
		case a.LocalWhoIs == localWhoIsRequire:
			return errors.New("TSFLOW_ACCESS_LOCAL_WHOIS=require is only valid with TSFLOW_ACCESS_MODE=header")
		case a.CapabilityHeader != DefaultCapabilityHeader:
			return errors.New("TSFLOW_ACCESS_CAPABILITY_HEADER is only valid with TSFLOW_ACCESS_MODE=header")
		case a.Capability == "" && len(a.GroupGrants) == 0:
			return errors.New("tsnet access control requires TSFLOW_ACCESS_CAPABILITY or a group grant map")
		}
	}

	if a.Mode == AccessModeHeader {
		if strings.TrimSpace(a.TrustedProxies) == "" {
			return errors.New("TSFLOW_ACCESS_MODE=header requires TSFLOW_ACCESS_TRUSTED_PROXIES")
		}
		whoIsGroups := a.LocalWhoIs == localWhoIsRequire && len(a.GroupGrants) > 0
		if a.Capability == "" && a.GroupsHeader == "" && !whoIsGroups {
			return errors.New("header mode requires TSFLOW_ACCESS_CAPABILITY or TSFLOW_ACCESS_GROUPS_HEADER")
		}
	}

	if a.GroupsHeader != "" && len(a.GroupGrants) == 0 {
		return errors.New("TSFLOW_ACCESS_GROUPS_HEADER requires TSFLOW_ACCESS_GROUP_GRANTS or TSFLOW_ACCESS_GROUP_GRANTS_FILE")
	}
	if len(a.GroupGrants) > 0 && !groupSourceAvailable(a, tsnetServe) {
		return errors.New("group grants require TSFLOW_ACCESS_GROUPS_HEADER, TSFLOW_SERVE, or TSFLOW_ACCESS_LOCAL_WHOIS=require")
	}
	if a.Autoscope == AccessAutoscopeGroups && len(a.GroupGrants) == 0 {
		return errors.New("TSFLOW_ACCESS_AUTOSCOPE=groups requires a group grant map")
	}
	if a.Autoscope == AccessAutoscopeGroups && !groupSourceAvailable(a, tsnetServe) {
		return errors.New("TSFLOW_ACCESS_AUTOSCOPE=groups requires a group source (tsnet WhoIs, a groups header, or TSFLOW_ACCESS_LOCAL_WHOIS=require)")
	}
	return nil
}

func groupSourceAvailable(a Access, tsnetServe bool) bool {
	if a.Mode == AccessModeTsnet && tsnetServe {
		return true
	}
	if a.GroupsHeader != "" {
		return true
	}
	return a.Mode == AccessModeHeader && a.LocalWhoIs == localWhoIsRequire
}

func parseAutoscope(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", AccessAutoscopeOff, "false", "0":
		return AccessAutoscopeOff, nil
	case AccessAutoscopeUser:
		return AccessAutoscopeUser, nil
	case AccessAutoscopeGroups:
		return AccessAutoscopeGroups, nil
	default:
		return "", errors.New("TSFLOW_ACCESS_AUTOSCOPE must be off, user, or groups")
	}
}

func parseLocalWhoIs(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", localWhoIsAuto:
		return localWhoIsAuto, nil
	case localWhoIsOff, "false", "0":
		return localWhoIsOff, nil
	case localWhoIsRequire, "true", "1", "on":
		return localWhoIsRequire, nil
	default:
		return "", errors.New("TSFLOW_ACCESS_LOCAL_WHOIS must be auto, off, or require")
	}
}

func validCapabilityName(name string) bool {
	if name == "" || strings.ContainsAny(name, " \t\r\n") {
		return false
	}
	slash := strings.IndexByte(name, '/')
	return slash > 0 && slash < len(name)-1
}

func loadGroupGrants(raw, path string) (map[string]Grant, error) {
	if raw != "" && path != "" {
		return nil, errors.New("set only one of TSFLOW_ACCESS_GROUP_GRANTS and TSFLOW_ACCESS_GROUP_GRANTS_FILE")
	}
	if raw == "" && path == "" {
		return nil, nil
	}
	payload := []byte(raw)
	if path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("TSFLOW_ACCESS_GROUP_GRANTS_FILE: %w", err)
		}
		payload = body
	}
	grants, err := ParseGrantMap(payload)
	if err != nil {
		return nil, fmt.Errorf("group grant map: %w", err)
	}
	if len(grants) == 0 {
		return nil, errors.New("group grant map is empty")
	}
	return grants, nil
}

// ParseGrantMap reads a JSON object of group name to grant.
func ParseGrantMap(raw []byte) (map[string]Grant, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	var body map[string]json.RawMessage
	if err := dec.Decode(&body); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if dec.More() {
		return nil, errors.New("extra JSON after the grant map")
	}
	if len(body) == 0 {
		return map[string]Grant{}, nil
	}
	out := make(map[string]Grant, len(body))
	names := make([]string, 0, len(body))
	for name := range body {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		key := strings.TrimSpace(name)
		if key == "" {
			return nil, errors.New("group name is empty")
		}
		grant, err := ParseGrant(body[name])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		out[key] = grant
	}
	return out, nil
}

// ParseGrant reads one grant object. Unknown fields are rejected.
// A null or empty object allows every tailnet. "*" in the list does too.
func ParseGrant(raw json.RawMessage) (Grant, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return Grant{All: true}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var body struct {
		Tailnets *[]string `json:"tailnets"`
	}
	if err := dec.Decode(&body); err != nil {
		return Grant{}, fmt.Errorf("invalid access grant: %w", err)
	}
	if dec.More() {
		return Grant{}, errors.New("invalid access grant")
	}
	if body.Tailnets == nil {
		return Grant{All: true}, nil
	}
	ids := make([]string, 0, len(*body.Tailnets))
	seen := make(map[string]struct{}, len(*body.Tailnets))
	for _, id := range *body.Tailnets {
		id = strings.TrimSpace(id)
		if id == "" {
			return Grant{}, errors.New("invalid access grant: tailnets entries must be non-empty")
		}
		if id == "*" {
			return Grant{All: true}, nil
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return Grant{Tailnets: ids}, nil
}

func parseTrustedPrefixes(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, err := parseIPPrefix(part)
		if err != nil {
			return nil, fmt.Errorf("TSFLOW_ACCESS_TRUSTED_PROXIES: %q: %w", part, err)
		}
		out = append(out, prefix)
	}
	if len(out) == 0 {
		return nil, errors.New("TSFLOW_ACCESS_MODE=header requires TSFLOW_ACCESS_TRUSTED_PROXIES")
	}
	return out, nil
}

func parseIPPrefix(value string) (netip.Prefix, error) {
	if !strings.Contains(value, "/") {
		addr, err := netip.ParseAddr(value)
		if err != nil {
			return netip.Prefix{}, err
		}
		return addr.Prefix(addr.BitLen())
	}
	addrPart, bitsPart, _ := strings.Cut(value, "/")
	addr, err := netip.ParseAddr(strings.TrimSpace(addrPart))
	if err != nil {
		return netip.Prefix{}, err
	}
	bits, err := strconv.Atoi(strings.TrimSpace(bitsPart))
	if err != nil {
		return netip.Prefix{}, err
	}
	return addr.Prefix(bits)
}
