package config

import (
	"flag"
	"os"
	"strconv"
)

// Config holds runtime configuration for the service.
type Config struct {
	RedisAddr         string
	WorkerCount       int
	HTTPAddr          string
	HandlersConfigPath string
}

const (
	envRedisAddr          = "REDIS_ADDR"
	envWorkerCount        = "WORKERS"
	envHTTPAddr           = "HTTP_ADDR"
	envHandlersConfigPath = "HANDLERS_CONFIG"
)

// FromEnv loads configuration from environment variables, applying sensible defaults.
// Defaults:
//   - RedisAddr: "localhost:6379"
//   - WorkerCount: 4
//   - HTTPAddr: ":8080"
func FromEnv() Config {
	cfg := Config{
		RedisAddr:         "localhost:6379",
		WorkerCount:       4,
		HTTPAddr:          ":8080",
		HandlersConfigPath: "config/handlers.json",
	}

	if v := os.Getenv(envRedisAddr); v != "" {
		cfg.RedisAddr = v
	}
	if v := os.Getenv(envHTTPAddr); v != "" {
		cfg.HTTPAddr = v
	}
	if v := os.Getenv(envWorkerCount); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.WorkerCount = n
		}
	}

	if v := os.Getenv(envHandlersConfigPath); v != "" {
		cfg.HandlersConfigPath = v
	}

	return cfg
}

// BindFlags binds command-line flags to the given Config, using its current
// values as defaults. Call flag.Parse() after this.
func BindFlags(cfg *Config) {
	if cfg == nil {
		return
	}

	flag.StringVar(&cfg.RedisAddr, "redis-addr", cfg.RedisAddr, "Redis address (host:port)")
	flag.IntVar(&cfg.WorkerCount, "workers", cfg.WorkerCount, "Number of worker goroutines")
	flag.StringVar(&cfg.HTTPAddr, "http-addr", cfg.HTTPAddr, "HTTP listen address")
	flag.StringVar(&cfg.HandlersConfigPath, "handlers-config", cfg.HandlersConfigPath, "Path to JSON file describing worker handler configurations")
}

