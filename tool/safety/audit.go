//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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
	"regexp"
	"sync"
	"time"
)

// AuditEvent is the structured record written for every safety decision.
type AuditEvent struct {
	Timestamp    string    `json:"timestamp"`
	TraceID      string    `json:"trace_id,omitempty"`
	Decision     Decision  `json:"decision"`
	RiskLevel    RiskLevel `json:"risk_level"`
	RuleID       string    `json:"rule_id"`
	ToolName     string    `json:"tool_name"`
	Command      string    `json:"command"`
	Backend      string    `json:"backend"`
	Blocked      bool      `json:"blocked"`
	DurationMs   int64     `json:"duration_ms"`
	Desensitized bool      `json:"desensitized"`
	Evidence     string    `json:"evidence,omitempty"`
}

// AuditLogger writes structured audit events for every safety decision.
type AuditLogger interface {
	Log(ctx context.Context, report *SafetyReport) error
	Close() error
}

// JSONLAuditLogger writes audit events as JSONL to a file.
type JSONLAuditLogger struct {
	mu       sync.Mutex
	file     *os.File
	patterns []*regexp.Regexp
}

// NewJSONLAuditLogger opens the audit file for append-only writing.
// It returns an error if policy is nil — a policy with secret-detection
// patterns is required for safe audit logging.
func NewJSONLAuditLogger(path string, policy *Policy) (*JSONLAuditLogger, error) {
	if policy == nil {
		return nil, fmt.Errorf("audit: policy must not be nil")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	return &JSONLAuditLogger{
		file:     f,
		patterns: policy.SecretRegexps(),
	}, nil
}

// Log writes a single audit event. Both Command and Evidence are
// desensitized before writing. If context carries a trace ID
// (via the standard "trace_id" key or OTel span context), it is
// recorded in the audit event.
func (l *JSONLAuditLogger) Log(ctx context.Context, report *SafetyReport) error {
	cmd := Desensitize(report.Command, l.patterns)
	ev := Desensitize(report.Evidence, l.patterns)

	traceID := traceIDFromContext(ctx)

	event := AuditEvent{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		TraceID:      traceID,
		Decision:     report.Decision,
		RiskLevel:    report.RiskLevel,
		RuleID:       report.RuleID,
		ToolName:     report.ToolName,
		Command:      cmd,
		Backend:      report.Backend,
		Blocked:      report.Blocked,
		DurationMs:   report.DurationMs,
		Evidence:     ev,
		Desensitized: cmd != report.Command || ev != report.Evidence,
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	enc := json.NewEncoder(l.file)
	return enc.Encode(event)
}

// Close flushes and closes the audit file.
func (l *JSONLAuditLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// traceIDFromContext extracts a trace identifier set via WithTraceID.
func traceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v := ctx.Value(TraceIDKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// TraceIDKey is the context key set by WithTraceID for audit logging.
// It uses a private type to avoid collisions with other packages' keys.
type contextKey string

// TraceIDKey is the context key for trace identifiers in audit logging.
const TraceIDKey contextKey = "trace_id"

// WithTraceID returns a context carrying a trace identifier for audit logging.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, TraceIDKey, id)
}
