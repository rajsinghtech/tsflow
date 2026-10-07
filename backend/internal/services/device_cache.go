package services

import (
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// DeviceCache maps IPs and node ids to device info for fast lookups.
// One physical node can be seen under both the stable Tailscale node id
// and the legacy numeric id. Those refer to a single cache entry.
type DeviceCache struct {
	mu          sync.RWMutex
	ipToDevice  map[string]*DeviceCacheEntry
	idToDevice  map[string]*DeviceCacheEntry
	aliases     map[string]string // alternate id -> canonical id
	lastRefresh time.Time
}

type DeviceCacheEntry struct {
	ID                 string
	NodeID             string
	Name               string
	Hostname           string
	Owner              string
	OS                 string
	LastSeen           string
	Created            string
	ClientVersion      string
	IPs                []string
	Tags               []string
	Online             bool
	ConnectedToControl bool
	Authorized         bool
	IsTailscale        bool
	// fromAPI is set for devices returned by the Tailscale device list.
	// Flow-log-only rows are the ones retention is allowed to drop.
	fromAPI bool
	// flowSeen is the last time a flow log identified this node.
	flowSeen time.Time
	// legacyID is the other id carried by a not-yet-adopted flow node.
	legacyID string
	// apiLegacyID is the numeric id the device list reported for this node.
	apiLegacyID string
	// aliases lists the ids that point at this entry in DeviceCache.aliases.
	// An id that was later pointed elsewhere is skipped on read. The list
	// keeps rekeying, pruning, and EquivalentIDs proportional to one device
	// instead of the whole tailnet.
	aliases []string
}

// deviceIsOnline reports whether a device should be shown as online.
// A node connected to control often has no lastSeen, so that flag counts.
func deviceIsOnline(connectedToControl bool, lastSeen, now time.Time) bool {
	if connectedToControl {
		return true
	}
	if lastSeen.IsZero() {
		return false
	}
	age := now.Sub(lastSeen)
	return age >= 0 && age < 2*time.Minute
}

func NewDeviceCache() *DeviceCache {
	return &DeviceCache{
		ipToDevice: make(map[string]*DeviceCacheEntry),
		idToDevice: make(map[string]*DeviceCacheEntry),
		aliases:    make(map[string]string),
	}
}

// preferStableDeviceID chooses the id exposed for a Tailscale API device.
// nodeId is the stable id. id is the legacy numeric id. When the API sends
// only one of them, that value stays the canonical id and there is no alias.
func preferStableDeviceID(legacyID, stableID string) (canonical, alias string) {
	legacyID = strings.TrimSpace(legacyID)
	stableID = strings.TrimSpace(stableID)
	if stableID != "" && stableID != legacyID {
		return stableID, legacyID
	}
	if legacyID != "" {
		return legacyID, ""
	}
	return stableID, ""
}

func (c *DeviceCache) Update(devices []Device) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ipToDevice = make(map[string]*DeviceCacheEntry)
	c.idToDevice = make(map[string]*DeviceCacheEntry)
	c.aliases = make(map[string]string)

	for _, d := range devices {
		if d.ID == "" {
			continue
		}
		nodeID := d.NodeID
		if nodeID == "" && isStableNodeID(d.ID) {
			nodeID = d.ID
		}
		entry := &DeviceCacheEntry{
			ID:                 d.ID,
			NodeID:             nodeID,
			Name:               d.Name,
			Hostname:           d.Hostname,
			Owner:              d.User,
			OS:                 d.OS,
			LastSeen:           d.LastSeen,
			Created:            d.Created,
			ClientVersion:      d.ClientVersion,
			IPs:                append([]string(nil), d.Addresses...),
			Tags:               append([]string(nil), d.Tags...),
			Online:             d.Online,
			ConnectedToControl: d.ConnectedToControl,
			Authorized:         d.Authorized,
			IsTailscale:        true,
			fromAPI:            true,
			apiLegacyID:        strings.TrimSpace(d.LegacyID),
		}
		c.idToDevice[d.ID] = entry
		c.aliasLocked(d.LegacyID, d.ID)
		for _, ip := range d.Addresses {
			c.ipToDevice[ip] = entry
		}
	}
	c.lastRefresh = time.Now()
}

// UpsertFromFlowLogMetadata adds device identities embedded in exported
// Tailscale network-flow log objects. Object-store ingestion can therefore
// resolve names and tags without waiting for a separate devices API refresh.
// A flow-log node that is the same device as an API node is merged into that
// API device instead of stored a second time.
func (c *DeviceCache) UpsertFromFlowLogMetadata(logMap map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if src, ok := logMap["srcNode"].(map[string]any); ok {
		c.adoptLocked(entryFromFlowNode(src))
	}
	if dstNodes, ok := logMap["dstNodes"].([]any); ok {
		for _, item := range dstNodes {
			if node, ok := item.(map[string]any); ok {
				c.adoptLocked(entryFromFlowNode(node))
			}
		}
	}
	c.lastRefresh = time.Now()
}

func (c *DeviceCache) UpsertNodeMetadata(nodes []database.NodeMetadata) {
	if len(nodes) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, node := range nodes {
		if node.NodeID == "" {
			continue
		}
		hostname := node.Hostname
		if hostname == "" {
			hostname = node.Name
			if dot := strings.Index(hostname, "."); dot > 0 {
				hostname = hostname[:dot]
			}
		}
		c.adoptLocked(&DeviceCacheEntry{
			ID:          node.NodeID,
			Name:        node.Name,
			Hostname:    hostname,
			Owner:       node.Owner,
			IPs:         append([]string(nil), node.IPs...),
			Tags:        append([]string(nil), node.Tags...),
			IsTailscale: true,
			Authorized:  true,
			flowSeen:    node.Updated,
		})
	}
	c.lastRefresh = time.Now()
}

func entryFromFlowNode(node map[string]any) *DeviceCacheEntry {
	id, legacy := flowNodeIDs(node)
	if id == "" {
		return nil
	}
	name, _ := node["name"].(string)
	hostname := name
	if dot := strings.Index(hostname, "."); dot > 0 {
		hostname = hostname[:dot]
	}
	owner, _ := node["user"].(string)
	if owner == "" {
		owner, _ = node["owner"].(string)
	}
	return &DeviceCacheEntry{
		ID:          id,
		Name:        name,
		Hostname:    hostname,
		Owner:       owner,
		IPs:         stringList(node["addresses"]),
		Tags:        stringList(node["tags"]),
		IsTailscale: true,
		Authorized:  true,
		flowSeen:    time.Now(),
		legacyID:    legacy,
	}
}

func flowNodeIDs(node map[string]any) (id, legacy string) {
	id = scalarID(node["nodeId"])
	legacy = scalarID(node["id"])
	if id == "" {
		return legacy, ""
	}
	if legacy == id {
		legacy = ""
	}
	return id, legacy
}

func scalarID(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case jsonNumber:
		return strings.TrimSpace(typed.String())
	case float64:
		if typed == 0 || math.Trunc(typed) != typed || math.IsNaN(typed) || math.IsInf(typed, 0) {
			return ""
		}
		return strconv.FormatFloat(typed, 'f', 0, 64)
	default:
		return ""
	}
}

// jsonNumber is the encoding/json.Number interface, accepted without importing
// encoding/json into every call site that already decoded into map[string]any.
type jsonNumber interface {
	String() string
}

func stringList(raw any) []string {
	values, ok := raw.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if str, ok := value.(string); ok && str != "" {
			result = append(result, str)
		}
	}
	return result
}

func (c *DeviceCache) adoptLocked(incoming *DeviceCacheEntry) {
	if incoming == nil || incoming.ID == "" {
		return
	}
	if c.aliases == nil {
		c.aliases = make(map[string]string)
	}
	legacy := incoming.legacyID
	if existing := c.matchLocked(incoming); existing != nil {
		incoming.legacyID = ""
		c.mergeLocked(existing, incoming, legacy)
		return
	}
	incoming.legacyID = ""
	if incoming.NodeID == "" && isStableNodeID(incoming.ID) {
		incoming.NodeID = incoming.ID
	}
	c.idToDevice[incoming.ID] = incoming
	c.aliasLocked(legacy, incoming.ID)
	for _, ip := range incoming.IPs {
		c.mapIPLocked(ip, incoming)
	}
}

// mapIPLocked points ip at entry unless a device from the Tailscale device
// list already holds it. A flow-only row for a deleted node must not take
// over an address that was reassigned to a live device.
func (c *DeviceCache) mapIPLocked(ip string, entry *DeviceCacheEntry) {
	if current, ok := c.ipToDevice[ip]; ok && current != nil && current != entry {
		if current.fromAPI && !entry.fromAPI {
			return
		}
		if !current.fromAPI && !entry.fromAPI && current.flowSeen.After(entry.flowSeen) {
			return
		}
	}
	c.ipToDevice[ip] = entry
}

func (c *DeviceCache) matchLocked(incoming *DeviceCacheEntry) *DeviceCacheEntry {
	if existing := c.deviceLocked(incoming.ID); existing != nil {
		return existing
	}
	if incoming.legacyID != "" {
		if existing := c.deviceLocked(incoming.legacyID); existing != nil {
			return existing
		}
	}
	for _, ip := range incoming.IPs {
		if !isTailscaleIP(ip) {
			continue
		}
		if existing, ok := c.ipToDevice[ip]; ok && existing != nil && !identitiesConflict(existing, incoming) {
			return existing
		}
	}
	return nil
}

// identitiesConflict reports whether two entries that share a Tailscale
// address carry ids proving they are different nodes. Addresses are reused
// after a node is deleted, so an address match alone cannot merge two
// different stable node ids, or a numeric id other than the one the device
// list reported for that device.
func identitiesConflict(existing, incoming *DeviceCacheEntry) bool {
	existingStable := existing.NodeID
	if existingStable == "" && isStableNodeID(existing.ID) {
		existingStable = existing.ID
	}
	for _, id := range []string{incoming.ID, incoming.legacyID} {
		if id == "" {
			continue
		}
		if isStableNodeID(id) {
			if existingStable != "" && existingStable != id {
				return true
			}
			continue
		}
		if existing.apiLegacyID != "" && existing.apiLegacyID != id {
			return true
		}
	}
	return false
}

func (c *DeviceCache) mergeLocked(existing, incoming *DeviceCacheEntry, legacy string) {
	// Tagged API devices often have an empty user. The creator login is then
	// only on the flow-log row, and has to survive onto the merged device.
	if existing.Owner == "" {
		existing.Owner = incoming.Owner
	}
	if existing.Name == "" {
		existing.Name = incoming.Name
	}
	if existing.Hostname == "" {
		existing.Hostname = incoming.Hostname
	}
	if existing.OS == "" {
		existing.OS = incoming.OS
	}
	if existing.LastSeen == "" {
		existing.LastSeen = incoming.LastSeen
	}
	if existing.Created == "" {
		existing.Created = incoming.Created
	}
	if existing.ClientVersion == "" {
		existing.ClientVersion = incoming.ClientVersion
	}
	if existing.NodeID == "" {
		existing.NodeID = incoming.NodeID
	}
	if incoming.Online {
		existing.Online = true
	}
	if incoming.ConnectedToControl {
		existing.ConnectedToControl = true
	}
	if !existing.fromAPI && incoming.Authorized {
		existing.Authorized = true
	}
	if incoming.flowSeen.After(existing.flowSeen) {
		existing.flowSeen = incoming.flowSeen
	}
	// The device list is the current state. Flow-log metadata is a snapshot
	// kept for the whole retention window, so it must not add tags or
	// addresses an API device no longer has. Older addresses still resolve to
	// the device so historical flows keep their attribution.
	if !existing.fromAPI {
		existing.IPs = unionStrings(existing.IPs, incoming.IPs)
		existing.Tags = unionStrings(existing.Tags, incoming.Tags)
	} else {
		for _, ip := range incoming.IPs {
			c.mapIPLocked(ip, existing)
		}
	}
	if next := preferredID(existing.ID, incoming.ID); next != existing.ID {
		c.rekeyLocked(existing, next)
	}
	if existing.NodeID == "" && isStableNodeID(existing.ID) {
		existing.NodeID = existing.ID
	}
	c.aliasLocked(incoming.ID, existing.ID)
	c.aliasLocked(legacy, existing.ID)
	c.aliasLocked(incoming.legacyID, existing.ID)
	for _, ip := range existing.IPs {
		c.mapIPLocked(ip, existing)
	}
}

func (c *DeviceCache) deviceLocked(id string) *DeviceCacheEntry {
	if id == "" {
		return nil
	}
	if entry, ok := c.idToDevice[id]; ok {
		return entry
	}
	if canonical, ok := c.aliases[id]; ok {
		return c.idToDevice[canonical]
	}
	return nil
}

func (c *DeviceCache) aliasLocked(alias, canonical string) {
	if alias == "" || canonical == "" || alias == canonical {
		return
	}
	if c.aliases == nil {
		c.aliases = make(map[string]string)
	}
	if _, exists := c.idToDevice[alias]; exists {
		return
	}
	if current, ok := c.aliases[alias]; ok && current == canonical {
		return
	}
	c.aliases[alias] = canonical
	if entry := c.idToDevice[canonical]; entry != nil {
		entry.aliases = append(entry.aliases, alias)
	}
}

func (c *DeviceCache) rekeyLocked(entry *DeviceCacheEntry, newID string) {
	if entry == nil || newID == "" || entry.ID == newID {
		return
	}
	old := entry.ID
	delete(c.idToDevice, old)
	entry.ID = newID
	c.idToDevice[newID] = entry
	if c.aliases == nil {
		c.aliases = make(map[string]string)
	}
	delete(c.aliases, newID)
	kept := entry.aliases[:0]
	for _, alias := range entry.aliases {
		if alias == newID || c.aliases[alias] != old {
			continue
		}
		c.aliases[alias] = newID
		kept = append(kept, alias)
	}
	entry.aliases = kept
	c.aliasLocked(old, newID)
}

func preferredID(current, incoming string) string {
	if isStableNodeID(incoming) && !isStableNodeID(current) {
		return incoming
	}
	return current
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
	// fd7a:115c:a1e0::/48
	return len(ip) == net.IPv6len &&
		ip[0] == 0xfd && ip[1] == 0x7a &&
		ip[2] == 0x11 && ip[3] == 0x5c &&
		ip[4] == 0xa1 && ip[5] == 0xe0
}

func unionStrings(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, item := range base {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	for _, item := range extra {
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

// PruneFlowOnly drops flow-log devices that are not in the Tailscale device
// list and have not been seen in a flow inside the retention window.
// API devices are left alone. A non-positive retention keeps every device.
func (c *DeviceCache) PruneFlowOnly(retention time.Duration, now time.Time) int {
	if c == nil || retention <= 0 || now.IsZero() {
		return 0
	}
	cutoff := now.Add(-retention)
	c.mu.Lock()
	defer c.mu.Unlock()

	removed := 0
	for id, entry := range c.idToDevice {
		if entry.fromAPI || entry.flowSeen.IsZero() || !entry.flowSeen.Before(cutoff) {
			continue
		}
		c.removeLocked(id, entry)
		removed++
	}
	return removed
}

func (c *DeviceCache) removeLocked(id string, entry *DeviceCacheEntry) {
	delete(c.idToDevice, id)
	delete(c.aliases, id)
	for _, alias := range entry.aliases {
		if c.aliases[alias] == id {
			delete(c.aliases, alias)
		}
	}
	for _, ip := range entry.IPs {
		if c.ipToDevice[ip] == entry {
			delete(c.ipToDevice, ip)
		}
	}
}

func (c *DeviceCache) Devices() []Device {
	c.mu.RLock()
	defer c.mu.RUnlock()

	devices := make([]Device, 0, len(c.idToDevice))
	for _, entry := range c.idToDevice {
		nodeID := entry.NodeID
		if nodeID == "" && isStableNodeID(entry.ID) {
			nodeID = entry.ID
		}
		devices = append(devices, Device{
			ID:                 entry.ID,
			NodeID:             nodeID,
			Name:               entry.Name,
			Hostname:           entry.Hostname,
			User:               entry.Owner,
			OS:                 entry.OS,
			Addresses:          append([]string(nil), entry.IPs...),
			Online:             entry.Online,
			ConnectedToControl: entry.ConnectedToControl,
			LastSeen:           entry.LastSeen,
			Authorized:         entry.Authorized,
			Created:            entry.Created,
			ClientVersion:      entry.ClientVersion,
			Tags:               append([]string(nil), entry.Tags...),
		})
	}
	return withEmptyLists(devices)
}

// EquivalentIDs returns the canonical id and every alias for the same node.
// An unknown id is returned unchanged so callers can still query stored rows.
func (c *DeviceCache) EquivalentIDs(id string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if id == "" {
		return nil
	}
	entry := c.deviceLocked(id)
	if entry == nil {
		return []string{id}
	}
	extras := make([]string, 0, len(entry.aliases))
	seen := make(map[string]struct{}, len(entry.aliases))
	for _, alias := range entry.aliases {
		if alias == entry.ID || c.aliases[alias] != entry.ID {
			continue
		}
		if _, dup := seen[alias]; dup {
			continue
		}
		seen[alias] = struct{}{}
		extras = append(extras, alias)
	}
	sort.Strings(extras)
	return append([]string{entry.ID}, extras...)
}

// ResolveIP returns the canonical device ID for an IP, or the IP itself if not found.
func (c *DeviceCache) ResolveIP(ip string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if entry, ok := c.ipToDevice[ip]; ok {
		return entry.ID
	}
	return ip
}

// GetDevice returns a copy of the device info for a canonical id or any alias.
// Merges update cached entries in place, so callers get a snapshot taken
// under the lock rather than a pointer into the cache.
func (c *DeviceCache) GetDevice(id string) *DeviceCacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return snapshotEntry(c.deviceLocked(id))
}

// GetDeviceByIP returns a copy of the device info for an IP address.
func (c *DeviceCache) GetDeviceByIP(ip string) *DeviceCacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return snapshotEntry(c.ipToDevice[ip])
}

func snapshotEntry(entry *DeviceCacheEntry) *DeviceCacheEntry {
	if entry == nil {
		return nil
	}
	snapshot := *entry
	snapshot.IPs = append([]string(nil), entry.IPs...)
	snapshot.Tags = append([]string(nil), entry.Tags...)
	snapshot.aliases = nil
	return &snapshot
}

// NeedsRefresh returns true if cache is stale
func (c *DeviceCache) NeedsRefresh(maxAge time.Duration) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return time.Since(c.lastRefresh) > maxAge
}
