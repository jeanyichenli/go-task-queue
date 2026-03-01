package job

import "time"

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Job represents a unit of work in the task queue.
type Job struct {
	ID   string `json:"id"`
	Type string `json:"type"`

	// Payload is the job data. It is a map of ID string to any.
	Payload map[string]any `json:"payload"`

	// Status and timing
	Status      Status     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`

	// Retries
	Attempt     int    `json:"attempt"`
	MaxAttempts int    `json:"max_attempts"`
	LastError   string `json:"last_error"`

	// Priority: higher = more urgent. 0 = default.
	Priority int `json:"priority"`
}
