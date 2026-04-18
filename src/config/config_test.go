package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig_HasSaneDefaults(t *testing.T) {
	cfg := defaultConfig()

	if cfg.Server.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Server.ReadTimeout != 15*time.Second {
		t.Errorf("expected ReadTimeout 15s, got %s", cfg.Server.ReadTimeout)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("expected log level info, got %s", cfg.Logging.Level)
	}
	if cfg.Metrics.CollectionInterval != 60*time.Second {
		t.Errorf("expected CollectionInterval 60s, got %s", cfg.Metrics.CollectionInterval)
	}
}

func TestExpandEnvVars(t *testing.T) {
	t.Setenv("PGAO_TEST_PASSWORD", "s3cret")
	t.Setenv("PGAO_TEST_HOST", "db.local")

	cases := []struct {
		in, want string
	}{
		{"host: ${PGAO_TEST_HOST}", "host: db.local"},
		{"pw: $PGAO_TEST_PASSWORD", "pw: s3cret"},
		{"missing: ${PGAO_TEST_NOT_SET}", "missing: ${PGAO_TEST_NOT_SET}"},
		{"literal: nothing", "literal: nothing"},
	}
	for _, c := range cases {
		got := expandEnvVars(c.in)
		if got != c.want {
			t.Errorf("expandEnvVars(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidate_RejectsBadConfig(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{
			name:    "no clusters",
			mutate:  func(c *Config) { c.Clusters = nil },
			wantErr: true,
		},
		{
			name: "invalid port",
			mutate: func(c *Config) {
				c.Clusters = []ClusterConfig{validCluster()}
				c.Server.Port = 0
			},
			wantErr: true,
		},
		{
			name: "invalid log level",
			mutate: func(c *Config) {
				c.Clusters = []ClusterConfig{validCluster()}
				c.Logging.Level = "bogus"
			},
			wantErr: true,
		},
		{
			name: "cluster missing host",
			mutate: func(c *Config) {
				cluster := validCluster()
				cluster.Host = ""
				c.Clusters = []ClusterConfig{cluster}
			},
			wantErr: true,
		},
		{
			name: "cluster missing id",
			mutate: func(c *Config) {
				cluster := validCluster()
				cluster.ID = ""
				c.Clusters = []ClusterConfig{cluster}
			},
			wantErr: true,
		},
		{
			name: "cluster invalid port",
			mutate: func(c *Config) {
				cluster := validCluster()
				cluster.Port = 99999
				c.Clusters = []ClusterConfig{cluster}
			},
			wantErr: true,
		},
		{
			name:    "valid config",
			mutate:  func(c *Config) { c.Clusters = []ClusterConfig{validCluster()} },
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfig_FromFileWithEnvExpansion(t *testing.T) {
	t.Setenv("PGAO_TEST_DB_PASSWORD", "hunter2")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
server:
  host: "127.0.0.1"
  port: 9090
  read_timeout: 10s
  write_timeout: 10s
  idle_timeout: 30s
clusters:
  - id: "c1"
    host: "db.local"
    port: 5432
    user: "postgres"
    password: "${PGAO_TEST_DB_PASSWORD}"
    database: "postgres"
    ssl_mode: "disable"
logging:
  level: "debug"
  format: "json"
  output: "stdout"
metrics:
  collection_interval: 30s
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if len(cfg.Clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(cfg.Clusters))
	}
	if cfg.Clusters[0].Password != "hunter2" {
		t.Errorf("expected env-expanded password, got %q", cfg.Clusters[0].Password)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("expected debug log level, got %s", cfg.Logging.Level)
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	if _, err := LoadConfig("/nonexistent/path.yaml"); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestGetCluster(t *testing.T) {
	cfg := &Config{Clusters: []ClusterConfig{validCluster()}}

	c, err := cfg.GetCluster("c1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.ID != "c1" {
		t.Errorf("expected id c1, got %s", c.ID)
	}

	if _, err := cfg.GetCluster("missing"); err == nil {
		t.Fatal("expected error for missing cluster, got nil")
	}
}

func TestOverrideFromEnv(t *testing.T) {
	t.Setenv("SERVER_PORT", "7777")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("DATABASE_HOST", "override-host")
	t.Setenv("DATABASE_USER", "override-user")
	t.Setenv("DATABASE_PASSWORD", "override-pw")

	cfg := defaultConfig()
	cfg.overrideFromEnv()

	if cfg.Server.Port != 7777 {
		t.Errorf("env override failed: port=%d", cfg.Server.Port)
	}
	if cfg.Logging.Level != "warn" {
		t.Errorf("env override failed: log level=%s", cfg.Logging.Level)
	}
	if len(cfg.Clusters) != 1 {
		t.Fatalf("expected single cluster from env, got %d", len(cfg.Clusters))
	}
	if cfg.Clusters[0].Host != "override-host" {
		t.Errorf("expected DATABASE_HOST to be used, got %s", cfg.Clusters[0].Host)
	}
}

func validCluster() ClusterConfig {
	return ClusterConfig{
		ID:       "c1",
		Host:     "127.0.0.1",
		Port:     5432,
		User:     "postgres",
		Database: "postgres",
		SSLMode:  "disable",
	}
}
