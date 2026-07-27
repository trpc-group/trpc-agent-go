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
