// Package logger provides level-based, classified logging for the service.
// Each log line includes a level (debug, info, warn, error) and a class
// (cmd, api, worker, queue, job, system) so you can filter in prod or locally.
package logger

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	internalmongo "go-task-queue/internal/mongo"

	"go.mongodb.org/mongo-driver/bson"
)

// Level orders verbosity. A configured minimum level suppresses lower levels.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var levelNames = map[Level]string{
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO",
	LevelWarn:  "WARN",
	LevelError: "ERROR",
}

// ParseLevel returns the Level for s (case-insensitive). Default is Info.
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug", "d":
		return LevelDebug
	case "info", "i":
		return LevelInfo
	case "warn", "warning", "w":
		return LevelWarn
	case "error", "err", "e":
		return LevelError
	default:
		return LevelInfo
	}
}

// Class identifies the subsystem emitting the log (for filtering and search).
type Class string

const (
	ClassCmd    Class = "cmd"    // process entry, config, shutdown orchestration
	ClassAPI    Class = "api"    // HTTP handlers and server
	ClassWorker Class = "worker" // dequeue loop, pool lifecycle
	ClassQueue  Class = "queue"  // enqueue/dequeue/persist (Redis)
	ClassJob    Class = "job"    // handler execution, job lifecycle logs
	ClassSystem Class = "system" // infra: redis client, generic system
)

// Logger writes prefixed lines if level and class pass filters.
type Logger struct {
	mu          sync.Mutex
	out         io.Writer
	min         Level
	allowClass  map[Class]struct{} // nil or empty internal map means allow all
	allowAllCls bool

	// mongo logging (optional).
	mongoOnce sync.Once
	mongoErr  error
}

// logEntry is the MongoDB representation of a log line.
type logEntry struct {
	Timestamp time.Time `bson:"ts"`
	Level     string    `bson:"level"`
	Class     string    `bson:"class"`
	Message   string    `bson:"message"`
}

// New builds a Logger. If allowClasses is nil or empty, all classes are allowed.
// Otherwise only listed classes (case-insensitive) are logged.
func New(out io.Writer, min Level, allowClasses []string) *Logger {
	l := &Logger{out: out, min: min, allowAllCls: true}
	if out == nil {
		l.out = os.Stderr
	}
	if len(allowClasses) > 0 {
		seen := make(map[Class]struct{})
		for _, c := range allowClasses {
			c = strings.TrimSpace(strings.ToLower(c))
			if c == "" || c == "all" {
				l.allowAllCls = true
				l.allowClass = nil
				return l
			}
			seen[Class(c)] = struct{}{}
		}
		if len(seen) > 0 {
			l.allowClass = seen
			l.allowAllCls = false
		}
	}
	return l
}

func (l *Logger) enabled(level Level, class Class) bool {
	if level < l.min {
		return false
	}
	if l.allowAllCls || l.allowClass == nil {
		return true
	}
	_, ok := l.allowClass[class]
	return ok
}

func (l *Logger) log(level Level, class Class, format string, args ...any) {
	if !l.enabled(level, class) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now().UTC()
	ts := now.Format(time.RFC3339Nano)
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("%s %-5s [%s] %s\n", ts, levelNames[level], class, msg)
	_, _ = io.WriteString(l.out, line)

	// Best-effort write to MongoDB; failures do not affect the main flow.
	l.mongoOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		mc, err := internalmongo.NewClient(ctx)
		if err != nil {
			l.mongoErr = err
			return
		}
		_ = mc // only ensure client is initialized; collection fetched below per call
	})
	if l.mongoErr != nil {
		return
	}

	go func(le logEntry) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		mc, err := internalmongo.NewClient(ctx)
		if err != nil {
			return
		}
		coll := mc.LogCollection()
		_, _ = coll.InsertOne(ctx, bson.M{
			"ts":     le.Timestamp,
			"level":  le.Level,
			"class":  le.Class,
			"message": le.Message,
		})
	}(logEntry{
		Timestamp: now,
		Level:     levelNames[level],
		Class:     string(class),
		Message:   msg,
	})
}

func (l *Logger) Debug(c Class, format string, args ...any) { l.log(LevelDebug, c, format, args...) }
func (l *Logger) Info(c Class, format string, args ...any)  { l.log(LevelInfo, c, format, args...) }
func (l *Logger) Warn(c Class, format string, args ...any)  { l.log(LevelWarn, c, format, args...) }
func (l *Logger) Error(c Class, format string, args ...any) { l.log(LevelError, c, format, args...) }

// --- Default logger (set from main via SetDefault) ---

var (
	defaultMu sync.RWMutex
	defaultL  = New(os.Stderr, LevelInfo, nil)
)

// Default returns the process-wide logger.
func Default() *Logger {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultL
}

// SetDefault replaces the default logger (e.g. after parsing config).
func SetDefault(l *Logger) {
	if l == nil {
		l = New(os.Stderr, LevelInfo, nil)
	}
	defaultMu.Lock()
	defaultL = l
	defaultMu.Unlock()
	// Keep std log prefix minimal for any legacy log.Print still in tree.
	log.SetOutput(l.out)
	log.SetFlags(0)
}

// SetOutput is shorthand to re-point only the writer on the default logger (tests).
func SetOutput(w io.Writer) {
	defaultMu.Lock()
	if defaultL != nil {
		defaultL.out = w
	}
	defaultMu.Unlock()
}

// ParseClassList splits a comma-separated env value into class names for New(..., allowClasses).
func ParseClassList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
