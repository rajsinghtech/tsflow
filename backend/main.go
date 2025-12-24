package main

import (
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/rajsinghtech/tsflow"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

// customLoggingMiddleware provides structured request logging for production
func customLoggingMiddleware() gin.HandlerFunc {
	return gin.LoggerWithConfig(gin.LoggerConfig{
		Formatter: func(param gin.LogFormatterParams) string {
			return fmt.Sprintf("[%s] %s %s %d %s %s\n",
				param.TimeStamp.Format("2006/01/02 - 15:04:05"),
				param.Method,
				param.Path,
				param.StatusCode,
				param.Latency,
				param.ClientIP,
			)
		},
		Output: os.Stdout,
		SkipPaths: []string{"/health"}, // Skip health checks to reduce noise
	})
}

func main() {
	// Configure logging to stdout for container visibility
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if err := godotenv.Load("../.env"); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	tailscaleService := services.NewTailscaleService(cfg)
	handlerService := handlers.NewHandlers(tailscaleService)

	// Configure Gin logging
	var router *gin.Engine
	if cfg.Environment == "production" {
		// In production, use custom logging middleware instead of completely disabling logs
		gin.SetMode(gin.ReleaseMode)
		gin.DefaultWriter = os.Stdout
		gin.DefaultErrorWriter = os.Stderr
		router = gin.New()
		router.Use(gin.Recovery())
		router.Use(customLoggingMiddleware())
	} else {
		router = gin.Default()
	}

	// Add gzip compression middleware
	router.Use(gzip.Gzip(gzip.DefaultCompression))

	corsConfig := cors.DefaultConfig()
	if cfg.Environment == "production" {
		corsConfig.AllowOrigins = []string{"https://tsflow.production.com"}
		corsConfig.AllowAllOrigins = false
	} else {
		corsConfig.AllowOrigins = []string{"http://localhost:3000", "http://localhost:5173"}
	}
	corsConfig.AllowCredentials = true
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization"}
	router.Use(cors.New(corsConfig))

	router.GET("/health", handlerService.HealthCheck)

	api := router.Group("/api")
	{
		api.GET("/devices", handlerService.GetDevices)
		api.GET("/services-records", handlerService.GetServicesAndRecords)
		api.GET("/network-logs", handlerService.GetNetworkLogs)
		api.GET("/network-map", handlerService.GetNetworkMap)
		api.GET("/devices/:deviceId/flows", handlerService.GetDeviceFlows)
		api.GET("/dns/nameservers", handlerService.GetDNSNameservers)
	}

	// Serve embedded frontend or local files in development
	var frontendFS http.FileSystem
	if cfg.Environment == "production" {
		// Use embedded files in production
		subFS, err := fs.Sub(tsflow.FrontendFS, "frontend/dist")
		if err != nil {
			log.Fatalf("Failed to create sub filesystem: %v", err)
		}
		frontendFS = http.FS(subFS)
		log.Println("Serving embedded frontend files (production)")
	} else {
		// Use local files in development
		frontendFS = http.Dir("./frontend/dist")
		log.Println("Serving local frontend files (development): ./frontend/dist")
	}

	// Serve static files and SPA fallback
	router.NoRoute(func(c *gin.Context) {
		path := strings.TrimPrefix(c.Request.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Try to open the requested file
		file, err := frontendFS.Open(path)
		if err == nil {
			defer file.Close()
			// Get file info
			stat, err := file.Stat()
			if err != nil {
				c.String(http.StatusInternalServerError, "Failed to stat file")
				return
			}

			// If it's a directory, fall through to serve index.html
			if !stat.IsDir() {
				// Read and serve the file
				content, err := io.ReadAll(file)
				if err != nil {
					c.String(http.StatusInternalServerError, "Failed to read file")
					return
				}

				// Set content type based on file extension
				contentType := "text/html"
				if strings.HasSuffix(path, ".js") {
					contentType = "application/javascript"
				} else if strings.HasSuffix(path, ".css") {
					contentType = "text/css"
				} else if strings.HasSuffix(path, ".svg") {
					contentType = "image/svg+xml"
				} else if strings.HasSuffix(path, ".png") {
					contentType = "image/png"
				} else if strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".jpeg") {
					contentType = "image/jpeg"
				}

				c.Data(http.StatusOK, contentType, content)
				return
			}
		}

		// If file doesn't exist or is a directory, serve index.html for SPA routing
		indexFile, err := frontendFS.Open("index.html")
		if err != nil {
			c.String(http.StatusNotFound, "404 page not found")
			return
		}
		defer indexFile.Close()

		content, err := io.ReadAll(indexFile)
		if err != nil {
			c.String(http.StatusInternalServerError, "Failed to read index.html")
			return
		}

		c.Data(http.StatusOK, "text/html", content)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = cfg.Port
	}

	log.Printf("=== TSFlow Server Starting ===")
	log.Printf("Port: %s", port)
	log.Printf("Tailnet: %s", cfg.TailscaleTailnet)
	log.Printf("API URL: %s", cfg.TailscaleAPIURL)
	log.Printf("Environment: %s", cfg.Environment)
	
	// Log authentication method being used
	if cfg.TailscaleOAuthClientID != "" && cfg.TailscaleOAuthClientSecret != "" {
		log.Printf("Authentication: OAuth Client Credentials")
	} else {
		log.Printf("Authentication: API Key")
	}
	
	log.Printf("Server ready at http://0.0.0.0:%s", port)
	log.Printf("=== Server Started Successfully ===")

	if err := router.Run("0.0.0.0:" + port); err != nil {
		log.Fatalf("FATAL Failed to start server: %v", err)
	}
}
