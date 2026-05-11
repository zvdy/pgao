package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
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
	"github.com/zvdy/pgao/src/leader"
	pgaometrics "github.com/zvdy/pgao/src/metrics"
	webui "github.com/zvdy/pgao/web"
)

// startElectionLoop spawns the leader-election goroutine. While this
// replica holds the lease, it kicks off the data-plane goroutines
// (supervisor + collectors) under a child context that's cancelled on
// demotion. When the parent ctx is cancelled (process shutdown),
// every goroutine is drained via bgWG by the caller.
func startElectionLoop(
	ctx context.Context,
	bgWG *sync.WaitGroup,
	log *logrus.Logger,
	elector leader.Elector,
	pool *db.ConnectionPool,
	mc *collector.MetricsCollector,
	cc *collector.ClusterCollector,
) {
	runBg := func(name string, fn func(context.Context)) {
		bgWG.Add(1)
		go func() {
			defer bgWG.Done()
			fn(ctx)
			log.WithField("goroutine", name).Debug("background goroutine returned")
		}()
	}

	var (
		dpMu     sync.Mutex
		dpCancel context.CancelFunc
	)
	onLeader := func(parent context.Context) {
		dpMu.Lock()
		if dpCancel != nil {
			dpMu.Unlock()
			return
		}
		dpCtx, dpc := context.WithCancel(parent)
		dpCancel = dpc
		dpMu.Unlock()

		log.Info("became leader; starting data plane")
		runBg("supervisor", pool.Supervise)
		runBg("metrics-collector", mc.Start)
		runBg("cluster-collector", cc.Start)
		<-dpCtx.Done()
	}
	onFollower := func(_ context.Context) {
		dpMu.Lock()
		defer dpMu.Unlock()
		if dpCancel != nil {
			log.Warn("lost leadership; stopping data plane")
			dpCancel()
			dpCancel = nil
		}
	}

	bgWG.Add(1)
	go func() {
		defer bgWG.Done()
		if err := elector.Run(ctx, leader.Callbacks{
			OnLeader:   onLeader,
			OnFollower: onFollower,
		}); err != nil {
			log.WithError(err).Error("leader elector exited with error")
		}
	}()
}

// buildElector resolves the leader-election Elector based on config.
// Disabled → AlwaysLeader (every replica runs the data plane, fine for
// replicaCount=1). Enabled → KubernetesElector backed by a coordination.
// k8s.io Lease.
func buildElector(cfg config.LeaderElectionConfig, log *logrus.Logger) (leader.Elector, error) {
	if !cfg.Enabled {
		log.Info("Leader election disabled; this replica owns the data plane")
		return leader.NewAlwaysLeader(), nil
	}

	identity := cfg.Identity
	if identity == "" {
		identity = os.Getenv("POD_NAME")
	}
	if identity == "" {
		host, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("leader election: resolve identity: %w", err)
		}
		identity = host
	}
	namespace := cfg.Namespace
	if namespace == "" {
		namespace = os.Getenv("POD_NAMESPACE")
	}
	name := cfg.Name
	if name == "" {
		name = "pgao-leader"
	}

	log.WithFields(logrus.Fields{
		"identity":  identity,
		"namespace": namespace,
		"lease":     name,
	}).Info("Leader election enabled")

	return leader.NewKubernetesElector(leader.Config{
		Identity:      identity,
		Namespace:     namespace,
		Name:          name,
		LeaseDuration: cfg.LeaseDuration,
		RenewDeadline: cfg.RenewDeadline,
		RetryPeriod:   cfg.RetryPeriod,
	})
}

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Resolve the leader-election Elector. With leader_election disabled
	// every replica runs the data plane (AlwaysLeader); enabled, only
	// the Kubernetes Lease-holder does.
	elector, err := buildElector(cfg.Server.LeaderElection, log)
	if err != nil {
		log.WithError(err).Fatal("failed to initialize leader election")
	}

	// bgWG tracks every long-lived goroutine so SIGTERM can drain them
	// before pool.Close runs.
	var bgWG sync.WaitGroup
	startElectionLoop(ctx, &bgWG, log, elector, pool, metricsCollector, clusterCollector)

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
		WithLeadership(elector).
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

	shutdownTimeout := cfg.Server.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = 30 * time.Second
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	// 1. Stop accepting new HTTP requests and let in-flight ones drain.
	//    Handlers that call collectors use r.Context() so they're bounded
	//    by the per-request timeout middleware (5 s default), not by us.
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.WithError(err).Error("HTTP server shutdown error")
	}

	// 2. Signal the supervisor + collectors to stop their tick loops.
	cancel()

	// 3. Wait for them to actually return so pool.Close (deferred at the
	//    top of main) doesn't yank connections from a query mid-flight.
	//    Cap the wait at the remaining shutdown budget; log if we time out
	//    so an operator on the receiving end of a stuck collector can find
	//    it.
	drained := make(chan struct{})
	go func() {
		bgWG.Wait()
		close(drained)
	}()
	select {
	case <-drained:
		log.Info("background goroutines drained")
	case <-shutdownCtx.Done():
		log.Warn("background goroutines did not drain before shutdown deadline")
	}

	log.Info("PostgreSQL Analytics Observer stopped")
}
