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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/sirupsen/logrus"
	"github.com/zvdy/pgao/src/analyzer"
	"github.com/zvdy/pgao/src/api"
	"github.com/zvdy/pgao/src/collector"
	"github.com/zvdy/pgao/src/config"
	"github.com/zvdy/pgao/src/db"
	pgaometrics "github.com/zvdy/pgao/src/metrics"
	webui "github.com/zvdy/pgao/web"
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
	pool.SetSupervisorConfig(db.SupervisorConfig{
		HealthInterval: cfg.Metrics.HealthCheckInterval,
		ProbeTimeout:   cfg.Metrics.QueryTimeout,
	})
	defer pool.Close()

	// Register every configured cluster. AddCluster no longer blocks on the
	// initial ping — it creates the pgxpool (which connects lazily) and
	// marks the cluster as connecting; the supervisor takes it from there.
	for _, clusterCfg := range cfg.Clusters {
		connConfig := db.ConnectionConfig{
			Host:             clusterCfg.Host,
			Port:             clusterCfg.Port,
			User:             clusterCfg.User,
			Password:         clusterCfg.Password,
			Database:         clusterCfg.Database,
			SSLMode:          clusterCfg.SSLMode,
			MaxConnections:   clusterCfg.MaxConnections,
			MinConnections:   clusterCfg.MinConnections,
			ConnMaxLifetime:  clusterCfg.ConnMaxLifetime,
			ConnMaxIdleTime:  clusterCfg.ConnMaxIdleTime,
			StatementTimeout: clusterCfg.StatementTimeout,
			SSLRootCert:      clusterCfg.SSLRootCert,
			SSLCert:          clusterCfg.SSLCert,
			SSLKey:           clusterCfg.SSLKey,
			SSLServerName:    clusterCfg.SSLServerName,
		}
		if err := pool.AddCluster(clusterCfg.ID, connConfig); err != nil {
			log.WithError(err).WithField("cluster_id", clusterCfg.ID).Error("register cluster failed")
			continue
		}
		log.WithFields(logrus.Fields{
			"cluster_id": clusterCfg.ID,
			"host":       clusterCfg.Host,
			"port":       clusterCfg.Port,
		}).Info("cluster registered")
	}

	// Initialize analyzers
	queryAnalyzer := analyzer.NewQueryAnalyzer()
	performanceAnalyzer := analyzer.NewPerformanceAnalyzer()

	log.Info("Initialized analyzers")

	// Prometheus registry: Go runtime + process collectors + pgao exporter +
	// pgao runtime instrumentation. Built before the collectors so they can
	// publish per-round metrics from their first tick.
	promReg := prometheus.NewRegistry()
	promReg.MustRegister(collectors.NewGoCollector())
	promReg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	collectionInstr := pgaometrics.NewCollectionInstrumentation(promReg)
	httpInstr := pgaometrics.NewHTTPInstrumentation(promReg)

	// Initialize collectors with a per-cluster query timeout so one slow
	// Postgres can't stall the whole collection cycle. Wire the
	// instrumentation so each round's duration + error count lands in
	// /metrics.
	metricsCollector := collector.NewMetricsCollector(pool, log, cfg.Metrics.CollectionInterval).
		WithQueryTimeout(cfg.Metrics.QueryTimeout).
		WithObserver(collectionInstr)
	clusterCollector := collector.NewClusterCollector(pool, log, cfg.Metrics.CollectionInterval*2).
		WithQueryTimeout(cfg.Metrics.QueryTimeout).
		WithObserver(collectionInstr)

	log.Info("Initialized collectors")

	// Start collectors + connection supervisor in the background.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pool.Supervise(ctx)
	go metricsCollector.Start(ctx)
	go clusterCollector.Start(ctx)

	log.Info("Started background collectors + supervisor")

	if cfg.Metrics.EnablePrometheus {
		promReg.MustRegister(pgaometrics.NewExporter(metricsCollector).WithStateSource(pool))
		log.Info("Prometheus exporter registered")
	}

	// Initialize API handler
	handler := api.NewHandler(
		pool,
		queryAnalyzer,
		performanceAnalyzer,
		metricsCollector,
		clusterCollector,
		log,
	).WithPromRegistry(promReg).
		WithHTTPInstrumentation(httpInstr).
		WithSecurity(api.SecurityOptions{
			APIKey:         cfg.Server.Auth.APIKey,
			RequestTimeout: cfg.Server.RequestTimeout,
			MaxBodyBytes:   cfg.Server.MaxBodyBytes,
			RateLimitRPS:   cfg.Server.RateLimitRPS,
			RateLimitBurst: cfg.Server.RateLimitBurst,
		})

	if cfg.Server.UIEnabledOrDefault() {
		uiHandler, err := webui.Handler()
		if err != nil {
			log.WithError(err).Warn("could not initialize embedded UI; serving API only")
		} else {
			handler = handler.WithUI(uiHandler)
			log.Info("Embedded UI enabled at /")
		}
	} else {
		log.Info("Embedded UI disabled by config (server.ui_enabled=false)")
	}

	if cfg.Server.Auth.APIKey != "" {
		log.Info("API key authentication enabled for /api/v1/*")
	} else {
		log.Warn("API key authentication is disabled; /api/v1/* is open")
	}

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
