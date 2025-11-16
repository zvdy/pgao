package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration
type Config struct {
	Server   ServerConfig    `yaml:"server"`
	Clusters []ClusterConfig `yaml:"clusters"`
	Logging  LoggingConfig   `yaml:"logging"`
	Metrics  MetricsConfig   `yaml:"metrics"`
	AWS      AWSConfig       `yaml:"aws"`
}

// ServerConfig represents HTTP server configuration
type ServerConfig struct {
	Host         string        `yaml:"host"`
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	IdleTimeout  time.Duration `yaml:"idle_timeout"`
}

// ClusterConfig represents a PostgreSQL cluster configuration
type ClusterConfig struct {
	ID              string            `yaml:"id"`
	Name            string            `yaml:"name"`
	Host            string            `yaml:"host"`
	Port            int               `yaml:"port"`
	User            string            `yaml:"user"`
	Password        string            `yaml:"password"`
	Database        string            `yaml:"database"`
	SSLMode         string            `yaml:"ssl_mode"`
	MaxConnections  int               `yaml:"max_connections"`
	MinConnections  int               `yaml:"min_connections"`
	ConnMaxLifetime time.Duration     `yaml:"conn_max_lifetime"`
	ConnMaxIdleTime time.Duration     `yaml:"conn_max_idle_time"`
	Region          string            `yaml:"region"`
	Environment     string            `yaml:"environment"`
	Tags            map[string]string `yaml:"tags"`
}

// LoggingConfig represents logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"` // json or text
	Output string `yaml:"output"` // stdout, stderr, or file path
}

// MetricsConfig represents metrics collection configuration
type MetricsConfig struct {
	CollectionInterval time.Duration `yaml:"collection_interval"`
	RetentionDays      int           `yaml:"retention_days"`
	EnablePrometheus   bool          `yaml:"enable_prometheus"`
	PrometheusPort     int           `yaml:"prometheus_port"`
}

// AWSConfig represents AWS configuration
type AWSConfig struct {
	Region          string   `yaml:"region"`
	AccessKeyID     string   `yaml:"access_key_id"`
	SecretAccessKey string   `yaml:"secret_access_key"`
	SessionToken    string   `yaml:"session_token"`
	AssumeRoleARN   string   `yaml:"assume_role_arn"`
	Accounts        []string `yaml:"accounts"`
}

// LoadConfig loads configuration from file or environment variables
func LoadConfig(configPath string) (*Config, error) {
	cfg := defaultConfig()

	// Load from file if provided
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}

		// Expand environment variables in the config file
		expandedData := expandEnvVars(string(data))

		if err := yaml.Unmarshal([]byte(expandedData), cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	// Override with environment variables
	cfg.overrideFromEnv()

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// expandEnvVars expands ${VAR} or $VAR patterns in the input string
func expandEnvVars(input string) string {
	re := regexp.MustCompile(`\$\{([^}]+)\}|\$([A-Z_][A-Z0-9_]*)`)
	return re.ReplaceAllStringFunc(input, func(match string) string {
		// Extract variable name
		var varName string
		if match[1] == '{' {
			// ${VAR} format
			varName = match[2 : len(match)-1]
		} else {
			// $VAR format
			varName = match[1:]
		}
		
		// Get value from environment
		if val := os.Getenv(varName); val != "" {
			return val
		}
		// Return original if not found
		return match
	})
}

// defaultConfig returns default configuration
func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host:         "0.0.0.0",
			Port:         8080,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
		Clusters: []ClusterConfig{},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		},
		Metrics: MetricsConfig{
			CollectionInterval: 60 * time.Second,
			RetentionDays:      30,
			EnablePrometheus:   true,
			PrometheusPort:     9090,
		},
		AWS: AWSConfig{
			Region:   "us-east-1",
			Accounts: []string{},
		},
	}
}

// overrideFromEnv overrides configuration with environment variables
func (c *Config) overrideFromEnv() {
	// Server configuration
	if host := os.Getenv("SERVER_HOST"); host != "" {
		c.Server.Host = host
	}
	if port := os.Getenv("SERVER_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			c.Server.Port = p
		}
	}

	// Logging configuration
	if level := os.Getenv("LOG_LEVEL"); level != "" {
		c.Logging.Level = level
	}
	if format := os.Getenv("LOG_FORMAT"); format != "" {
		c.Logging.Format = format
	}

	// AWS configuration
	if region := os.Getenv("AWS_REGION"); region != "" {
		c.AWS.Region = region
	}
	if accessKey := os.Getenv("AWS_ACCESS_KEY_ID"); accessKey != "" {
		c.AWS.AccessKeyID = accessKey
	}
	if secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY"); secretKey != "" {
		c.AWS.SecretAccessKey = secretKey
	}
	if sessionToken := os.Getenv("AWS_SESSION_TOKEN"); sessionToken != "" {
		c.AWS.SessionToken = sessionToken
	}

	// Metrics configuration
	if interval := os.Getenv("METRICS_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			c.Metrics.CollectionInterval = d
		}
	}

	// Single cluster configuration from environment
	if dbHost := os.Getenv("DATABASE_HOST"); dbHost != "" {
		cluster := ClusterConfig{
			ID:       getEnv("DATABASE_ID", "default"),
			Name:     getEnv("DATABASE_NAME", "default"),
			Host:     dbHost,
			Port:     getEnvInt("DATABASE_PORT", 5432),
			User:     getEnv("DATABASE_USER", "postgres"),
			Password: getEnv("DATABASE_PASSWORD", ""),
			Database: getEnv("DATABASE_NAME", "postgres"),
			SSLMode:  getEnv("DATABASE_SSLMODE", "disable"),
		}
		c.Clusters = append(c.Clusters, cluster)
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Validate server configuration
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	// Validate logging configuration
	validLevels := map[string]bool{
		"debug": true, "info": true, "warn": true, "error": true, "fatal": true,
	}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("invalid log level: %s", c.Logging.Level)
	}

	// Validate clusters
	if len(c.Clusters) == 0 {
		return fmt.Errorf("at least one cluster must be configured")
	}

	for i, cluster := range c.Clusters {
		if cluster.ID == "" {
			return fmt.Errorf("cluster %d: ID is required", i)
		}
		if cluster.Host == "" {
			return fmt.Errorf("cluster %s: host is required", cluster.ID)
		}
		if cluster.Port < 1 || cluster.Port > 65535 {
			return fmt.Errorf("cluster %s: invalid port: %d", cluster.ID, cluster.Port)
		}
		if cluster.User == "" {
			return fmt.Errorf("cluster %s: user is required", cluster.ID)
		}
		if cluster.Database == "" {
			return fmt.Errorf("cluster %s: database is required", cluster.ID)
		}
	}

	return nil
}

// GetCluster returns configuration for a specific cluster
func (c *Config) GetCluster(clusterID string) (*ClusterConfig, error) {
	for _, cluster := range c.Clusters {
		if cluster.ID == clusterID {
			return &cluster, nil
		}
	}
	return nil, fmt.Errorf("cluster %s not found in configuration", clusterID)
}

// getEnv retrieves an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt retrieves an integer environment variable or returns a default value
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
