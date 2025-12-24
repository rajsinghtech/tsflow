package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"runtime/debug"
	"sync"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/utils"
	tailscale "tailscale.com/client/tailscale/v2"
)

type TailscaleService struct {
	apiKey   string
	tailnet  string
	baseURL  string
	client   *http.Client
	useOAuth bool
	tsClient *tailscale.Client
}

type Device struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	Hostname               string   `json:"hostname"`
	User                   string   `json:"user"`
	OS                     string   `json:"os"`
	Addresses              []string `json:"addresses"`
	Online                 bool     `json:"online"`
	LastSeen               string   `json:"lastSeen"`
	Authorized             bool     `json:"authorized"`
	KeyExpiryDisabled      bool     `json:"keyExpiryDisabled"`
	Created                string   `json:"created"`
	MachineKey             string   `json:"machineKey"`
	NodeKey                string   `json:"nodeKey"`
	ClientVersion          string   `json:"clientVersion"`
	UpdateAvailable        bool     `json:"updateAvailable"`
	Blocksincomingnonnodes bool     `json:"blocksIncomingnonnodes"`
	EnabledRoutes          []string `json:"enabledRoutes"`
	AdvertisedRoutes       []string `json:"advertisedRoutes"`
	Tags                   []string `json:"tags"`
}

type DevicesResponse struct {
	Devices []Device `json:"devices"`
}

type NetworkLogEntry struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	Protocol  string `json:"protocol"`
	Action    string `json:"action"`
}

type NetworkLogsResponse struct {
	Logs []NetworkLogEntry `json:"logs"`
}

func NewTailscaleService(cfg *config.Config) *TailscaleService {
	ts := &TailscaleService{
		tailnet: cfg.TailscaleTailnet,
		baseURL: cfg.TailscaleAPIURL,
	}

	if cfg.TailscaleOAuthClientID != "" && cfg.TailscaleOAuthClientSecret != "" {
		// Use the Tailscale client's built-in OAuth support
		oauthConfig := tailscale.OAuthConfig{
			ClientID:     cfg.TailscaleOAuthClientID,
			ClientSecret: cfg.TailscaleOAuthClientSecret,
			Scopes:       cfg.TailscaleOAuthScopes,
		}
		
		ts.tsClient = &tailscale.Client{
			HTTP:     oauthConfig.HTTPClient(),
			Tailnet:  cfg.TailscaleTailnet,
		}
		ts.client = oauthConfig.HTTPClient()
		ts.useOAuth = true
	} else if cfg.TailscaleAPIKey != "" {
		ts.apiKey = cfg.TailscaleAPIKey
		// No global timeout - let request contexts control timeouts
		ts.client = &http.Client{}
		ts.tsClient = &tailscale.Client{
			APIKey:  cfg.TailscaleAPIKey,
			Tailnet: cfg.TailscaleTailnet,
		}
		ts.useOAuth = false
	} else {
		// No global timeout - let request contexts control timeouts
		ts.client = &http.Client{}
	}

	return ts
}

func (ts *TailscaleService) makeRequest(ctx context.Context, endpoint string) ([]byte, error) {
	return ts.makeRequestWithRetry(ctx, endpoint, 3, 1*time.Second)
}

func (ts *TailscaleService) makeRequestWithRetry(ctx context.Context, endpoint string, maxRetries int, initialDelay time.Duration) ([]byte, error) {
	var lastErr error
	delay := initialDelay

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			delay *= 2
		}

		body, err := ts.doRequest(ctx, endpoint)
		if err == nil {
			return body, nil
		}

		lastErr = err

		if !ts.isRetryableError(err) {
			return nil, err
		}

		if attempt < maxRetries {
			log.Printf("Request failed (attempt %d/%d), retrying in %v: %v", attempt+1, maxRetries+1, delay, err)
		}
	}

	return nil, fmt.Errorf("request failed after %d attempts: %w", maxRetries+1, lastErr)
}

func (ts *TailscaleService) doRequest(ctx context.Context, endpoint string) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v2%s", ts.baseURL, endpoint)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if !ts.useOAuth && ts.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+ts.apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := ts.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read error response body (status %d): %w", resp.StatusCode, err)
		}
		return nil, utils.HTTPError(resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return body, nil
}

func (ts *TailscaleService) isRetryableError(err error) bool {
	return utils.IsRetryable(err)
}

func (ts *TailscaleService) GetDevices(ctx context.Context) (*DevicesResponse, error) {
	if ts.tsClient != nil {
		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()

		devices, err := ts.tsClient.Devices().List(timeoutCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to get devices from tailscale client: %w", err)
		}
		
		// Convert tailscale client devices to our format
		var ourDevices []Device
		for _, device := range devices {
			ourDevices = append(ourDevices, Device{
				ID:                     device.ID,
				Name:                   device.Name,
				Hostname:               device.Hostname,
				User:                   device.User,
				OS:                     device.OS,
				Addresses:              device.Addresses,
				Online:                 !device.LastSeen.IsZero() && time.Since(device.LastSeen.Time) < 2*time.Minute,
				LastSeen:               func() string { if device.LastSeen.IsZero() { return "" }; return device.LastSeen.Time.Format(time.RFC3339) }(),
				Authorized:             device.Authorized,
				KeyExpiryDisabled:      device.KeyExpiryDisabled,
				Created:                device.Created.Time.Format(time.RFC3339),
				MachineKey:             device.MachineKey,
				NodeKey:                device.NodeKey,
				ClientVersion:          device.ClientVersion,
				UpdateAvailable:        device.UpdateAvailable,
				Blocksincomingnonnodes: device.BlocksIncomingConnections,
				EnabledRoutes:          device.EnabledRoutes,
				AdvertisedRoutes:       device.AdvertisedRoutes,
				Tags:                   device.Tags,
			})
		}
		
		return &DevicesResponse{Devices: ourDevices}, nil
	}
	
	// Fallback to old implementation
	endpoint := fmt.Sprintf("/tailnet/%s/devices", ts.tailnet)

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	body, err := ts.makeRequest(timeoutCtx, endpoint)
	if err != nil {
		return nil, err
	}

	var response DevicesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal devices response: %w", err)
	}

	return &response, nil
}

func (ts *TailscaleService) GetNetworkLogs(ctx context.Context, start, end string) (interface{}, error) {
	// Parse time range to determine if we need chunking
	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return nil, fmt.Errorf("invalid start time: %w", err)
	}

	endTime, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return nil, fmt.Errorf("invalid end time: %w", err)
	}


	// For smaller ranges, use the original approach
	if ts.tsClient != nil {
		// Use much longer timeout for larger time ranges
		timeoutDuration := 10 * time.Minute
		if endTime.Sub(startTime) > 7*24*time.Hour {
			timeoutDuration = 30 * time.Minute // Much longer timeout for 30+ day queries
		}
		timeoutCtx, cancel := context.WithTimeout(ctx, timeoutDuration)
		defer cancel()

		var logs []tailscale.NetworkFlowLog
		var mu sync.Mutex

		err = ts.tsClient.Logging().GetNetworkFlowLogs(timeoutCtx, tailscale.NetworkFlowLogsRequest{
			Start: startTime,
			End:   endTime,
		}, func(log tailscale.NetworkFlowLog) error {
			mu.Lock()
			logs = append(logs, log)
			mu.Unlock()
			return nil
		})

		if err != nil {
			return nil, fmt.Errorf("failed to fetch network logs from tailscale client: %w", err)
		}


		return map[string]interface{}{
			"logs": logs,
		}, nil
	}
	
	// Fallback to old implementation
	endpoint := fmt.Sprintf("/tailnet/%s/logging/network", ts.tailnet)

	if start != "" && end != "" {
		endpoint += fmt.Sprintf("?start=%s&end=%s", url.QueryEscape(start), url.QueryEscape(end))
	}

	// Use much longer timeout for larger time ranges
	timeoutDuration := 10 * time.Minute
	if endTime.Sub(startTime) > 7*24*time.Hour {
		timeoutDuration = 30 * time.Minute // Much longer timeout for 30+ day queries
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, timeoutDuration)
	defer cancel()

	body, err := ts.makeRequest(timeoutCtx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch network logs: %w", err)
	}

	var response interface{}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal network logs response: %w", err)
	}

	// Ensure consistent response format
	if responseMap, ok := response.(map[string]interface{}); ok {
		if logs, exists := responseMap["logs"]; exists {
			return map[string]interface{}{
				"logs": logs,
			}, nil
		}
	}
	return map[string]interface{}{
		"logs": response,
	}, nil
}

// GetNetworkLogsChunked retrieves network logs in chunks for large time ranges
func (ts *TailscaleService) GetNetworkLogsChunked(ctx context.Context, start, end string, chunkSize time.Duration) ([]interface{}, error) {
	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return nil, fmt.Errorf("invalid start time: %w", err)
	}

	endTime, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return nil, fmt.Errorf("invalid end time: %w", err)
	}

	// If the time range is small enough, use the regular method
	if endTime.Sub(startTime) <= chunkSize {
		result, err := ts.GetNetworkLogs(ctx, start, end)
		if err != nil {
			return nil, err
		}
		return []interface{}{result}, nil
	}

	// Split the time range into chunks
	var allLogs []interface{}
	currentStart := startTime

	for currentStart.Before(endTime) {
		currentEnd := currentStart.Add(chunkSize)
		if currentEnd.After(endTime) {
			currentEnd = endTime
		}

		// Fetch logs for this chunk
		logs, err := ts.GetNetworkLogs(
			ctx,
			currentStart.Format(time.RFC3339),
			currentEnd.Format(time.RFC3339),
		)
		if err != nil {
			// Log the error but continue with other chunks
			log.Printf("Error fetching logs for chunk %s to %s: %v", 
				currentStart.Format(time.RFC3339), 
				currentEnd.Format(time.RFC3339), 
				err)
		} else if logs != nil {
			allLogs = append(allLogs, logs)
		}

		currentStart = currentEnd
	}

	return allLogs, nil
}

// GetNetworkLogsChunkedParallel retrieves network logs in parallel chunks for large time ranges
func (ts *TailscaleService) GetNetworkLogsChunkedParallel(ctx context.Context, start, end string, chunkSize time.Duration, maxConcurrency int) ([]interface{}, error) {
	// Use a longer timeout for chunked queries to accommodate large time ranges
	// Each chunk may need up to 30 minutes, and with parallel execution we need headroom
	timeoutCtx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()
	return ts.GetNetworkLogsChunkedParallelWithContext(timeoutCtx, start, end, chunkSize, maxConcurrency)
}

// GetNetworkLogsChunkedParallelWithContext retrieves network logs in parallel chunks with context support
func (ts *TailscaleService) GetNetworkLogsChunkedParallelWithContext(ctx context.Context, start, end string, chunkSize time.Duration, maxConcurrency int) ([]interface{}, error) {
	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return nil, fmt.Errorf("invalid start time: %w", err)
	}

	endTime, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return nil, fmt.Errorf("invalid end time: %w", err)
	}

	// Calculate chunks
	var chunks []struct{ start, end time.Time }
	currentStart := startTime

	for currentStart.Before(endTime) {
		currentEnd := currentStart.Add(chunkSize)
		if currentEnd.After(endTime) {
			currentEnd = endTime
		}
		chunks = append(chunks, struct{ start, end time.Time }{currentStart, currentEnd})
		currentStart = currentEnd
	}

	// If only one chunk, use regular method
	if len(chunks) <= 1 {
		result, err := ts.GetNetworkLogs(ctx, start, end)
		if err != nil {
			return nil, err
		}
		return []interface{}{result}, nil
	}

	// Channel for collecting results - buffered to prevent goroutine leaks
	type result struct {
		index int
		logs  interface{}
		err   error
	}
	resultsChan := make(chan result, len(chunks))

	// Semaphore for concurrency control
	semaphore := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	// Launch parallel requests
	for i, chunk := range chunks {
		wg.Add(1)
		go func(index int, chunkStart, chunkEnd time.Time) {
			defer wg.Done()
			
			// Recover from panics
			defer func() {
				if r := recover(); r != nil {
					stack := debug.Stack()
					resultsChan <- result{
						index: index,
						logs:  nil,
						err:   fmt.Errorf("panic recovered: %v\nstack trace:\n%s", r, string(stack)),
					}
				}
			}()
			
			// Check context before proceeding
			select {
			case <-ctx.Done():
				resultsChan <- result{
					index: index,
					logs:  nil,
					err:   ctx.Err(),
				}
				return
			default:
			}
			
			// Acquire semaphore
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				resultsChan <- result{
					index: index,
					logs:  nil,
					err:   ctx.Err(),
				}
				return
			}

			logs, err := ts.GetNetworkLogs(
				ctx,
				chunkStart.Format(time.RFC3339),
				chunkEnd.Format(time.RFC3339),
			)
			
			resultsChan <- result{
				index: index,
				logs:  logs,
				err:   err,
			}
		}(i, chunk.start, chunk.end)
	}

	// Close results channel when all goroutines complete
	go func() {
		defer close(resultsChan)
		wg.Wait()
	}()

	// Collect results
	results := make([]interface{}, len(chunks))
	var hasError bool

	for res := range resultsChan {
		// Bounds check to prevent slice access panic
		if res.index < 0 || res.index >= len(results) {
			log.Printf("Warning: invalid result index %d, skipping", res.index)
			continue
		}
		
		if res.err != nil {
			log.Printf("Error fetching chunk %d: %v", res.index, res.err)
			hasError = true
			// Store nil for failed chunks
			results[res.index] = nil
		} else {
			results[res.index] = res.logs
		}
	}

	// Collect all logs while maintaining chronological order
	// Keep track of which chunks succeeded for better error reporting
	var allLogs []interface{}
	successCount := 0
	for i, logs := range results {
		if logs != nil {
			allLogs = append(allLogs, logs)
			successCount++
		} else if hasError {
			// Log which time period failed for debugging
			if i < len(chunks) {
				log.Printf("Warning: missing data for chunk %d (time range: %s to %s)",
					i, chunks[i].start.Format(time.RFC3339), chunks[i].end.Format(time.RFC3339))
			}
		}
	}

	if hasError && len(allLogs) == 0 {
		return nil, fmt.Errorf("failed to fetch any logs from parallel requests")
	}

	if hasError {
		log.Printf("Warning: partial data returned - %d of %d chunks succeeded", successCount, len(chunks))
	}

	return allLogs, nil
}

// GetNetworkMap retrieves the network map (simplified version)
func (ts *TailscaleService) GetNetworkMap() (map[string]interface{}, error) {
	// Get devices as the basis for network map
	devices, err := ts.GetDevices(context.Background())
	if err != nil {
		return nil, err
	}

	// Create a simplified network map
	networkMap := map[string]interface{}{
		"tailnet":       ts.tailnet,
		"devices":       devices.Devices,
		"total_devices": len(devices.Devices),
		"online_devices": func() int {
			count := 0
			for _, device := range devices.Devices {
				if device.Online {
					count++
				}
			}
			return count
		}(),
	}

	return networkMap, nil
}

// GetDeviceFlows retrieves flow data for a specific device
func (ts *TailscaleService) GetDeviceFlows(deviceID string) (map[string]interface{}, error) {
	// Note: This is a placeholder as Tailscale doesn't have a public device flows API
	// In a real implementation, you might need to implement this differently
	// or collect this data through other means

	// For now, return mock data
	flows := map[string]interface{}{
		"device_id": deviceID,
		"flows": []map[string]interface{}{
			{
				"source":      "unknown",
				"destination": "unknown",
				"protocol":    "tcp",
				"bytes":       0,
				"packets":     0,
			},
		},
		"total_flows": 0,
	}

	return flows, nil
}

// GetDNSNameservers retrieves DNS config for the tailnet
func (ts *TailscaleService) GetDNSNameservers(ctx context.Context) (map[string]interface{}, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Get nameservers
	nameserversBody, err := ts.makeRequest(timeoutCtx, fmt.Sprintf("/tailnet/%s/dns/nameservers", ts.tailnet))
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(nameserversBody, &result); err != nil {
		return nil, err
	}

	// Get preferences
	prefsBody, err := ts.makeRequest(timeoutCtx, fmt.Sprintf("/tailnet/%s/dns/preferences", ts.tailnet))
	if err != nil {
		// Preferences endpoint might not be available, continue with defaults
		log.Printf("failed to fetch DNS preferences: %v", err)
	} else {
		var prefs map[string]interface{}
		if err := json.Unmarshal(prefsBody, &prefs); err != nil {
			log.Printf("failed to parse DNS preferences: %v", err)
		} else {
			result["magicDNS"] = prefs["magicDNS"]
			if domains, ok := prefs["searchDomains"]; ok {
				result["domains"] = domains
			}
		}
	}

	// Default values
	if result["magicDNS"] == nil {
		result["magicDNS"] = false
	}
	if result["domains"] == nil {
		result["domains"] = []string{}
	}

	// Show MagicDNS resolver when enabled
	dns, _ := result["dns"].([]interface{})
	if len(dns) == 0 && result["magicDNS"] == true {
		result["dns"] = []string{"100.100.100.100"}
	}

	return result, nil
}

// VIPServiceInfo represents a VIP service from the Tailscale API
type VIPServiceInfo struct {
	Name  string   `json:"name"`
	Addrs []string `json:"addrs"`
}

// StaticRecordInfo represents a static DNS record
type StaticRecordInfo struct {
	Addrs   []string `json:"addrs"`
	Comment string   `json:"comment,omitempty"`
}

// GetVIPServices fetches all VIP services (virtual IP services) for the tailnet
func (ts *TailscaleService) GetVIPServices(ctx context.Context) (map[string]VIPServiceInfo, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf("/tailnet/%s/services", url.PathEscape(ts.tailnet))

	body, err := ts.makeRequest(timeoutCtx, endpoint)
	if err != nil {
		// VIP services might not be available for all tailnets
		// Return empty map instead of error for graceful degradation
		return make(map[string]VIPServiceInfo), nil
	}
	
	var response struct {
		VIPServices []VIPServiceInfo `json:"vipServices"`
	}
	
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse VIP services: %w", err)
	}
	
	// Convert to map keyed by service name for easy lookup
	services := make(map[string]VIPServiceInfo)
	for _, svc := range response.VIPServices {
		services[svc.Name] = svc
	}
	
	return services, nil
}

// GetStaticRecords fetches all static DNS records for the tailnet
func (ts *TailscaleService) GetStaticRecords(ctx context.Context) (map[string]StaticRecordInfo, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf("/tailnet/%s/static-records", url.PathEscape(ts.tailnet))

	body, err := ts.makeRequest(timeoutCtx, endpoint)
	if err != nil {
		// Static records might not be available for all tailnets
		// Return empty map instead of error for graceful degradation
		return make(map[string]StaticRecordInfo), nil
	}
	
	var response struct {
		Records map[string]StaticRecordInfo `json:"records"`
	}
	
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse static records: %w", err)
	}
	
	return response.Records, nil
}

// Helper function to get map keys for debugging
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
