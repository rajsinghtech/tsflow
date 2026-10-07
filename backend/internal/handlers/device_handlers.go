package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

func (h *Handlers) GetDevices(c *gin.Context) {
	tn, ok := h.bindTailnet(c)
	if !ok {
		return
	}
	if tn.poller != nil {
		cachedDevices := tn.poller.GetDeviceCache().Devices()
		if len(cachedDevices) > 0 {
			c.JSON(http.StatusOK, gin.H{"devices": cachedDevices})
			return
		}
	}

	devices, err := tn.service.GetDevicesWithContext(c.Request.Context())
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
	tn, ok := h.bindTailnet(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if err := ctx.Err(); err != nil && writeContextError(c, err) {
		return
	}

	// Fetch VIP services
	vipServices, servicesErr := tn.service.GetVIPServices(ctx)
	if servicesErr != nil {
		if writeContextError(c, servicesErr) {
			return
		}
		log.Printf("WARNING GetVIPServices failed: %v", servicesErr)
		vipServices = make(map[string]services.VIPServiceInfo)
	}

	// Fetch static records
	staticRecords, recordsErr := tn.service.GetStaticRecords(ctx)
	if recordsErr != nil {
		if writeContextError(c, recordsErr) {
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
	tn, ok := h.bindTailnet(c)
	if !ok {
		return
	}
	nameservers, err := tn.service.GetDNSNameserversWithContext(c.Request.Context())
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		log.Printf("ERROR GetDNSNameservers: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, nameservers)
}
