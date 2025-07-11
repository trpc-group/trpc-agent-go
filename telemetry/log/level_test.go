//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package log

// ... existing code ...

import (
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

// TestTracef makes sure Tracef forwards the call to the underlying
// logger at the debug level. The test installs a stub logger that
// records the last format string received and asserts that Tracef
// prefixes the message with the required tag.
func TestTracef(t *testing.T) {
	var recorded string
	stub := &stubLogger{debugf: func(format string, _ ...any) {
		recorded = format
	}}

	// Replace Default logger with stub and restore afterwards.
	old := Default
	Default = stub
	defer func() { Default = old }()

	Tracef("hello %s", "world")

	wantPrefix := "[TRACE] "
	if recorded == "" || recorded[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("Tracef did not prefix message with %q: got %q", wantPrefix, recorded)
	}
}

// stubLogger is a minimal implementation of Logger that captures
// Debugf calls for verification.
// Only the methods required by the tests are implemented; the rest
// are no-ops to satisfy the interface.
type stubLogger struct {
	debugf func(format string, args ...any)
}

func (s *stubLogger) Debug(args ...any)                 {}
func (s *stubLogger) Debugf(format string, args ...any) { s.debugf(format, args...) }
func (s *stubLogger) Info(args ...any)                  {}
func (s *stubLogger) Infof(format string, args ...any)  {}
func (s *stubLogger) Warn(args ...any)                  {}
func (s *stubLogger) Warnf(format string, args ...any)  {}
func (s *stubLogger) Error(args ...any)                 {}
func (s *stubLogger) Errorf(format string, args ...any) {}
func (s *stubLogger) Fatal(args ...any)                 {}
func (s *stubLogger) Fatalf(format string, args ...any) {}
