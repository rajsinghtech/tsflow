package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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
	ClientVersion             string   `json:"clientVersion"`
	UpdateAvailable           bool     `json:"updateAvailable"`
	BlocksIncomingConnections bool     `json:"blocksIncomingConnections"`
	EnabledRoutes             []string `json:"enabledRoutes"`
	AdvertisedRoutes       []string `json:"advertisedRoutes"`
	Tags                   []string `json:"tags"`
}

type DevicesResponse struct {
	Devices []Device `json:"devices"`
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
		ts.client = &http.Client{
			Timeout: 30 * time.Minute, // Much longer timeout for large requests
		}
		ts.tsClient = &tailscale.Client{
			APIKey:  cfg.TailscaleAPIKey,
			Tailnet: cfg.TailscaleTailnet,
		}
		ts.useOAuth = false
	} else {
		ts.client = &http.Client{
			Timeout: 30 * time.Minute, // Much longer timeout for large requests
		}
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
		body, _ := io.ReadAll(resp.Body)
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

func (ts *TailscaleService) GetDevices() (*DevicesResponse, error) {
	if ts.tsClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		
		devices, err := ts.tsClient.Devices().List(ctx)
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
				LastSeen:               device.LastSeen.Time.Format(time.RFC3339),
				Authorized:             device.Authorized,
				KeyExpiryDisabled:      device.KeyExpiryDisabled,
				Created:                device.Created.Time.Format(time.RFC3339),
				MachineKey:             device.MachineKey,
				NodeKey:                device.NodeKey,
				ClientVersion:          device.ClientVersion,
				UpdateAvailable:        device.UpdateAvailable,
				BlocksIncomingConnections: device.BlocksIncomingConnections,
				EnabledRoutes:          device.EnabledRoutes,
				AdvertisedRoutes:       device.AdvertisedRoutes,
				Tags:                   device.Tags,
			})
		}
		
		return &DevicesResponse{Devices: ourDevices}, nil
	}
	
	// Fallback to old implementation
	endpoint := fmt.Sprintf("/tailnet/%s/devices", ts.tailnet)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	body, err := ts.makeRequest(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	var response DevicesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal devices response: %w", err)
	}

	return &response, nil
}

func (ts *TailscaleService) GetUsers() ([]byte, error) {
	endpoint := fmt.Sprintf("/tailnet/%s/users", ts.tailnet)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return ts.makeRequest(ctx, endpoint)
}

func (ts *TailscaleService) GetPolicy() ([]byte, error) {
	endpoint := fmt.Sprintf("/tailnet/%s/acl", ts.tailnet)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return ts.makeRequest(ctx, endpoint)
}

func (ts *TailscaleService) GetNetworkLogs(start, end string) (any, error) {
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
		ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
		defer cancel()
		
		var logs []tailscale.NetworkFlowLog
		
		err = ts.tsClient.Logging().GetNetworkFlowLogs(ctx, tailscale.NetworkFlowLogsRequest{
			Start: startTime,
			End:   endTime,
		}, func(log tailscale.NetworkFlowLog) error {
			logs = append(logs, log)
			return nil
		})
		
		if err != nil {
			return nil, fmt.Errorf("failed to fetch network logs from tailscale client: %w", err)
		}

		return map[string]any{
			"logs": logs,
		}, nil
	}
	
	// Fallback to old implementation
	endpoint := fmt.Sprintf("/tailnet/%s/logging/network?start=%s&end=%s",
		ts.tailnet, url.QueryEscape(start), url.QueryEscape(end))

	// Use much longer timeout for larger time ranges
	timeoutDuration := 10 * time.Minute
	if endTime.Sub(startTime) > 7*24*time.Hour {
		timeoutDuration = 30 * time.Minute // Much longer timeout for 30+ day queries
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
	defer cancel()

	body, err := ts.makeRequest(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch network logs: %w", err)
	}

	var response any
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal network logs response: %w", err)
	}

	// Ensure consistent response format
	if responseMap, ok := response.(map[string]any); ok {
		if logs, exists := responseMap["logs"]; exists {
			return map[string]any{
				"logs": logs,
			}, nil
		}
	}
	return map[string]any{
		"logs": response,
	}, nil
}

// GetNetworkLogsChunked retrieves network logs in chunks for large time ranges
func (ts *TailscaleService) GetNetworkLogsChunked(start, end string, chunkSize time.Duration) ([]any, error) {
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
		result, err := ts.GetNetworkLogs(start, end)
		if err != nil {
			return nil, err
		}
		return []any{result}, nil
	}

	// Split the time range into chunks
	var allLogs []any
	currentStart := startTime

	for currentStart.Before(endTime) {
		currentEnd := currentStart.Add(chunkSize)
		if currentEnd.After(endTime) {
			currentEnd = endTime
		}

		// Fetch logs for this chunk
		logs, err := ts.GetNetworkLogs(
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
func (ts *TailscaleService) GetNetworkLogsChunkedParallel(start, end string, chunkSize time.Duration, maxConcurrency int) ([]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	return ts.GetNetworkLogsChunkedParallelWithContext(ctx, start, end, chunkSize, maxConcurrency)
}

// GetNetworkLogsChunkedParallelWithContext retrieves network logs in parallel chunks with context support
func (ts *TailscaleService) GetNetworkLogsChunkedParallelWithContext(ctx context.Context, start, end string, chunkSize time.Duration, maxConcurrency int) ([]any, error) {
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
		result, err := ts.GetNetworkLogs(start, end)
		if err != nil {
			return nil, err
		}
		return []any{result}, nil
	}

	// Channel for collecting results - buffered to prevent goroutine leaks
	type result struct {
		index int
		logs  any
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
					resultsChan <- result{
						index: index,
						logs:  nil,
						err:   fmt.Errorf("panic recovered: %v", r),
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
	results := make([]any, len(chunks))
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

	// Filter out nil results and maintain order
	var allLogs []any
	failedChunks := 0
	for _, logs := range results {
		if logs != nil {
			allLogs = append(allLogs, logs)
		} else {
			failedChunks++
		}
	}

	if hasError && len(allLogs) == 0 {
		return nil, fmt.Errorf("failed to fetch any logs from parallel requests")
	}

	// Warn about partial failures - data may be incomplete
	if failedChunks > 0 {
		log.Printf("Warning: %d/%d chunks failed during parallel fetch - results may be incomplete", failedChunks, len(chunks))
	}

	return allLogs, nil
}

// GetNetworkMap retrieves the network map (simplified version)
func (ts *TailscaleService) GetNetworkMap() (map[string]any, error) {
	// Get devices as the basis for network map
	devices, err := ts.GetDevices()
	if err != nil {
		return nil, err
	}

	// Create a simplified network map
	networkMap := map[string]any{
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
func (ts *TailscaleService) GetDeviceFlows(deviceID string) (map[string]any, error) {
	return nil, fmt.Errorf("device flows API not implemented: Tailscale does not expose per-device flow data")
}

// GetDNSNameservers retrieves DNS config for the tailnet
func (ts *TailscaleService) GetDNSNameservers() (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get nameservers
	nameserversBody, err := ts.makeRequest(ctx, fmt.Sprintf("/tailnet/%s/dns/nameservers", ts.tailnet))
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(nameserversBody, &result); err != nil {
		return nil, err
	}

	// Get preferences
	prefsBody, err := ts.makeRequest(ctx, fmt.Sprintf("/tailnet/%s/dns/preferences", ts.tailnet))
	if err == nil {
		var prefs map[string]any
		if json.Unmarshal(prefsBody, &prefs) == nil {
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
	dns, _ := result["dns"].([]any)
	magicDNSEnabled, _ := result["magicDNS"].(bool)
	if len(dns) == 0 && magicDNSEnabled {
		result["dns"] = []string{"100.100.100.100"}
	}

	return result, nil
}

// VIPServiceInfo represents a VIP service from the Tailscale API
type VIPServiceInfo struct {
	Name  string   `json:"name"`
	Addrs []string `json:"addrs"`
	Tags  []string `json:"tags,omitempty"`
}

// StaticRecordInfo represents a static DNS record
type StaticRecordInfo struct {
	Addrs   []string `json:"addrs"`
	Comment string   `json:"comment,omitempty"`
}

// GetVIPServices fetches all VIP services (virtual IP services) for the tailnet
func (ts *TailscaleService) GetVIPServices(ctx context.Context) (map[string]VIPServiceInfo, error) {
	endpoint := fmt.Sprintf("/tailnet/%s/services", url.PathEscape(ts.tailnet))
	
	body, err := ts.makeRequest(ctx, endpoint)
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
	endpoint := fmt.Sprintf("/tailnet/%s/static-records", url.PathEscape(ts.tailnet))
	
	body, err := ts.makeRequest(ctx, endpoint)
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
