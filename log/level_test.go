//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package log

// ... existing code ...

import (
	"strings"
	"testing"

	"go.uber.org/zap/zapcore"
)

// TestSetLevel verifies that SetLevel correctly updates the
// underlying zap atomic level according to the provided level
// string. It iterates through all supported levels and checks the
// zapLevel after the call.
func TestSetLevel(t *testing.T) {
	cases := []struct {
		in       string
		expected zapcore.Level
	}{
		{LevelDebug, zapcore.DebugLevel},
		{LevelInfo, zapcore.InfoLevel},
		{LevelWarn, zapcore.WarnLevel},
		{LevelError, zapcore.ErrorLevel},
		{LevelFatal, zapcore.FatalLevel},
		{"unknown", zapcore.InfoLevel}, // default branch
	}

	for _, c := range cases {
		SetLevel(c.in)
		if got := zapLevel.Level(); got != c.expected {
			t.Fatalf("SetLevel(%q) = %v; want %v", c.in, got, c.expected)
		}
	}
}

// TestTraceDisabledByDefault ensures trace logging starts disabled and Tracef is a no-op.
func TestTraceDisabledByDefault(t *testing.T) {
	stub := &stubLogger{}
	oldDefault := Default
	oldTrace := traceEnabled
	Default = stub
	t.Cleanup(func() {
		Default = oldDefault
		traceEnabled = oldTrace
	})

	if traceEnabled {
		t.Fatalf("traceEnabled should be false by default")
	}

	Tracef("hello %s", "world")

	if stub.debugfCalls != 0 {
		t.Fatalf("Tracef should not log when trace is disabled; got %d calls", stub.debugfCalls)
	}
}

// TestTracefEnabled makes sure Tracef forwards the call when trace is enabled.
func TestTracefEnabled(t *testing.T) {
	stub := &stubLogger{}
	oldDefault := Default
	oldTrace := traceEnabled
	Default = stub
	SetTraceEnabled(true)
	t.Cleanup(func() {
		Default = oldDefault
		traceEnabled = oldTrace
	})

	Tracef("hello %s", "world")

	if stub.debugfCalls != 1 {
		t.Fatalf("Tracef should log once when trace is enabled; got %d calls", stub.debugfCalls)
	}
	if !strings.HasPrefix(stub.lastFormat, "[TRACE] ") {
		t.Fatalf("Tracef did not prefix message with \"[TRACE] \": got %q", stub.lastFormat)
	}
}

// stubLogger is a minimal implementation of Logger that captures
// Debugf calls for verification.
// Only the methods required by the tests are implemented; the rest
// are no-ops to satisfy the interface.
type stubLogger struct {
	lastFormat  string
	debugfCalls int
}

func (s *stubLogger) Debug(args ...any) {}
func (s *stubLogger) Debugf(format string, args ...any) {
	s.debugfCalls++
	s.lastFormat = format
}
func (s *stubLogger) Info(args ...any)                  {}
func (s *stubLogger) Infof(format string, args ...any)  {}
func (s *stubLogger) Warn(args ...any)                  {}
func (s *stubLogger) Warnf(format string, args ...any)  {}
func (s *stubLogger) Error(args ...any)                 {}
func (s *stubLogger) Errorf(format string, args ...any) {}
func (s *stubLogger) Fatal(args ...any)                 {}
func (s *stubLogger) Fatalf(format string, args ...any) {}
