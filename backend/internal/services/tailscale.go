package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/utils"
	"golang.org/x/oauth2/clientcredentials"
)

type TailscaleService struct {
	apiKey      string
	oauthConfig *clientcredentials.Config
	tailnet     string
	baseURL     string
	client      *http.Client
	useOAuth    bool
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
		ts.oauthConfig = &clientcredentials.Config{
			ClientID:     cfg.TailscaleOAuthClientID,
			ClientSecret: cfg.TailscaleOAuthClientSecret,
			Scopes:       cfg.TailscaleOAuthScopes,
			TokenURL:     cfg.TailscaleAPIURL + "/api/v2/oauth/token",
		}
		ts.client = ts.oauthConfig.Client(context.Background())
		ts.client.Timeout = 5 * time.Minute
		ts.useOAuth = true
	} else if cfg.TailscaleAPIKey != "" {
		ts.apiKey = cfg.TailscaleAPIKey
		ts.client = &http.Client{
			Timeout: 5 * time.Minute,
		}
		ts.useOAuth = false
	} else {
		ts.client = &http.Client{
			Timeout: 5 * time.Minute,
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
			fmt.Printf("Request failed (attempt %d/%d), retrying in %v: %v\n", attempt+1, maxRetries+1, delay, err)
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

func (ts *TailscaleService) GetNetworkLogs(start, end string) (interface{}, error) {
	endpoint := fmt.Sprintf("/tailnet/%s/logging/network", ts.tailnet)

	if start != "" && end != "" {
		endpoint += fmt.Sprintf("?start=%s&end=%s", url.QueryEscape(start), url.QueryEscape(end))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	body, err := ts.makeRequest(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch network logs: %w", err)
	}

	var response interface{}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal network logs response: %w", err)
	}

	return response, nil
}

// GetNetworkLogsChunked retrieves network logs in chunks for large time ranges
func (ts *TailscaleService) GetNetworkLogsChunked(start, end string, chunkSize time.Duration) ([]interface{}, error) {
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
			currentStart.Format(time.RFC3339),
			currentEnd.Format(time.RFC3339),
		)
		if err != nil {
			// Log the error but continue with other chunks
			fmt.Printf("Error fetching logs for chunk %s to %s: %v\n", 
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
func (ts *TailscaleService) GetNetworkLogsChunkedParallel(start, end string, chunkSize time.Duration, maxConcurrency int) ([]interface{}, error) {
	return ts.GetNetworkLogsChunkedParallelWithContext(context.Background(), start, end, chunkSize, maxConcurrency)
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
		result, err := ts.GetNetworkLogs(start, end)
		if err != nil {
			return nil, err
		}
		return []interface{}{result}, nil
	}

	// Channel for collecting results
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
		wg.Wait()
		close(resultsChan)
	}()

	// Collect results
	results := make([]interface{}, len(chunks))
	var hasError bool

	for res := range resultsChan {
		if res.err != nil {
			fmt.Printf("Error fetching chunk %d: %v\n", res.index, res.err)
			hasError = true
			// Store nil for failed chunks
			results[res.index] = nil
		} else {
			results[res.index] = res.logs
		}
	}

	// Filter out nil results and maintain order
	var allLogs []interface{}
	for _, logs := range results {
		if logs != nil {
			allLogs = append(allLogs, logs)
		}
	}

	if hasError && len(allLogs) == 0 {
		return nil, fmt.Errorf("failed to fetch any logs from parallel requests")
	}

	return allLogs, nil
}

// GetNetworkMap retrieves the network map (simplified version)
func (ts *TailscaleService) GetNetworkMap() (map[string]interface{}, error) {
	// Get devices as the basis for network map
	devices, err := ts.GetDevices()
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
func (ts *TailscaleService) GetDNSNameservers() (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get nameservers
	nameserversBody, err := ts.makeRequest(ctx, fmt.Sprintf("/tailnet/%s/dns/nameservers", ts.tailnet))
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(nameserversBody, &result); err != nil {
		return nil, err
	}

	// Get preferences
	prefsBody, err := ts.makeRequest(ctx, fmt.Sprintf("/tailnet/%s/dns/preferences", ts.tailnet))
	if err == nil {
		var prefs map[string]interface{}
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
	dns, _ := result["dns"].([]interface{})
	if len(dns) == 0 && result["magicDNS"] == true {
		result["dns"] = []string{"100.100.100.100"}
	}

	return result, nil
}
