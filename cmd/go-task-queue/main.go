package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-task-queue/internal/config"
	"go-task-queue/internal/dlq"
	"go-task-queue/internal/httpapi"
	"go-task-queue/internal/job"
	"go-task-queue/internal/logger"
	"go-task-queue/internal/queue"
	"go-task-queue/internal/worker"

	"github.com/redis/go-redis/v9"
)

// lg is the process-wide application logger. It is set in main after
// logger.SetDefault so config (level, class filter) applies everywhere.
var lg *logger.Logger

// HandlerConfig describes how a particular job type should be handled.
// It is loaded from an external JSON configuration file so users do not need
// to modify code to add or change handlers. In the future this can be used to
// drive real RPC calls to other microservices.
type HandlerConfig struct {
	Type           string `json:"type"`            // job.Type (e.g. "echo", "email.send")
	Target         string `json:"target"`          // logical service or URL (for future RPC)
	TimeoutSeconds int    `json:"timeout_seconds"` // per-call timeout in seconds (for future RPC)
	MaxAttempts    int    `json:"max_attempts"`    // default max attempts when job.MaxAttempts == 0
}

func main() {
	// Base configuration from environment.
	cfg := config.FromEnv()
	config.BindFlags(&cfg)
	flag.Parse()

	logger.SetDefault(logger.New(os.Stderr, logger.ParseLevel(cfg.LogLevel), logger.ParseClassList(cfg.LogClass)))
	lg = logger.Default()
	worker.SetLogger(lg)
	httpapi.SetLogger(lg)
	queue.SetLogger(lg)

	lg.Info(logger.ClassCmd, "starting go-task-queue service (redis=%s, workers=%d, http=%s)", cfg.RedisAddr, cfg.WorkerCount, cfg.HTTPAddr)

	// Root context for the whole service.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up Redis-backed queue.
	rdb := redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddr,
	})
	q := queue.NewRedisQueue(&redis.Options{
		Addr: cfg.RedisAddr,
	})

	// Set up Mongo-backed DLQ.
	dlqStore, err := dlq.NewMongoDLQ(ctx)
	if err != nil {
		lg.Warn(logger.ClassSystem, "failed to initialize Mongo DLQ: %v; DLQ endpoints disabled", err)
	}

	// Build worker handlers from external configuration. If loading fails for
	// any reason, fall back to a small in-memory default so the service still
	// starts in development.
	handlerConfigs, err := loadHandlerConfigs(cfg.HandlersConfigPath)
	if err != nil {
		lg.Warn(logger.ClassCmd, "failed to load handler config from %q: %v; falling back to defaults", cfg.HandlersConfigPath, err)
		handlerConfigs = defaultHandlerConfigs()
	}
	handlers := buildHandlers(handlerConfigs, q)

	pool := worker.NewWorkerPool(ctx, q, handlers, cfg.WorkerCount, dlqStore)
	pool.Start()
	lg.Info(logger.ClassCmd, "worker pool started with %d workers", cfg.WorkerCount)

	// HTTP server using internal httpapi package.
	api := httpapi.NewServer(q, dlqStore)
	srv := api.HTTPServer(cfg.HTTPAddr)

	// Start HTTP server.
	go func() {
		lg.Info(logger.ClassAPI, "http server listening on %s", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			lg.Error(logger.ClassAPI, "http server error: %v", err)
			cancel()
		}
	}()

	// Handle OS signals for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigCh:
		lg.Info(logger.ClassCmd, "signal received, shutting down...")
	case <-ctx.Done():
		lg.Info(logger.ClassCmd, "context cancelled, shutting down...")
	}

	// Begin graceful shutdown.
	cancel()

	// Use a relatively short timeout so shutdown does not hang for long.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	lg.Info(logger.ClassCmd, "shutdown: calling http server Shutdown")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		lg.Error(logger.ClassAPI, "http server shutdown error: %v", err)
	}
	lg.Info(logger.ClassCmd, "shutdown: http server Shutdown complete")

	lg.Info(logger.ClassCmd, "shutdown: calling worker pool Stop")
	pool.Stop()
	lg.Info(logger.ClassCmd, "shutdown: worker pool Stop complete")

	lg.Info(logger.ClassCmd, "shutdown: calling queue Close")
	if err := q.Close(shutdownCtx); err != nil {
		lg.Error(logger.ClassQueue, "queue close error: %v", err)
	}
	lg.Info(logger.ClassCmd, "shutdown: queue Close complete")

	lg.Info(logger.ClassSystem, "shutdown: calling redis client Close")
	if err := rdb.Close(); err != nil {
		lg.Error(logger.ClassSystem, "redis client close error: %v", err)
	}

	lg.Info(logger.ClassCmd, "shutdown complete")
}

// defaultHandlerConfigs returns an in-memory set of handler configurations used
// when the external configuration is missing or invalid.
func defaultHandlerConfigs() []HandlerConfig {
	return []HandlerConfig{
		{
			Type:           "echo",
			Target:         "mock://echo-service",
			TimeoutSeconds: 2,
			MaxAttempts:    3,
		},
	}
}

// buildHandlers constructs a map of WorkerPool handlers from HandlerConfig.
func buildHandlers(cfgs []HandlerConfig, q queue.Queue) map[string]worker.Handler {
	handlers := make(map[string]worker.Handler, len(cfgs))

	for _, hc := range cfgs {
		cfg := hc // capture loop variable

		timeout := 2 * time.Second
		if cfg.TimeoutSeconds > 0 {
			timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
		}

		handlers[cfg.Type] = func(ctx context.Context, j *job.Job) error {
			if j.MaxAttempts == 0 && cfg.MaxAttempts > 0 {
				j.MaxAttempts = cfg.MaxAttempts
			}

			payload, _ := json.Marshal(j.Payload)
			lg.Info(logger.ClassJob, "handler type=%s target=%s timeout=%s job_id=%s payload=%s (mocked RPC)",
				cfg.Type, cfg.Target, timeout.String(), j.ID, string(payload),
			)

			if err := q.UpdateStatus(ctx, j.ID, job.StatusCompleted); err != nil {
				return err
			}
			return nil
		}
	}

	return handlers
}

func loadHandlerConfigs(path string) ([]HandlerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var cfgs []HandlerConfig
	if err := json.Unmarshal(data, &cfgs); err != nil {
		return nil, fmt.Errorf("unmarshal handler config: %w", err)
	}
	return cfgs, nil
}
