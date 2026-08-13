package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

func (h *Handlers) GetDevices(c *gin.Context) {
	if h.poller != nil {
		cachedDevices := h.poller.GetDeviceCache().Devices()
		if len(cachedDevices) > 0 {
			c.JSON(http.StatusOK, gin.H{"devices": cachedDevices})
			return
		}
	}

	devices, err := h.tailscaleService.GetDevicesWithContext(c.Request.Context())
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		log.Printf("ERROR GetDevices: %v", err)
		c.JSON(http.StatusOK, gin.H{"devices": []services.Device{}})
		return
	}

	c.JSON(http.StatusOK, devices)
}

func (h *Handlers) GetServicesAndRecords(c *gin.Context) {
	ctx := c.Request.Context()
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			c.Status(499)
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			c.Status(http.StatusGatewayTimeout)
			return
		}
	}

	// Fetch VIP services
	vipServices, servicesErr := h.tailscaleService.GetVIPServices(ctx)
	if servicesErr != nil {
		if errors.Is(servicesErr, context.Canceled) {
			c.Status(499)
			return
		}
		if errors.Is(servicesErr, context.DeadlineExceeded) {
			c.Status(http.StatusGatewayTimeout)
			return
		}
		log.Printf("WARNING GetVIPServices failed: %v", servicesErr)
		vipServices = make(map[string]services.VIPServiceInfo)
	}

	// Fetch static records
	staticRecords, recordsErr := h.tailscaleService.GetStaticRecords(ctx)
	if recordsErr != nil {
		if errors.Is(recordsErr, context.Canceled) {
			c.Status(499)
			return
		}
		if errors.Is(recordsErr, context.DeadlineExceeded) {
			c.Status(http.StatusGatewayTimeout)
			return
		}
		log.Printf("WARNING GetStaticRecords failed: %v", recordsErr)
		staticRecords = make(map[string]services.StaticRecordInfo)
	}

	response := gin.H{
		"services": vipServices,
		"records":  staticRecords,
	}

	c.JSON(http.StatusOK, response)
}

func (h *Handlers) GetDNSNameservers(c *gin.Context) {
	nameservers, err := h.tailscaleService.GetDNSNameserversWithContext(c.Request.Context())
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		log.Printf("ERROR GetDNSNameservers: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to fetch DNS nameservers",
		})
		return
	}

	c.JSON(http.StatusOK, nameservers)
}
