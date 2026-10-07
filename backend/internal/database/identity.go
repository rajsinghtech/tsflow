package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
)

const (
	identityFilterMaxTag  = 128
	identityFilterMaxText = 320
	// identityAny is the tag or user value that matches every device which
	// has that field. The analytics search box sends it for a bare "tag:" or
	// "user@" query, the same way the graph matches any tag or any login.
	identityAny = "*"
)

// IdentityQuery narrows a ranked read to devices recorded for one tailnet.
// An empty query leaves the read unfiltered. Tag and User are both required
// when both are set. Q matches a login, tag, hostname, device name, or address.
type IdentityQuery struct {
	Tag  string
	User string
	Q    string
}

func (q IdentityQuery) active() bool {
	return strings.TrimSpace(q.Tag) != "" || strings.TrimSpace(q.User) != "" || strings.TrimSpace(q.Q) != ""
}

func (q IdentityQuery) normalized() (IdentityQuery, error) {
	tag := strings.TrimSpace(q.Tag)
	user := strings.TrimSpace(q.User)
	text := strings.TrimSpace(q.Q)
	if len(tag) > identityFilterMaxTag || len(user) > identityFilterMaxText || len(text) > identityFilterMaxText {
		return IdentityQuery{}, fmt.Errorf("tag or user filter is too long")
	}
	tag = strings.TrimSpace(stripPrefixFold(tag, "tag:"))
	user = strings.TrimSpace(stripPrefixFold(user, "user@"))
	return IdentityQuery{
		Tag:  strings.ToLower(tag),
		User: strings.ToLower(user),
		Q:    strings.ToLower(text),
	}, nil
}

func stripPrefixFold(value, prefix string) string {
	if len(value) >= len(prefix) && strings.EqualFold(value[:len(prefix)], prefix) {
		return value[len(prefix):]
	}
	return value
}

// mergedDevice is one device after flow-log rows and API rows that name the
// same node have been combined. ids holds every stored id, including the
// numeric id a flow log used and the stable id the device list used.
type mergedDevice struct {
	canonical string
	ids       []string
	owner     string
	hostname  string
	name      string
	tags      []string
	ips       []string
	// apiLegacy is the numeric id already claimed by this device. A different
	// numeric id that only shares an address is a different node.
	apiLegacy string
	stable    string
}

// loadMergedDevices reads one tailnet's node metadata and merges rows that
// are the same device. A tagged device often has a blank owner on the stable
// id; the creator login is on the numeric flow-log row that shares its
// Tailscale address. That login is copied onto the merged device.
func loadMergedDevices(ctx context.Context, q queryRower, tailnetID string) ([]*mergedDevice, error) {
	rows, err := loadNodeMetadata(ctx, q, tailnetID)
	if err != nil {
		return nil, err
	}
	return mergeMetadata(rows), nil
}

func loadNodeMetadata(ctx context.Context, q queryRower, tailnetID string) ([]NodeMetadata, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT node_id, name, hostname, owner, ips, tags
		FROM node_metadata
		WHERE tailnet_id = ?
	`, tailnetID)
	if err != nil {
		return nil, fmt.Errorf("failed to query node metadata: %w", err)
	}
	defer rows.Close()

	var result []NodeMetadata
	for rows.Next() {
		var node NodeMetadata
		var ipsJSON, tagsJSON string
		if err := rows.Scan(&node.NodeID, &node.Name, &node.Hostname, &node.Owner, &ipsJSON, &tagsJSON); err != nil {
			return nil, fmt.Errorf("failed to scan node metadata: %w", err)
		}
		if err := json.Unmarshal([]byte(ipsJSON), &node.IPs); err != nil {
			return nil, fmt.Errorf("failed to decode IP metadata for node %q: %w", node.NodeID, err)
		}
		if err := json.Unmarshal([]byte(tagsJSON), &node.Tags); err != nil {
			return nil, fmt.Errorf("failed to decode tag metadata for node %q: %w", node.NodeID, err)
		}
		result = append(result, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to query node metadata: %w", err)
	}
	return result, nil
}

func mergeMetadata(rows []NodeMetadata) []*mergedDevice {
	if len(rows) == 0 {
		return nil
	}
	ordered := append([]NodeMetadata(nil), rows...)
	sort.Slice(ordered, func(i, j int) bool {
		leftStable := isStableNodeID(ordered[i].NodeID)
		rightStable := isStableNodeID(ordered[j].NodeID)
		if leftStable != rightStable {
			return leftStable
		}
		return ordered[i].NodeID < ordered[j].NodeID
	})

	byID := make(map[string]*mergedDevice, len(ordered))
	byIP := make(map[string]*mergedDevice)
	devices := make([]*mergedDevice, 0, len(ordered))

	for _, row := range ordered {
		id := strings.TrimSpace(row.NodeID)
		if id == "" {
			continue
		}
		if existing := byID[id]; existing != nil {
			mergeDevice(existing, row, byID, byIP)
			continue
		}
		incoming := deviceFromRow(row)
		if existing := matchDevice(incoming, byID, byIP); existing != nil {
			mergeDevice(existing, row, byID, byIP)
			continue
		}
		devices = append(devices, incoming)
		indexDevice(incoming, byID, byIP)
	}
	return devices
}

func deviceFromRow(row NodeMetadata) *mergedDevice {
	id := strings.TrimSpace(row.NodeID)
	device := &mergedDevice{
		ids:      []string{id},
		owner:    strings.TrimSpace(row.Owner),
		hostname: strings.TrimSpace(row.Hostname),
		name:     strings.TrimSpace(row.Name),
		tags:     cleanStrings(row.Tags),
		ips:      cleanStrings(row.IPs),
	}
	if isStableNodeID(id) {
		device.stable = id
	} else {
		device.apiLegacy = id
	}
	device.canonical = canonicalNodeID(device)
	return device
}

func matchDevice(incoming *mergedDevice, byID map[string]*mergedDevice, byIP map[string]*mergedDevice) *mergedDevice {
	for _, id := range incoming.ids {
		if existing := byID[id]; existing != nil {
			return existing
		}
	}
	for _, ip := range incoming.ips {
		if !isTailscaleIP(ip) {
			continue
		}
		existing := byIP[ip]
		if existing != nil && !identityConflict(existing, incoming) {
			return existing
		}
	}
	return nil
}

// identityConflict reports that a shared Tailscale address belongs to two
// different nodes. Two stable ids never merge. A numeric id merges only when
// this device has not already claimed a different numeric id.
func identityConflict(existing, incoming *mergedDevice) bool {
	for _, id := range incoming.ids {
		if id == "" {
			continue
		}
		if isStableNodeID(id) {
			if existing.stable != "" && existing.stable != id {
				return true
			}
			continue
		}
		if existing.apiLegacy != "" && existing.apiLegacy != id {
			return true
		}
	}
	return false
}

func mergeDevice(existing *mergedDevice, row NodeMetadata, byID map[string]*mergedDevice, byIP map[string]*mergedDevice) {
	id := strings.TrimSpace(row.NodeID)
	if existing.owner == "" {
		existing.owner = strings.TrimSpace(row.Owner)
	}
	if existing.hostname == "" {
		existing.hostname = strings.TrimSpace(row.Hostname)
	}
	if existing.name == "" {
		existing.name = strings.TrimSpace(row.Name)
	}
	existing.tags = unionStrings(existing.tags, cleanStrings(row.Tags))
	existing.ips = unionStrings(existing.ips, cleanStrings(row.IPs))
	if id != "" && !containsString(existing.ids, id) {
		existing.ids = append(existing.ids, id)
		if isStableNodeID(id) {
			if existing.stable == "" {
				existing.stable = id
			}
		} else if existing.apiLegacy == "" {
			existing.apiLegacy = id
		}
	}
	existing.canonical = canonicalNodeID(existing)
	indexDevice(existing, byID, byIP)
}

func indexDevice(device *mergedDevice, byID map[string]*mergedDevice, byIP map[string]*mergedDevice) {
	for _, id := range device.ids {
		if id != "" {
			byID[id] = device
		}
	}
	for _, ip := range device.ips {
		if !isTailscaleIP(ip) {
			continue
		}
		current := byIP[ip]
		if current != nil && current != device && current.stable != "" && device.stable == "" {
			continue
		}
		byIP[ip] = device
	}
}

func canonicalNodeID(device *mergedDevice) string {
	var stable, fallback string
	for _, id := range device.ids {
		if id == "" {
			continue
		}
		if isStableNodeID(id) {
			if stable == "" || id < stable {
				stable = id
			}
			continue
		}
		if fallback == "" || id < fallback {
			fallback = id
		}
	}
	if stable != "" {
		return stable
	}
	return fallback
}

func isStableNodeID(id string) bool {
	return len(id) >= 8 && strings.HasSuffix(id, "CNTRL")
}

func isTailscaleIP(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
	}
	return len(ip) == net.IPv6len &&
		ip[0] == 0xfd && ip[1] == 0x7a &&
		ip[2] == 0x11 && ip[3] == 0x5c &&
		ip[4] == 0xa1 && ip[5] == 0xe0
}

func (d *mergedDevice) matches(q IdentityQuery) bool {
	if q.Tag != "" && !tagMatches(d.tags, q.Tag) {
		return false
	}
	if q.User != "" && !textContains(d.owner, q.User) {
		return false
	}
	if q.Q != "" && !deviceContains(d, q.Q) {
		return false
	}
	return q.Tag != "" || q.User != "" || q.Q != ""
}

func tagMatches(tags []string, query string) bool {
	if query == identityAny {
		return len(tags) > 0
	}
	for _, tag := range tags {
		name := strings.ToLower(strings.TrimPrefix(strings.ToLower(tag), "tag:"))
		if strings.Contains(name, query) {
			return true
		}
	}
	return false
}

func textContains(value, query string) bool {
	if query == identityAny {
		return strings.TrimSpace(value) != ""
	}
	return strings.Contains(strings.ToLower(value), query)
}

func deviceContains(device *mergedDevice, query string) bool {
	if textContains(device.owner, query) || textContains(device.hostname, query) || textContains(device.name, query) {
		return true
	}
	if tagMatches(device.tags, query) {
		return true
	}
	for _, ip := range device.ips {
		if strings.Contains(strings.ToLower(ip), query) {
			return true
		}
	}
	return false
}

func matchingDevices(devices []*mergedDevice, query IdentityQuery) []*mergedDevice {
	if !query.active() {
		return nil
	}
	matched := make([]*mergedDevice, 0)
	for _, device := range devices {
		if device.matches(query) {
			matched = append(matched, device)
		}
	}
	return matched
}

type searchExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertSearchIDs(ctx context.Context, exec searchExec, devices []*mergedDevice) error {
	if _, err := exec.ExecContext(ctx, `DROP TABLE IF EXISTS search_ids`); err != nil {
		return fmt.Errorf("failed to reset search ids: %w", err)
	}
	if _, err := exec.ExecContext(ctx, `
		CREATE TEMP TABLE search_ids (
			node_id TEXT PRIMARY KEY,
			canonical TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create search ids: %w", err)
	}
	for _, device := range devices {
		canonical := device.canonical
		if canonical == "" {
			canonical = canonicalNodeID(device)
		}
		seen := make(map[string]struct{}, len(device.ids))
		for _, id := range device.ids {
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			if _, err := exec.ExecContext(ctx, `INSERT OR REPLACE INTO search_ids (node_id, canonical) VALUES (?, ?)`, id, canonical); err != nil {
				return fmt.Errorf("failed to insert search id: %w", err)
			}
		}
	}
	return nil
}

func dropSearchIDs(ctx context.Context, exec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}) {
	if exec == nil {
		return
	}
	_, _ = exec.ExecContext(ctx, `DROP TABLE IF EXISTS search_ids`)
}

func overlayIdentity(byID map[string]*mergedDevice, nodeID, hostname string) (string, string) {
	device := byID[nodeID]
	if device == nil {
		return hostname, ""
	}
	if hostname == "" {
		hostname = device.hostname
		if hostname == "" {
			hostname = device.name
		}
	}
	return hostname, device.owner
}

func indexMerged(devices []*mergedDevice) map[string]*mergedDevice {
	byID := make(map[string]*mergedDevice, len(devices))
	for _, device := range devices {
		if device.canonical != "" {
			byID[device.canonical] = device
		}
		for _, id := range device.ids {
			if id != "" {
				byID[id] = device
			}
		}
	}
	return byID
}

func unionStrings(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, item := range append(base, extra...) {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func cleanStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return unionStrings(nil, values)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
