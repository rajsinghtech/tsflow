package services

import (
	"strings"
	"sync"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// DeviceCache maps IPs to device info for fast lookups
type DeviceCache struct {
	mu          sync.RWMutex
	ipToDevice  map[string]*DeviceCacheEntry
	idToDevice  map[string]*DeviceCacheEntry
	lastRefresh time.Time
}

type DeviceCacheEntry struct {
	ID          string
	Name        string
	Hostname    string
	Owner       string
	IPs         []string
	Tags        []string
	IsTailscale bool
}

func NewDeviceCache() *DeviceCache {
	return &DeviceCache{
		ipToDevice: make(map[string]*DeviceCacheEntry),
		idToDevice: make(map[string]*DeviceCacheEntry),
	}
}

func (c *DeviceCache) Update(devices []Device) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ipToDevice = make(map[string]*DeviceCacheEntry)
	c.idToDevice = make(map[string]*DeviceCacheEntry)

	for _, d := range devices {
		entry := &DeviceCacheEntry{
			ID:          d.ID,
			Name:        d.Name,
			Hostname:    d.Hostname,
			Owner:       d.User,
			IPs:         d.Addresses,
			Tags:        d.Tags,
			IsTailscale: true,
		}
		c.idToDevice[d.ID] = entry
		for _, ip := range d.Addresses {
			c.ipToDevice[ip] = entry
		}
	}
	c.lastRefresh = time.Now()
}

// UpsertFromFlowLogMetadata adds device identities embedded in exported
// Tailscale network-flow log objects. Object-store ingestion can therefore
// resolve names and tags without waiting for a separate devices API refresh.
func (c *DeviceCache) UpsertFromFlowLogMetadata(logMap map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if src, ok := logMap["srcNode"].(map[string]any); ok {
		c.upsertMetadataLocked(src)
	}
	if dstNodes, ok := logMap["dstNodes"].([]any); ok {
		for _, item := range dstNodes {
			if node, ok := item.(map[string]any); ok {
				c.upsertMetadataLocked(node)
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
		entry := &DeviceCacheEntry{
			ID:          node.NodeID,
			Name:        node.Name,
			Hostname:    hostname,
			Owner:       node.Owner,
			IPs:         append([]string(nil), node.IPs...),
			Tags:        append([]string(nil), node.Tags...),
			IsTailscale: true,
		}
		c.idToDevice[node.NodeID] = entry
		for _, ip := range node.IPs {
			c.ipToDevice[ip] = entry
		}
	}
	c.lastRefresh = time.Now()
}

func (c *DeviceCache) upsertMetadataLocked(node map[string]any) {
	id, _ := node["nodeId"].(string)
	if id == "" {
		return
	}

	name, _ := node["name"].(string)
	hostname := name
	if dot := strings.Index(hostname, "."); dot > 0 {
		hostname = hostname[:dot]
	}

	var addresses []string
	if rawAddrs, ok := node["addresses"].([]any); ok {
		for _, raw := range rawAddrs {
			if addr, ok := raw.(string); ok && addr != "" {
				addresses = append(addresses, addr)
			}
		}
	}

	var tags []string
	if rawTags, ok := node["tags"].([]any); ok {
		for _, raw := range rawTags {
			if tag, ok := raw.(string); ok && tag != "" {
				tags = append(tags, tag)
			}
		}
	}

	entry := &DeviceCacheEntry{
		ID:          id,
		Name:        name,
		Hostname:    hostname,
		IPs:         addresses,
		Tags:        tags,
		IsTailscale: true,
	}
	c.idToDevice[id] = entry
	for _, ip := range addresses {
		c.ipToDevice[ip] = entry
	}
}

func (c *DeviceCache) Devices() []Device {
	c.mu.RLock()
	defer c.mu.RUnlock()

	devices := make([]Device, 0, len(c.idToDevice))
	for _, entry := range c.idToDevice {
		devices = append(devices, Device{
			ID:         entry.ID,
			Name:       entry.Name,
			Hostname:   entry.Hostname,
			User:       entry.Owner,
			Addresses:  append([]string(nil), entry.IPs...),
			Tags:       append([]string(nil), entry.Tags...),
			Authorized: true,
		})
	}
	return devices
}

// ResolveIP returns the device ID for an IP, or the IP itself if not found
func (c *DeviceCache) ResolveIP(ip string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if entry, ok := c.ipToDevice[ip]; ok {
		return entry.ID
	}
	return ip // Return IP as-is for external addresses
}

// GetDevice returns device info by ID
func (c *DeviceCache) GetDevice(id string) *DeviceCacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.idToDevice[id]
}

// GetDeviceByIP returns device info by IP address
func (c *DeviceCache) GetDeviceByIP(ip string) *DeviceCacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ipToDevice[ip]
}

// NeedsRefresh returns true if cache is stale
func (c *DeviceCache) NeedsRefresh(maxAge time.Duration) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return time.Since(c.lastRefresh) > maxAge
}
