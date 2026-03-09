package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-task-queue/internal/config"
	"go-task-queue/internal/httpapi"
	"go-task-queue/internal/job"
	"go-task-queue/internal/queue"
	"go-task-queue/internal/worker"

	"github.com/redis/go-redis/v9"
)

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

	log.Printf("starting go-task-queue service (redis=%s, workers=%d, http=%s)", cfg.RedisAddr, cfg.WorkerCount, cfg.HTTPAddr)

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

	// Build worker handlers from external configuration. If loading fails for
	// any reason, fall back to a small in-memory default so the service still
	// starts in development.
	handlerConfigs, err := loadHandlerConfigs(cfg.HandlersConfigPath)
	if err != nil {
		log.Printf("failed to load handler config from %q: %v; falling back to defaults", cfg.HandlersConfigPath, err)
		handlerConfigs = defaultHandlerConfigs()
	}
	handlers := buildHandlers(handlerConfigs, q)

	pool := worker.NewWorkerPool(ctx, q, handlers, cfg.WorkerCount)
	pool.Start()
	log.Printf("worker pool started with %d workers", cfg.WorkerCount)

	// HTTP server using internal httpapi package.
	api := httpapi.NewServer(q)
	srv := api.HTTPServer(cfg.HTTPAddr)

	// Start HTTP server.
	go func() {
		log.Printf("http server listening on %s", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("http server error: %v", err)
			cancel()
		}
	}()

	// Handle OS signals for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigCh:
		log.Printf("signal received, shutting down...")
	case <-ctx.Done():
		log.Printf("context cancelled, shutting down...")
	}

	// Begin graceful shutdown.
	cancel()

	// Use a relatively short timeout so shutdown does not hang for long.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	log.Printf("shutdown: calling http server Shutdown")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http server shutdown error: %v", err)
	}
	log.Printf("shutdown: http server Shutdown complete")

	log.Printf("shutdown: calling worker pool Stop")
	pool.Stop()
	log.Printf("shutdown: worker pool Stop complete")

	log.Printf("shutdown: calling queue Close")
	if err := q.Close(shutdownCtx); err != nil {
		log.Printf("queue close error: %v", err)
	}
	log.Printf("shutdown: queue Close complete")

	log.Printf("shutdown: calling redis client Close")
	if err := rdb.Close(); err != nil {
		log.Printf("redis client close error: %v", err)
	}

	log.Printf("shutdown complete")
}

// defaultHandlerConfigs returns an in-memory set of handler configurations used
// when the external configuration is missing or invalid.
func defaultHandlerConfigs() []HandlerConfig {
	return []HandlerConfig{
		{
			Type:        "echo",
			Target:      "mock://echo-service",
			TimeoutSeconds: 2,
			MaxAttempts: 3,
		},
		// Additional handler types can be added here, for example:
		// {
		//     Type:        "email.send",
		//     Target:      "http://email-service",
		//     Timeout:     5 * time.Second,
		//     MaxAttempts: 5,
		// },
	}
}

// buildHandlers constructs a map of WorkerPool handlers from HandlerConfig.
// Each handler is currently a mocked implementation that just logs and marks
// the job as completed. The real implementation can later perform RPC calls
// to external microservices.
func buildHandlers(cfgs []HandlerConfig, q queue.Queue) map[string]worker.Handler {
	handlers := make(map[string]worker.Handler, len(cfgs))

	for _, hc := range cfgs {
		cfg := hc // capture loop variable

		// Precompute timeout duration; if not set, use a small default.
		timeout := 2 * time.Second
		if cfg.TimeoutSeconds > 0 {
			timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
		}

		handlers[cfg.Type] = func(ctx context.Context, j *job.Job) error {
			// Apply default MaxAttempts from config when the job does not set it.
			if j.MaxAttempts == 0 && cfg.MaxAttempts > 0 {
				j.MaxAttempts = cfg.MaxAttempts
			}

			// Mocked “RPC call” – for now we just log. In a real implementation,
			// this is where you would perform an HTTP/gRPC/etc. call to the
			// microservice identified by cfg.Target, passing j.Payload.
			payload, _ := json.Marshal(j.Payload)
			log.Printf(
				"handler type=%s target=%s timeout=%s job_id=%s payload=%s (mocked RPC)",
				cfg.Type,
				cfg.Target,
				timeout.String(),
				j.ID,
				string(payload),
			)

			// Example future RPC flow (commented until implemented):
			//
			// callCtx, cancel := context.WithTimeout(ctx, timeout)
			// defer cancel()
			//
			// reqBody, err := json.Marshal(j.Payload)
			// if err != nil {
			//     return err
			// }
			// httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, cfg.Target, bytes.NewReader(reqBody))
			// if err != nil {
			//     return err
			// }
			// httpReq.Header.Set("Content-Type", "application/json")
			// resp, err := http.DefaultClient.Do(httpReq)
			// if err != nil {
			//     return err
			// }
			// defer resp.Body.Close()
			// if resp.StatusCode >= 500 {
			//     return fmt.Errorf("remote service error: %s", resp.Status)
			// }

			// For now, pretend the RPC succeeded and mark the job as completed.
			if err := q.UpdateStatus(ctx, j.ID, job.StatusCompleted); err != nil {
				return err
			}
			return nil
		}
	}

	return handlers
}

// loadHandlerConfigs reads handler configuration from a JSON file at the given
// path. The JSON format is:
//
// [
//   {
//     "type": "echo",
//     "target": "mock://echo-service",
//     "timeout_seconds": 2,
//     "max_attempts": 3
//   }
// ]
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
