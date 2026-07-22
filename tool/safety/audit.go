//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// AuditEvent is one line in the audit log, written as JSON.
type AuditEvent struct {
	Timestamp    time.Time `json:"timestamp"`
	ToolName     string    `json:"tool_name"`
	Backend      string    `json:"backend"`
	Command      string    `json:"command"`
	Verdict      string    `json:"verdict"`
	RiskLevel    string    `json:"risk_level"`
	RiskCount    int       `json:"risk_count"`
	RuleIDs      []string  `json:"rule_ids"`
	DurationMs   int64     `json:"duration_ms"`
	Redacted     bool      `json:"redacted"`
	SessionID    string    `json:"session_id,omitempty"`
	InvocationID string    `json:"invocation_id,omitempty"`
}

// AuditLogger writes AuditEvents as JSON Lines to a file.
type AuditLogger struct {
	mu       sync.Mutex
	file     *os.File
	encoder  *json.Encoder
	redactor *Redactor
}

// NewAuditLogger opens path for appending (creating it if needed)
// and returns a ready-to-use AuditLogger.  The file is opened with
// 0600 permissions to protect potentially sensitive audit data.
func NewAuditLogger(path string, patterns []string) (*AuditLogger, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("safety: open audit log: %w", err)
	}
	return &AuditLogger{
		file:     file,
		encoder:  json.NewEncoder(file),
		redactor: NewRedactor(patterns),
	}, nil
}

// Log writes a single AuditEvent derived from report and duration.
// The command is redacted before writing.  A non-nil error from Log
// does not prevent the tool from executing — callers should log the
// error but continue.
func (l *AuditLogger) Log(ctx context.Context, report *ScanReport, duration time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	redacted := l.redactor.Redact(report.Command)
	wasRedacted := redacted != report.Command

	ruleIDs := make([]string, len(report.Risks))
	for i, risk := range report.Risks {
		ruleIDs[i] = risk.RuleID
	}

	sessionID, _ := ctx.Value(sessionIDKey{}).(string)
	invocationID, _ := ctx.Value(invocationIDKey{}).(string)

	event := AuditEvent{
		Timestamp:    time.Now(),
		ToolName:     report.ToolName,
		Backend:      string(report.Backend),
		Command:      redacted,
		Verdict:      string(report.Verdict),
		RiskLevel:    string(report.RiskLevel),
		RiskCount:    len(report.Risks),
		RuleIDs:      ruleIDs,
		DurationMs:   duration.Milliseconds(),
		Redacted:     wasRedacted,
		SessionID:    sessionID,
		InvocationID: invocationID,
	}

	if err := l.encoder.Encode(event); err != nil {
		return fmt.Errorf("safety: encode audit event: %w", err)
	}
	return nil
}

// Close flushes and closes the underlying file.
func (l *AuditLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// sessionIDKey and invocationIDKey are context value key types used
// by AuditLogger.Log to extract session and invocation identifiers.
type sessionIDKey struct{}
type invocationIDKey struct{}

// WithSessionID returns a new context that carries sessionID so
// AuditLogger.Log can include it in audit events.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, sessionID)
}

// WithInvocationID returns a new context that carries invocationID.
func WithInvocationID(ctx context.Context, invocationID string) context.Context {
	return context.WithValue(ctx, invocationIDKey{}, invocationID)
}
