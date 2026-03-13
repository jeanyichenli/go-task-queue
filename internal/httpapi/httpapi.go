package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"go-task-queue/internal/job"
	"go-task-queue/internal/logger"
	"go-task-queue/internal/queue"
)

// lg is the package logger; set from cmd via SetLogger after logger.SetDefault.
var lg *logger.Logger

// SetLogger assigns the logger used by this package (typically the same instance as main).
func SetLogger(l *logger.Logger) {
	lg = l
}

// Server provides HTTP handlers backed by a queue.Queue.
type Server struct {
	queue queue.Queue
}

// NewServer constructs a new HTTP API server.
func NewServer(q queue.Queue) *Server {
	return &Server{queue: q}
}

// HTTPServer constructs an *http.Server with all routes registered.
func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:    addr,
		Handler: s.Router(),
	}
}

// Router returns an http.Handler with all routes registered.
func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/jobs", s.handleJobs)
	mux.HandleFunc("/jobs/", s.handleJobByID)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleEnqueueJob(w, r)
	case http.MethodGet:
		s.handleListJobs(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleJobByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Path[len("/jobs/"):]
	if id == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	j, err := s.queue.GetJob(ctx, id)
	if err != nil {
		lg.Error(logger.ClassAPI, "GET /jobs/%s failed: %v", id, err)
		http.Error(w, "failed to get job", http.StatusInternalServerError)
		return
	}
	if j == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, j)
}

func (s *Server) handleEnqueueJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Type        string                 `json:"type"`
		Payload     map[string]any         `json:"payload"`
		MaxAttempts int                    `json:"max_attempts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if req.Type == "" {
		http.Error(w, "`type` is required", http.StatusBadRequest)
		return
	}

	now := time.Now()
	id := strconv.FormatInt(now.UnixNano(), 10)

	j := &job.Job{
		ID:          id,
		Type:        req.Type,
		Payload:     req.Payload,
		Status:      job.StatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
		Attempt:     0,
		MaxAttempts: req.MaxAttempts,
		LastError:   "",
	}

	if err := s.queue.Enqueue(ctx, j); err != nil {
		lg.Error(logger.ClassAPI, "POST /jobs enqueue failed: %v", err)
		http.Error(w, "failed to enqueue job", http.StatusInternalServerError)
		return
	}
	lg.Info(logger.ClassAPI, "POST /jobs enqueued id=%s type=%s", j.ID, j.Type)

	writeJSON(w, map[string]any{
		"id":     j.ID,
		"status": j.Status,
	})
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobs, err := s.queue.ListJobs(ctx)
	if err != nil {
		lg.Error(logger.ClassAPI, "GET /jobs list failed: %v", err)
		http.Error(w, "failed to list jobs", http.StatusInternalServerError)
		return
	}
	lg.Debug(logger.ClassAPI, "GET /jobs ok count=%d", len(jobs))
	writeJSON(w, jobs)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
