//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewRedactor_InvalidRegex_ReturnsError verifies that constructing
// a Redactor with an uncompileable regex pattern fails loudly instead
// of silently degrading into a no-op redactor that would let every
// secret through unredacted.
func TestNewRedactor_InvalidRegex_ReturnsError(t *testing.T) {
	// "[" is an unterminated character class — RE2 rejects it.
	r, err := NewRedactor([]string{"["})
	if err == nil {
		t.Fatal("NewRedactor with an invalid regex must return a " +
			"non-nil error, not silently fall back to a no-op redactor")
	}
	if r != nil {
		t.Errorf("NewRedactor with an invalid regex must return a nil " +
			"redactor so callers cannot accidentally use a broken one")
	}
}

// TestNewAuditLogger_InvalidRegex_ReturnsError verifies that an
// invalid redaction pattern surfaces as an error from NewAuditLogger
// rather than succeeding with a no-op redactor that would persist
// sensitive commands in clear text.  This guards the audit-logger
// seam against a regression that swallows the redactor error.
func TestNewAuditLogger_InvalidRegex_ReturnsError(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "audit.jsonl")

	// "[" is an unterminated character class — RE2 rejects it.
	logger, err := NewAuditLogger(logPath, []string{"["})
	if err == nil {
		if logger != nil {
			_ = logger.Close()
		}
		t.Fatal("NewAuditLogger with an invalid regex must return a " +
			"non-nil error, not succeed with a no-op redactor")
	}
	if logger != nil {
		t.Errorf("NewAuditLogger with an invalid regex must return a " +
			"nil logger so callers cannot use one with a broken redactor")
	}
}

// TestNewAuditLogger_ValidRegex_RedactsEndToEnd verifies that a valid
// custom redaction pattern is honored all the way through to the
// persisted audit record: the matched secret must appear as
// "[REDACTED]" in the JSONL event, not in clear text.  This guards
// against an over-broad fix that errors on valid patterns or returns
// a silently no-op redactor.
func TestNewAuditLogger_ValidRegex_RedactsEndToEnd(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "audit.jsonl")

	// "AKIA" + exactly 16 alphanumerics, so the whole token is matched
	// and redacted by the pattern below.
	const secret = "AKIAEXAMPLESECRET012"
	pattern := `AKIA[A-Z0-9]{16}`

	logger, err := NewAuditLogger(logPath, []string{pattern})
	if err != nil {
		t.Fatalf("NewAuditLogger with a valid regex must succeed: %v", err)
	}
	defer logger.Close() //nolint:errcheck

	report := &ScanReport{
		ToolName: "workspace_exec",
		Command:  "export AWS_KEY=" + secret,
		Backend:  BackendWorkspaceExec,
		Verdict:  VerdictDeny,
	}
	if err := logger.Log(context.Background(), report, 0); err != nil {
		t.Fatalf("Log: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}

	var event AuditEvent
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("unmarshal audit event: %v", err)
	}

	const wantCommand = "export AWS_KEY=[REDACTED]"
	if got := event.Command; got != wantCommand {
		t.Errorf("redacted command = %q, want %q (secret must not leak)", got, wantCommand)
	}
	if !event.Redacted {
		t.Errorf("event.Redacted = false, want true (pattern matched)")
	}
	// Guard against the secret leaking anywhere in the persisted line.
	if strings.Contains(string(data), secret) {
		t.Errorf("secret leaked into audit log; raw line: %s", data)
	}
}
