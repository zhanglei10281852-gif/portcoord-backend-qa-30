package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds all service configuration values.
type Config struct {
	Server    ServerConfig
	Database  DatabaseConfig
	Scheduler SchedulerConfig
	Executor  ExecutorConfig
	Logging   LoggingConfig
	Quotas    QuotaConfig
}

type ServerConfig struct {
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

type DatabaseConfig struct {
	DataDir      string
	DBName       string
	MaxOpenConns int
	MaxIdleConns int
}

type SchedulerConfig struct {
	TickInterval       time.Duration
	LeaseTimeout       time.Duration
	EscalationInterval time.Duration
	MaxRetries         int
}

type ExecutorConfig struct {
	PollInterval time.Duration
	BatchSize    int
	ID           string
}

type LoggingConfig struct {
	Level  string
	Pretty bool
}

type QuotaConfig struct {
	DailyCabinLimit       int
	DailyYardLimit        int
	CabinWarningThreshold float64
	YardWarningThreshold  float64
}

// Default returns a Config with production-safe defaults.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Port:            58552,
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			IdleTimeout:     120 * time.Second,
			ShutdownTimeout: 15 * time.Second,
		},
		Database: DatabaseConfig{
			DataDir:      "./data",
			DBName:       "portcoord.db",
			MaxOpenConns: 10,
			MaxIdleConns: 5,
		},
		Scheduler: SchedulerConfig{
			TickInterval:       5 * time.Second,
			LeaseTimeout:       60 * time.Second,
			EscalationInterval: 10 * time.Second,
			MaxRetries:         3,
		},
		Executor: ExecutorConfig{
			PollInterval: 3 * time.Second,
			BatchSize:    10,
			ID:           "executor-1",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Pretty: true,
		},
		Quotas: QuotaConfig{
			DailyCabinLimit:       1000,
			DailyYardLimit:        5000,
			CabinWarningThreshold: 0.8,
			YardWarningThreshold:  0.8,
		},
	}
}

// FromEnv builds a Config from environment variables, falling back to defaults.
func FromEnv() *Config {
	c := Default()
	if v := os.Getenv("PORTCOORD_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.Server.Port = p
		}
	}
	if v := os.Getenv("PORTCOORD_DATA_DIR"); v != "" {
		c.Database.DataDir = v
	}
	if v := os.Getenv("PORTCOORD_DB_NAME"); v != "" {
		c.Database.DBName = v
	}
	if v := os.Getenv("PORTCOORD_REQUEST_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Server.ReadTimeout = time.Duration(n) * time.Second
			c.Server.WriteTimeout = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("PORTCOORD_SCHEDULER_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Scheduler.TickInterval = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("PORTCOORD_LEASE_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Scheduler.LeaseTimeout = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("PORTCOORD_ESCALATION_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Scheduler.EscalationInterval = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("PORTCOORD_LOG_LEVEL"); v != "" {
		c.Logging.Level = strings.ToLower(v)
	}
	if v := os.Getenv("PORTCOORD_EXECUTOR_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Executor.PollInterval = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("PORTCOORD_EXECUTOR_ID"); v != "" {
		c.Executor.ID = v
	}
	return c
}

// Validate checks the Config for obvious errors.
func (c *Config) Validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Server.Port)
	}
	if c.Database.DataDir == "" {
		return fmt.Errorf("data_dir must not be empty")
	}
	if c.Database.DBName == "" {
		return fmt.Errorf("db_name must not be empty")
	}
	if !strings.HasSuffix(c.Database.DBName, ".db") {
		return fmt.Errorf("db_name must end with .db")
	}
	if c.Scheduler.TickInterval < 100*time.Millisecond {
		return fmt.Errorf("scheduler tick_interval too small")
	}
	if c.Scheduler.LeaseTimeout < 1*time.Second {
		return fmt.Errorf("lease timeout too small")
	}
	if c.Executor.BatchSize < 1 {
		return fmt.Errorf("executor batch_size must be >= 1")
	}
	if c.Quotas.DailyCabinLimit < 1 || c.Quotas.DailyYardLimit < 1 {
		return fmt.Errorf("quota limits must be positive")
	}
	return nil
}

// DBPath returns the full path to the SQLite database file.
func (c *Config) DBPath() string {
	return filepath.Join(c.Database.DataDir, c.Database.DBName)
}

// EnsureDataDir creates the data directory if it does not exist.
func (c *Config) EnsureDataDir() error {
	if err := os.MkdirAll(c.Database.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	return nil
}
