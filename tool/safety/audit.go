// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// AuditWriter records safety audit events.
type AuditWriter interface {
	WriteAuditEvent(ctx context.Context, ev AuditEvent) error
}

// JSONLWriter appends audit events as one JSON object per line.
type JSONLWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONLWriter creates an audit writer around w.
func NewJSONLWriter(w io.Writer) *JSONLWriter {
	return &JSONLWriter{w: w}
}

// NewJSONLFileWriter opens path for append-only JSONL audit output.
func NewJSONLFileWriter(path string) (*JSONLWriter, func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return NewJSONLWriter(f), f.Close, nil
}

// WriteAuditEvent writes one audit event.
func (w *JSONLWriter) WriteAuditEvent(_ context.Context, ev AuditEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := w.w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func auditEventFromReport(now time.Time, report Report) AuditEvent {
	return AuditEvent{
		Timestamp:   now.UTC(),
		RequestID:   report.RequestID,
		ToolName:    report.ToolName,
		Backend:     report.Backend,
		Decision:    report.Decision,
		RiskLevel:   report.RiskLevel,
		RuleID:      primaryRuleID(report.RuleIDs),
		AllRuleIDs:  report.RuleIDs,
		DurationMS:  report.DurationMS,
		Blocked:     report.Blocked,
		Redacted:    report.Redacted,
		CommandHash: hashIfNotEmpty(report.Command),
		Summary:     report.Recommendation,
	}
}

func hashIfNotEmpty(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
