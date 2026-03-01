package job

import (
	"testing"
	"time"
)

func TestStatus_String(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusPending, "pending"},
		{StatusRunning, "running"},
		{StatusCompleted, "completed"},
		{StatusFailed, "failed"},
		{StatusCancelled, "cancelled"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := string(tt.status); got != tt.want {
				t.Errorf("Status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestJob_ZeroValue(t *testing.T) {
	var j Job
	if j.ID != "" {
		t.Errorf("zero Job ID = %q, want empty", j.ID)
	}
	if j.Type != "" {
		t.Errorf("zero Job Type = %q, want empty", j.Type)
	}
	if j.Status != "" {
		t.Errorf("zero Job Status = %q, want empty", j.Status)
	}
	if j.Attempt != 0 {
		t.Errorf("zero Job Attempt = %d, want 0", j.Attempt)
	}
	if j.StartedAt != nil {
		t.Error("zero Job StartedAt should be nil")
	}
	if j.CompletedAt != nil {
		t.Error("zero Job CompletedAt should be nil")
	}
}

func TestJob_WithFields(t *testing.T) {
	now := time.Now()
	started := now.Add(time.Minute)
	completed := now.Add(2 * time.Minute)

	j := Job{
		ID:          "job-1",
		Type:        "email",
		Payload:     map[string]any{"job-1": map[string]any{"to": "user@example.com"}},
		Status:      StatusPending,
		CreatedAt:   now,
		ScheduledAt: now,
		StartedAt:   &started,
		CompletedAt: &completed,
		Attempt:     1,
		MaxAttempts: 3,
		LastError:   "",
		Priority:    1,
	}

	if j.ID != "job-1" {
		t.Errorf("ID = %q, want job-1", j.ID)
	}
	if j.Type != "email" {
		t.Errorf("Type = %q, want email", j.Type)
	}
	if j.Payload == nil {
		t.Fatal("Payload should not be nil")
	}
	if got, ok := j.Payload["job-1"].(map[string]any)["to"].(string); !ok || got != "user@example.com" {
		t.Errorf("Payload[\"job-1\"][\"to\"] = %v, want user@example.com", j.Payload["job-1"])
	}
	if j.Status != StatusPending {
		t.Errorf("Status = %q, want pending", j.Status)
	}
	if j.Attempt != 1 || j.MaxAttempts != 3 {
		t.Errorf("Attempt = %d, MaxAttempts = %d; want 1, 3", j.Attempt, j.MaxAttempts)
	}
	if j.Priority != 1 {
		t.Errorf("Priority = %d, want 1", j.Priority)
	}
	if j.StartedAt == nil || j.CompletedAt == nil {
		t.Error("StartedAt and CompletedAt should be set")
	}
}

func TestJob_RetryState(t *testing.T) {
	j := Job{
		ID:          "job-retry",
		Type:        "webhook",
		Attempt:     2,
		MaxAttempts: 3,
		LastError:   "connection refused",
		Status:      StatusFailed,
	}
	if j.Attempt != 2 {
		t.Errorf("Attempt = %d, want 2", j.Attempt)
	}
	if j.LastError != "connection refused" {
		t.Errorf("LastError = %q, want connection refused", j.LastError)
	}
	if j.Status != StatusFailed {
		t.Errorf("Status = %q, want failed", j.Status)
	}
}
