package services

import (
	"sync"
	"time"
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
			IsTailscale: true,
		}
		c.idToDevice[d.ID] = entry
		for _, ip := range d.Addresses {
			c.ipToDevice[ip] = entry
		}
	}
	c.lastRefresh = time.Now()
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
