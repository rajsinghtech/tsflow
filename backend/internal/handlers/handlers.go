package handlers

import (
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
	tailscale "tailscale.com/client/tailscale/v2"
)

var deviceIDPattern = regexp.MustCompile(`^n[A-Za-z0-9_-]{10,50}$`)

type Handlers struct {
	tailscaleService *services.TailscaleService
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

func sanitizeError(err error) string {
	// Don't expose internal error details in production
	// This prevents leaking sensitive information about the system
	errStr := err.Error()
	if len(errStr) > 200 {
		return "An error occurred while processing your request"
	}
	return "An error occurred while processing your request"
}

func NewHandlers(tailscaleService *services.TailscaleService) *Handlers {
	return &Handlers{
		tailscaleService: tailscaleService,
	}
}

func (h *Handlers) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"service":   "tsflow-backend",
	})
}

func (h *Handlers) GetDevices(c *gin.Context) {
	devices, err := h.tailscaleService.GetDevices(c.Request.Context())
	if err != nil {
		log.Printf("ERROR GetDevices failed: %v", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to fetch devices",
			Message: sanitizeError(err),
		})
		return
	}

	log.Printf("SUCCESS GetDevices: returned devices successfully")
	c.JSON(http.StatusOK, devices)
}

func (h *Handlers) GetServicesAndRecords(c *gin.Context) {
	// Fetch VIP services
	vipServices, servicesErr := h.tailscaleService.GetVIPServices(c.Request.Context())
	if servicesErr != nil {
		log.Printf("WARNING GetVIPServices failed: %v", servicesErr)
		vipServices = make(map[string]services.VIPServiceInfo)
	}

	// Fetch static records
	staticRecords, recordsErr := h.tailscaleService.GetStaticRecords(c.Request.Context())
	if recordsErr != nil {
		log.Printf("WARNING GetStaticRecords failed: %v", recordsErr)
		staticRecords = make(map[string]services.StaticRecordInfo)
	}
	
	response := gin.H{
		"services": vipServices,
		"records":  staticRecords,
	}
	
	log.Printf("SUCCESS GetServicesAndRecords: returned %d services and %d records", len(vipServices), len(staticRecords))
	c.JSON(http.StatusOK, response)
}

func (h *Handlers) GetNetworkLogs(c *gin.Context) {
	start := c.Query("start")
	end := c.Query("end")

	if start == "" || end == "" {
		now := time.Now()
		start = now.Add(-5 * time.Minute).Format(time.RFC3339)
		end = now.Format(time.RFC3339)
	}

	st, err := time.Parse(time.RFC3339, start)
	if err != nil {
		log.Printf("ERROR GetNetworkLogs: invalid start time %s: %v", start, err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "bad start time",
			"message": err.Error(),
		})
		return
	}

	et, err := time.Parse(time.RFC3339, end)
	if err != nil {
		log.Printf("ERROR GetNetworkLogs: invalid end time %s: %v", end, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad end time", "message": err.Error()})
		return
	}

	if et.Before(st) {
		log.Printf("ERROR GetNetworkLogs: end time before start time: %s < %s", end, start)
		c.JSON(http.StatusBadRequest, gin.H{"error": "end time before start time"})
		return
	}

	now := time.Now()
	if st.After(now) {
		log.Printf("ERROR GetNetworkLogs: future start time not allowed: %s", start)
		c.JSON(http.StatusBadRequest, gin.H{"error": "future start time not allowed"})
		return
	}

	duration := et.Sub(st)
	// Use chunking for queries longer than 7 days to prevent response size issues
	if duration > 7*24*time.Hour {
		// Use smaller chunks and fewer parallel requests for 30+ day queries
		chunkSize := 24 * time.Hour // 1-day chunks to prevent timeouts
		maxParallel := 2            // Reduce parallel requests to prevent memory issues
		chunks, err := h.tailscaleService.GetNetworkLogsChunkedParallel(c.Request.Context(), start, end, chunkSize, maxParallel)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "Failed to fetch network logs",
				"message": err.Error(),
				"hint":    "Try selecting a smaller time range",
			})
			return
		}

		var allLogs []interface{}
		maxLogs := 10000 // Limit total logs to prevent memory issues
		
		for _, chunk := range chunks {
			// Check if we've hit the limit before processing more chunks
			if len(allLogs) >= maxLogs {
				break
			}

			if logsArray, ok := chunk.([]interface{}); ok {
				if len(allLogs)+len(logsArray) > maxLogs {
					// Truncate if we're approaching the limit
					remaining := maxLogs - len(allLogs)
					if remaining > 0 {
						allLogs = append(allLogs, logsArray[:remaining]...)
					}
					break
				}
				allLogs = append(allLogs, logsArray...)
			} else if logsMap, ok := chunk.(map[string]interface{}); ok {
				if logs, exists := logsMap["logs"]; exists {
					if logsArray, ok := logs.([]interface{}); ok {
						if len(allLogs)+len(logsArray) > maxLogs {
							// Truncate if we're approaching the limit
							remaining := maxLogs - len(allLogs)
							if remaining > 0 {
								allLogs = append(allLogs, logsArray[:remaining]...)
							}
							break
						}
						allLogs = append(allLogs, logsArray...)
					} else if logsArray, ok := logs.([]tailscale.NetworkFlowLog); ok {
						// Convert []NetworkFlowLog to []interface{} with bounds checking
						for _, log := range logsArray {
							if len(allLogs) >= maxLogs {
								break
							}
							allLogs = append(allLogs, log)
						}
					} else {
						log.Printf("Warning: unexpected logs type in chunk map: %T", logs)
					}
				} else {
					log.Printf("Warning: chunk map missing 'logs' field")
				}
			} else {
				log.Printf("Warning: unexpected chunk type: %T", chunk)
			}
		}
		
		// allLogs is already limited to maxLogs (10000), so no additional sampling needed
		finalLogs := allLogs
		
		c.JSON(http.StatusOK, gin.H{
			"logs": finalLogs,
			"metadata": gin.H{
				"chunked":     true,
				"chunks":      len(chunks),
				"duration":    duration.String(),
				"totalLogs":   len(allLogs),
				"sampled":     len(finalLogs) < len(allLogs),
				"sampleRate":  func() float64 {
				if len(allLogs) > 0 {
					return float64(len(finalLogs)) / float64(len(allLogs))
				}
				return 1.0
			}(),
			},
		})
		return
	}

	logs, err := h.tailscaleService.GetNetworkLogs(c.Request.Context(), start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to fetch network logs",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, logs)
}

// Helper function to get map keys
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (h *Handlers) GetNetworkMap(c *gin.Context) {
	networkMap, err := h.tailscaleService.GetNetworkMap()
	if err != nil {
		log.Printf("ERROR GetNetworkMap failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to fetch network map",
			"message": err.Error(),
		})
		return
	}

	log.Printf("SUCCESS GetNetworkMap: returned network map")
	c.JSON(http.StatusOK, networkMap)
}

func (h *Handlers) GetDeviceFlows(c *gin.Context) {
	deviceID := c.Param("deviceId")
	if deviceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Device ID is required",
		})
		return
	}

	if !deviceIDPattern.MatchString(deviceID) {
		log.Printf("ERROR GetDeviceFlows: invalid device ID format: %s", deviceID)
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid device ID format",
		})
		return
	}

	flows, err := h.tailscaleService.GetDeviceFlows(deviceID)
	if err != nil {
		log.Printf("ERROR GetDeviceFlows failed for device %s: %v", deviceID, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to fetch device flows",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, flows)
}

func (h *Handlers) GetDNSNameservers(c *gin.Context) {
	nameservers, err := h.tailscaleService.GetDNSNameservers(c.Request.Context())
	if err != nil {
		log.Printf("ERROR GetDNSNameservers failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to fetch DNS nameservers",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, nameservers)
}
