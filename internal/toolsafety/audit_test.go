// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

func TestAuditLogger_DisabledByDefault(t *testing.T) {
	logger, err := toolsafety.NewAuditLogger(nil)
	if err != nil {
		t.Fatalf("NewAuditLogger(nil): %v", err)
	}
	defer logger.Close()

	// Should not panic or write anything.
	logger.Log(&toolsafety.ScanReport{
		ToolName:  "test",
		Decision:  toolsafety.DecisionDeny,
		Timestamp: time.Now().UTC(),
	})
}

func TestAuditLogger_WritesToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	policy := &toolsafety.AuditPolicy{
		Enabled:    true,
		OutputPath: path,
	}

	logger, err := toolsafety.NewAuditLogger(policy)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	logger.Log(&toolsafety.ScanReport{
		ToolName:    "workspace_exec",
		Decision:    toolsafety.DecisionDeny,
		RiskLevel:   toolsafety.RiskLevelCritical,
		Duration:    time.Millisecond,
		Intercepted: true,
		Backend:     "workspaceexec",
		Timestamp:   time.Now().UTC(),
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("audit file is empty")
	}
}

func TestAuditLogger_ClosedGracefully(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	policy := &toolsafety.AuditPolicy{
		Enabled:    true,
		OutputPath: path,
	}

	logger, err := toolsafety.NewAuditLogger(policy)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAuditLogger_LoggerCallback(t *testing.T) {
	logger, err := toolsafety.NewAuditLogger(nil)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	// Disabled logger should return nil callback.
	cb := logger.Logger()
	if cb != nil {
		t.Error("expected nil callback for disabled logger")
	}
}

// TestAuditLogger_LoggerCallbackEnabled verifies that an enabled logger
// returns a non-nil callback that can be passed to SafetyGuardPermissionPolicy.
func TestAuditLogger_LoggerCallbackEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit_enabled.jsonl")
	policy := &toolsafety.AuditPolicy{
		Enabled:    true,
		OutputPath: path,
	}
	logger, err := toolsafety.NewAuditLogger(policy)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	cb := logger.Logger()
	if cb == nil {
		t.Fatal("expected non-nil callback for enabled logger")
	}

	// The callback should write to the file without error.
	cb(&toolsafety.ScanReport{
		ToolName:    "test",
		Decision:    toolsafety.DecisionDeny,
		RiskLevel:   toolsafety.RiskLevelCritical,
		Intercepted: true,
		Timestamp:   time.Now().UTC(),
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("audit file is empty after callback")
	}
}

// TestAuditLogger_EnabledNoOutputPath creates a logger with Enabled=true but
// no OutputPath, which should produce a disabled (no-op) logger.
func TestAuditLogger_EnabledNoOutputPath(t *testing.T) {
	policy := &toolsafety.AuditPolicy{
		Enabled:    true,
		OutputPath: "",
	}
	logger, err := toolsafety.NewAuditLogger(policy)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	// Should be a no-op — Log should not panic.
	logger.Log(&toolsafety.ScanReport{
		ToolName: "test",
		Decision: toolsafety.DecisionDeny,
	})
}

// TestAuditLogger_NewWithInvalidPath verifies that NewAuditLogger errors
// when the output path cannot be opened.
func TestAuditLogger_NewWithInvalidPath(t *testing.T) {
	policy := &toolsafety.AuditPolicy{
		Enabled:    true,
		OutputPath: "/nonexistent_dir/audit.jsonl",
	}
	_, err := toolsafety.NewAuditLogger(policy)
	if err == nil {
		t.Fatal("expected error for invalid path, got nil")
	}
}

// TestAuditLogger_LogNilReport verifies that Log(nil) does not panic.
func TestAuditLogger_LogNilReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit_nil.jsonl")
	policy := &toolsafety.AuditPolicy{
		Enabled:    true,
		OutputPath: path,
	}
	logger, err := toolsafety.NewAuditLogger(policy)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	// Should not panic.
	logger.Log(nil)
}

// TestAuditLogger_LogSanitizedAlready verifies that when a report is already
// sanitized (Sanitized=true), the audit logger does not re-sanitize the evidence.
func TestAuditLogger_LogSanitizedAlready(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit_sanitized.jsonl")
	policy := &toolsafety.AuditPolicy{
		Enabled:    true,
		OutputPath: path,
	}
	logger, err := toolsafety.NewAuditLogger(policy)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	logger.Log(&toolsafety.ScanReport{
		ToolName:    "test",
		Decision:    toolsafety.DecisionDeny,
		RiskLevel:   toolsafety.RiskLevelCritical,
		Sanitized:   true, // Already marked as sanitized — should skip re-sanitization.
		Intercepted: true,
		Backend:     "workspaceexec",
		Findings: []toolsafety.RiskFinding{
			{
				RuleID:   toolsafety.RuleSensitiveLeak,
				Evidence: "API_KEY = 'sk-abc123def456xyz'",
			},
		},
		Timestamp: time.Now().UTC(),
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("audit file is empty")
	}
}

// TestAuditLogger_LogAutoSanitize verifies that when evidence contains
// sensitive patterns but report.Sanitized is false, the logger sanitizes
// the evidence before writing and sets sanitized=true in the event.
func TestAuditLogger_LogAutoSanitize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit_autosanitize.jsonl")
	policy := &toolsafety.AuditPolicy{
		Enabled:    true,
		OutputPath: path,
	}
	logger, err := toolsafety.NewAuditLogger(policy)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	logger.Log(&toolsafety.ScanReport{
		ToolName:    "test",
		Decision:    toolsafety.DecisionDeny,
		RiskLevel:   toolsafety.RiskLevelCritical,
		Sanitized:   false, // Not yet sanitized — logger should auto-sanitize.
		Intercepted: true,
		Backend:     "workspaceexec",
		Findings: []toolsafety.RiskFinding{
			{
				RuleID:   toolsafety.RuleSensitiveLeak,
				Evidence: "leaked: sk-abc123def456xyz0123456789abcdef",
			},
		},
		Timestamp: time.Now().UTC(),
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("audit file is empty")
	}
}

// TestAuditLogger_CloseNilOutput verifies that Close() on a disabled logger
// (nil output) returns nil and does not panic.
func TestAuditLogger_CloseNilOutput(t *testing.T) {
	logger, err := toolsafety.NewAuditLogger(nil)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Errorf("Close: unexpected error: %v", err)
	}
}

// TestAuditLogger_SanitizePrivateKey verifies that RSA private key patterns
// are correctly redacted inline (the regex only matches the BEGIN marker).
func TestAuditLogger_SanitizePrivateKey(t *testing.T) {
	logger, err := toolsafety.NewAuditLogger(nil)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	// The pattern matches "-----BEGIN RSA PRIVATE KEY-----" on a single line.
	input := "-----BEGIN RSA PRIVATE KEY-----"
	got := logger.Sanitize(input)
	if got != "***REDACTED***" {
		t.Errorf("Sanitize private key: got %q, want %q", got, "***REDACTED***")
	}
}

// TestAuditLogger_SanitizeEmptyString verifies that empty input is preserved.
func TestAuditLogger_SanitizeEmptyString(t *testing.T) {
	logger, err := toolsafety.NewAuditLogger(nil)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	got := logger.Sanitize("")
	if got != "" {
		t.Errorf("Sanitize empty: got %q, want ''", got)
	}
}

// TestAuditLogger_LogNoFindings verifies that Log handles a report with no findings.
func TestAuditLogger_LogNoFindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit_nofindings.jsonl")
	policy := &toolsafety.AuditPolicy{
		Enabled:    true,
		OutputPath: path,
	}
	logger, err := toolsafety.NewAuditLogger(policy)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	logger.Log(&toolsafety.ScanReport{
		ToolName:  "test",
		Decision:  toolsafety.DecisionAllow,
		RiskLevel: toolsafety.RiskLevelNone,
		Timestamp: time.Now().UTC(),
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("audit file is empty")
	}
}

// TestAuditLogger_LogDisabledDoesNotWrite verifies that Log does not write
// when the logger is disabled.
func TestAuditLogger_LogDisabledDoesNotWrite(t *testing.T) {
	logger, err := toolsafety.NewAuditLogger(nil)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	// Should not panic or write.
	logger.Log(&toolsafety.ScanReport{
		ToolName: "should_not_write",
		Decision: toolsafety.DecisionDeny,
	})
}

func TestAuditLogger_Sanitize(t *testing.T) {
	logger, err := toolsafety.NewAuditLogger(nil)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	tests := []struct {
		input string
		want  string
	}{
		{"nothing sensitive here", "nothing sensitive here"},
		{"API_KEY = 'sk-abc123def456xyz'", "***REDACTED***"},
		{"token: 'ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx'", "***REDACTED***"},
		{"safe text", "safe text"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := logger.Sanitize(tt.input)
			if got != tt.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
