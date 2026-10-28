package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/orrn/spool/internal/scale"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Printers PrintersConfig `yaml:"printers"`
	Queue    QueueConfig    `yaml:"queue"`
	Logging  LoggingConfig  `yaml:"logging"`
	Scale    scale.Config   `yaml:"scale"`
	ORRN     ORRNConfig     `yaml:"orrn"`
}

// ORRNConfig links this spool to its ORRN edge-node deployment. The install
// script writes these as ORRN_SPOOL_* environment variables.
type ORRNConfig struct {
	// InstanceID is the deployment's instance id (ORRN_SPOOL_INSTANCE_ID).
	InstanceID string `yaml:"instance_id"`
	// Subdomain is the node subdomain (ORRN_SPOOL_SUBDOMAIN).
	Subdomain string `yaml:"subdomain"`
	// ServerURL is ORRN's API origin (ORRN_SPOOL_SERVER_URL).
	ServerURL string `yaml:"server_url"`
	// SharedSecret signs and verifies ORRN machine requests
	// (ORRN_SPOOL_SHARED_SECRET). Empty turns ORRN HMAC auth off.
	SharedSecret string `yaml:"shared_secret"`
	// HeartbeatInterval between POSTs to ORRN; 0 means 60s.
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
}

type ServerConfig struct {
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

type DatabaseConfig struct {
	Path        string `yaml:"path"`
	ArchivePath string `yaml:"archive_path"`
	ArchiveDays int    `yaml:"archive_days"`
}

type PrintersConfig struct {
	HealthCheckInterval time.Duration `yaml:"health_check_interval"`
	ConnectionTimeout   time.Duration `yaml:"connection_timeout"`
	StatusPollInterval  time.Duration `yaml:"status_poll_interval"`
}

type QueueConfig struct {
	MaxRetries  int           `yaml:"max_retries"`
	RetryDelay  time.Duration `yaml:"retry_delay"`
	WorkerCount int           `yaml:"worker_count"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Database: DatabaseConfig{
			Path:        "./data/spool.db",
			ArchivePath: "./data/archives",
			ArchiveDays: 30,
		},
		Printers: PrintersConfig{
			HealthCheckInterval: 30 * time.Second,
			ConnectionTimeout:   10 * time.Second,
			StatusPollInterval:  5 * time.Second,
		},
		Queue: QueueConfig{
			MaxRetries:  3,
			RetryDelay:  10 * time.Second,
			WorkerCount: 2,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

func Load(configPath string) (*Config, error) {
	cfg := defaults()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.ApplyDeviceEnv(os.Getenv)
			return cfg, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	cfg.ApplyDeviceEnv(os.Getenv)
	return cfg, nil
}

// ApplyDeviceEnv overlays the scale and ORRN link settings from the
// environment (the install script writes ORRN_SPOOL_*; SPOOL_SCALE_* is for
// the scale). Set variables win over config.yaml.
func (c *Config) ApplyDeviceEnv(getenv func(string) string) {
	if v := getenv("SPOOL_SCALE_DEVICE"); v != "" {
		c.Scale.Device = v
	}
	if v := getenv("SPOOL_SCALE_BAUD"); v != "" {
		if baud, err := strconv.Atoi(v); err == nil {
			c.Scale.Baud = baud
		}
	}
	if v := getenv("SPOOL_SCALE_FRAME"); v != "" {
		c.Scale.Frame = v
	}
	if v := getenv("SPOOL_SCALE_SIMULATE"); v != "" {
		if on, err := strconv.ParseBool(v); err == nil {
			c.Scale.Simulate = on
		}
	}
	if v := getenv("ORRN_SPOOL_INSTANCE_ID"); v != "" {
		c.ORRN.InstanceID = v
	}
	if v := getenv("ORRN_SPOOL_SUBDOMAIN"); v != "" {
		c.ORRN.Subdomain = v
	}
	if v := getenv("ORRN_SPOOL_SERVER_URL"); v != "" {
		c.ORRN.ServerURL = v
	}
	if v := getenv("ORRN_SPOOL_SHARED_SECRET"); v != "" {
		c.ORRN.SharedSecret = v
	}
}

func LoadFromEnv() *Config {
	cfg := defaults()

	if v := os.Getenv("SPOOL_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
	}

	if v := os.Getenv("SPOOL_DB_PATH"); v != "" {
		cfg.Database.Path = v
	}

	if v := os.Getenv("SPOOL_ARCHIVE_PATH"); v != "" {
		cfg.Database.ArchivePath = v
	}

	if v := os.Getenv("SPOOL_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}

	return cfg
}

func (c *Config) Validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server port must be between 1 and 65535, got %d", c.Server.Port)
	}

	if c.Server.ReadTimeout < 0 {
		return fmt.Errorf("server read timeout must be non-negative")
	}

	if c.Server.WriteTimeout < 0 {
		return fmt.Errorf("server write timeout must be non-negative")
	}

	if c.Database.Path == "" {
		return fmt.Errorf("database path is required")
	}

	if c.Database.ArchiveDays < 0 {
		return fmt.Errorf("archive days must be non-negative")
	}

	if c.Printers.HealthCheckInterval < 0 {
		return fmt.Errorf("health check interval must be non-negative")
	}

	if c.Printers.ConnectionTimeout < 0 {
		return fmt.Errorf("connection timeout must be non-negative")
	}

	if c.Printers.StatusPollInterval < 0 {
		return fmt.Errorf("status poll interval must be non-negative")
	}

	if c.Queue.MaxRetries < 0 {
		return fmt.Errorf("max retries must be non-negative")
	}

	if c.Queue.RetryDelay < 0 {
		return fmt.Errorf("retry delay must be non-negative")
	}

	if c.Queue.WorkerCount < 1 {
		return fmt.Errorf("worker count must be at least 1")
	}

	validLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}

	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("invalid log level: %s (valid: debug, info, warn, error)", c.Logging.Level)
	}

	validFormats := map[string]bool{
		"json":  true,
		"text":  true,
		"plain": true,
	}

	if !validFormats[c.Logging.Format] {
		return fmt.Errorf("invalid log format: %s (valid: json, text, plain)", c.Logging.Format)
	}

	if err := c.Scale.Validate(); err != nil {
		return err
	}

	if c.ORRN.HeartbeatInterval < 0 {
		return fmt.Errorf("orrn heartbeat interval must be non-negative")
	}

	return nil
}
