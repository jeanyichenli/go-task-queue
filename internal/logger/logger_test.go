package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	if ParseLevel("") != LevelInfo {
		t.Fatal("empty -> Info")
	}
	if ParseLevel("DEBUG") != LevelDebug {
		t.Fatal()
	}
	if ParseLevel("error") != LevelError {
		t.Fatal()
	}
}

func TestLoggerFiltersLevel(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, LevelWarn, nil)
	l.Info(ClassCmd, "nope")
	l.Warn(ClassCmd, "yes")
	if !strings.Contains(buf.String(), "yes") || strings.Contains(buf.String(), "nope") {
		t.Fatalf("buf=%q", buf.String())
	}
}

func TestLoggerFiltersClass(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, LevelDebug, []string{"api"})
	l.Debug(ClassWorker, "worker")
	l.Debug(ClassAPI, "api")
	if strings.Contains(buf.String(), "worker") || !strings.Contains(buf.String(), "api") {
		t.Fatalf("buf=%q", buf.String())
	}
}
