package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/zvdy/pgao/src/analyzer"
	"github.com/zvdy/pgao/src/api"
	"github.com/zvdy/pgao/src/collector"
	"github.com/zvdy/pgao/src/config"
	"github.com/zvdy/pgao/src/db"
)

func main() {
	// Initialize logger
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})
	log.SetLevel(logrus.InfoLevel)

	log.Info("Starting PostgreSQL Analytics Observer...")

	// Load configuration
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Set log level
	level, err := logrus.ParseLevel(cfg.Logging.Level)
	if err == nil {
		log.SetLevel(level)
	}

	log.Infof("Loaded configuration with %d clusters", len(cfg.Clusters))

	// Initialize connection pool
	pool := db.NewConnectionPool(log)
	defer pool.Close()

	// Connect to all configured clusters
	for _, clusterCfg := range cfg.Clusters {
		connConfig := db.ConnectionConfig{
			Host:            clusterCfg.Host,
			Port:            clusterCfg.Port,
			User:            clusterCfg.User,
			Password:        clusterCfg.Password,
			Database:        clusterCfg.Database,
			SSLMode:         clusterCfg.SSLMode,
			MaxConnections:  clusterCfg.MaxConnections,
			MinConnections:  clusterCfg.MinConnections,
			ConnMaxLifetime: clusterCfg.ConnMaxLifetime,
			ConnMaxIdleTime: clusterCfg.ConnMaxIdleTime,
		}

		if err := pool.AddCluster(clusterCfg.ID, connConfig); err != nil {
			log.Errorf("Failed to connect to cluster %s: %v", clusterCfg.ID, err)
			continue
		}

		log.Infof("Connected to cluster: %s (%s:%d)", clusterCfg.ID, clusterCfg.Host, clusterCfg.Port)
	}

	// Initialize analyzers
	queryAnalyzer := analyzer.NewQueryAnalyzer()
	performanceAnalyzer := analyzer.NewPerformanceAnalyzer()

	log.Info("Initialized analyzers")

	// Initialize collectors
	metricsCollector := collector.NewMetricsCollector(pool, log, cfg.Metrics.CollectionInterval)
	clusterCollector := collector.NewClusterCollector(pool, log, cfg.Metrics.CollectionInterval*2)

	log.Info("Initialized collectors")

	// Start collectors in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go metricsCollector.Start(ctx)
	go clusterCollector.Start(ctx)

	log.Info("Started background collectors")

	// Initialize API handler
	handler := api.NewHandler(
		pool,
		queryAnalyzer,
		performanceAnalyzer,
		metricsCollector,
		clusterCollector,
		log,
	)

	// Setup HTTP router
	router := mux.NewRouter()
	handler.RegisterRoutes(router)

	// Setup HTTP server
	serverAddr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	server := &http.Server{
		Addr:         serverAddr,
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	// Start server in goroutine
	go func() {
		log.Infof("Starting HTTP server on %s", serverAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	log.Info("PGAO is ready to accept requests")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Info("Shutting down gracefully...")

	// Cancel context for collectors
	cancel()

	// Shutdown HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Errorf("Server shutdown error: %v", err)
	}

	log.Info("PostgreSQL Analytics Observer stopped")
}
