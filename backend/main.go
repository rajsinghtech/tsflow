package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/rajsinghtech/tsflow/backend/frontend"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
	"github.com/rajsinghtech/tsflow/backend/internal/middleware"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
	"github.com/rajsinghtech/tsflow/backend/internal/tsnetserve"
	"net/http"
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
		Output:    os.Stdout,
		SkipPaths: []string{"/health"}, // Skip health checks to reduce noise
	})
}

var (
	// Version is set at build time via ldflags by GoReleaser
	Version = "dev"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Println("TSFlow - Tailscale network flow visualizer")
		fmt.Println("Usage: tsflow [--help]")
		fmt.Println("Configuration is supplied through environment variables; see the README for details.")
		return
	}

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

	// Initialize SQLite database
	dbPath := os.Getenv("TSFLOW_DB_PATH")
	if dbPath == "" {
		// Default to data directory
		dbPath = filepath.Join(".", "data", "tsflow.db")
	}

	// Ensure data directory exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	store, err := database.NewSQLiteStore(dbPath)
	if err != nil {
		log.Fatalf("Failed to create database store: %v", err)
	}

	// Initialize database schema
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Create services
	tailscaleService := services.NewTailscaleService(cfg)

	// Create and start background poller
	pollerConfig := services.DefaultPollerConfig()

	// Allow configuration via environment variables
	if interval := os.Getenv("TSFLOW_POLL_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			pollerConfig.PollInterval = d
		}
	}
	if backfill := os.Getenv("TSFLOW_INITIAL_BACKFILL"); backfill != "" {
		if d, err := time.ParseDuration(backfill); err == nil {
			pollerConfig.InitialBackfill = d
		}
	}
	if retention := os.Getenv("TSFLOW_RETENTION"); retention != "" {
		if d, err := time.ParseDuration(retention); err == nil {
			pollerConfig.Retention = d
		}
	}
	pollerConfig.FlowBackend = cfg.FlowBackend
	if pollerConfig.FlowBackend == "" {
		pollerConfig.FlowBackend = "api"
		if cfg.FlowObjectStoreEndpoint != "" && cfg.FlowObjectStoreAccessKey != "" && cfg.FlowObjectStoreSecretKey != "" {
			pollerConfig.FlowBackend = "s3"
		}
	}
	if pollerConfig.FlowBackend == "s3" && os.Getenv("TSFLOW_RETENTION") == "" {
		pollerConfig.Retention = 0
	}
	lookback, err := time.ParseDuration(cfg.FlowObjectStoreLookback)
	if err != nil {
		lookback = 15 * time.Minute
	}
	pollerConfig.ObjectStore = services.ObjectStoreConfig{
		Bucket:       cfg.FlowObjectStoreBucket,
		Prefix:       cfg.FlowObjectStorePrefix,
		Endpoint:     cfg.FlowObjectStoreEndpoint,
		Region:       cfg.FlowObjectStoreRegion,
		AccessKey:    cfg.FlowObjectStoreAccessKey,
		SecretKey:    cfg.FlowObjectStoreSecretKey,
		UsePathStyle: cfg.FlowObjectStorePathStyle,
		Lookback:     lookback,
		MaxObjects:   cfg.FlowObjectStoreMaxObjects,
	}

	poller := services.NewPoller(tailscaleService, store, pollerConfig)
	pollerCtx, pollerCancel := context.WithCancel(context.Background())
	defer pollerCancel()
	if pollerConfig.FlowBackend == "s3" {
		objectSource, err := services.NewObjectStoreSource(ctx, pollerConfig.ObjectStore)
		if err != nil {
			log.Fatalf("Failed to configure object-store flow backend: %v", err)
		}
		poller.ConfigureObjectStore(objectSource)
	}

	// Start poller in background
	if err := poller.Start(pollerCtx); err != nil {
		log.Printf("Warning: Failed to start poller: %v", err)
	}

	// Create handlers with store and poller
	handlerService := handlers.NewHandlers(tailscaleService, store, poller, Version)

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

	router.HandleMethodNotAllowed = true

	// Add gzip compression middleware
	router.Use(gzip.Gzip(gzip.DefaultCompression))

	corsConfig := cors.DefaultConfig()
	// Configure CORS based on allowed origins. Production deployments must
	// explicitly opt into cross-origin origins.
	if len(cfg.AllowedCORSOrigins) > 0 {
		corsConfig.AllowOrigins = cfg.AllowedCORSOrigins
	} else if strings.EqualFold(cfg.Environment, "production") {
		corsConfig.AllowOriginFunc = func(string) bool { return false }
	} else {
		corsConfig.AllowOriginFunc = func(origin string) bool {
			return true
		}
	}
	corsConfig.AllowCredentials = true
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization"}
	router.Use(cors.New(corsConfig))

	// Add CSP middleware for security
	router.Use(middleware.CSPMiddleware())

	router.GET("/health", handlerService.HealthCheck)

	api := router.Group("/api")
	api.Use(middleware.RateLimitMiddleware(middleware.DefaultRateLimitConfig()))
	{
		api.GET("/health", handlerService.HealthCheck)
		// Existing endpoints (live API queries) - short cache
		liveCache := middleware.CacheMiddleware(middleware.ShortCacheConfig())
		api.GET("/devices", liveCache, handlerService.GetDevices)
		api.GET("/services-records", liveCache, handlerService.GetServicesAndRecords)
		api.GET("/network-logs", liveCache, handlerService.GetNetworkLogs)
		api.GET("/network-map", liveCache, handlerService.GetNetworkMap)
		api.GET("/devices/:deviceId/flows", handlerService.GetDeviceFlows)
		api.GET("/dns/nameservers", liveCache, handlerService.GetDNSNameservers)

		// Stored historical data - longer cache for time-series
		histCache := middleware.CacheMiddleware(middleware.LongCacheConfig())
		api.GET("/flow-logs", handlerService.GetStoredFlowLogs)
		api.GET("/flow-logs/aggregated", histCache, handlerService.GetAggregatedFlowLogs)
		api.GET("/flow-logs/range", handlerService.GetDataRange)
		api.GET("/bandwidth", histCache, handlerService.GetBandwidthAggregated)

		// Stats endpoints - longer cache for aggregated analytics
		statsCache := middleware.CacheMiddleware(middleware.LongCacheConfig())
		stats := api.Group("/stats")
		{
			stats.GET("/overview", statsCache, handlerService.GetStatsOverview)
			stats.GET("/top-talkers", statsCache, handlerService.GetTopTalkers)
			stats.GET("/top-pairs", statsCache, handlerService.GetTopPairs)
			stats.GET("/node/:id", statsCache, handlerService.GetNodeDetailStats)
		}

		// Policy endpoints
		api.GET("/policy", liveCache, handlerService.GetPolicy)
		api.GET("/users", liveCache, handlerService.GetUsers)

		// Status endpoints - no cache
		noCache := middleware.CacheMiddleware(middleware.NoCacheConfig())
		api.GET("/poller/status", noCache, handlerService.GetPollerStatus)
		api.POST("/poller/trigger", handlerService.TriggerPoll)
	}

	// Register embedded frontend (must be after API routes)
	if err := frontend.RegisterFrontend(router); err != nil {
		if errors.Is(err, frontend.ErrFrontendNotIncluded) {
			log.Println("Frontend not embedded in build, skipping frontend registration")
			log.Println("Run `npm run build` in frontend/ then rebuild Go binary to embed frontend")
		} else {
			log.Fatalf("Failed to register frontend: %v", err)
		}
	} else {
		log.Println("Embedded frontend registered successfully")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = cfg.Port
	}

	log.Printf("=== TSFlow Server Starting ===")
	log.Printf("Port: %s", port)
	log.Printf("Tailnet: %s", cfg.TailscaleTailnet)
	log.Printf("API URL: %s", cfg.TailscaleAPIURL)
	log.Printf("Environment: %s", cfg.Environment)
	log.Printf("Database: %s", dbPath)
	log.Printf("Poll Interval: %s", pollerConfig.PollInterval)
	log.Printf("Retention: %s", pollerConfig.Retention)
	log.Printf("Flow Backend: %s", pollerConfig.FlowBackend)
	if pollerConfig.FlowBackend == "s3" {
		log.Printf("Flow Object Store: bucket=%s prefix=%s endpoint=%s maxObjectsPerPoll=%d",
			pollerConfig.ObjectStore.Bucket,
			pollerConfig.ObjectStore.Prefix,
			pollerConfig.ObjectStore.Endpoint,
			pollerConfig.ObjectStore.MaxObjects,
		)
	}

	// Log authentication method being used
	if cfg.TailscaleOAuthClientID != "" && cfg.TailscaleOAuthClientSecret != "" {
		maskedID := cfg.TailscaleOAuthClientID
		if len(maskedID) > 4 {
			maskedID = "****" + maskedID[len(maskedID)-4:]
		}
		log.Printf("Authentication: OAuth Client Credentials (Client ID: %s)", maskedID)
	} else {
		log.Printf("Authentication: API Key")
	}

	if cfg.TsnetServe {
		log.Printf("Mode: tsnet (embedded Tailscale node)")
		log.Printf("Hostname: %s", cfg.TsnetHostname)
		if len(cfg.TsnetTags) > 0 {
			log.Printf("Tags: %v", cfg.TsnetTags)
		}
		if cfg.TsnetClientID != "" {
			log.Printf("tsnet auth: workload identity federation (TS_CLIENT_ID)")
		} else {
			log.Printf("tsnet auth: OAuth client secret")
		}
		log.Printf("Funnel: %v", cfg.TsnetFunnel)
	} else {
		log.Printf("Server ready at http://0.0.0.0:%s", port)
	}
	log.Printf("=== Server Started Successfully ===")

	// Graceful shutdown handling
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	if cfg.TsnetServe {
		tsnetCtx, tsnetCancel := context.WithTimeout(context.Background(), 60*time.Second)
		tsnetSrv, err := tsnetserve.New(tsnetCtx, cfg)
		tsnetCancel()
		if err != nil {
			log.Fatalf("Failed to start tsnet server: %v", err)
		}

		tlsSrv := &http.Server{Handler: router}
		httpSrv := &http.Server{Handler: router}
		go func() {
			if err := tlsSrv.Serve(tsnetSrv.TLSListener()); err != nil && err != http.ErrServerClosed {
				log.Fatalf("FATAL tsnet TLS serve failed: %v", err)
			}
		}()
		go func() {
			if err := httpSrv.Serve(tsnetSrv.HTTPListener()); err != nil && err != http.ErrServerClosed {
				log.Fatalf("FATAL tsnet HTTP serve failed: %v", err)
			}
		}()

		<-quit
		log.Println("Shutting down server...")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		tlsSrv.Shutdown(shutdownCtx)
		httpSrv.Shutdown(shutdownCtx)
		tsnetSrv.Close()
	} else {
		httpSrv := &http.Server{Addr: "0.0.0.0:" + port, Handler: router}
		go func() {
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("FATAL Failed to start server: %v", err)
			}
		}()

		<-quit
		log.Println("Shutting down server...")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("Error shutting down HTTP server: %v", err)
		}
		shutdownCancel()
	}

	// Stop the poller gracefully
	pollerCancel()
	poller.Stop()

	// Close database connection
	if err := store.Close(); err != nil {
		log.Printf("Error closing database: %v", err)
	}

	log.Println("Server stopped")
}
