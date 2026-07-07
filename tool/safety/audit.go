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
	"errors"
	"io"
	"sync"
	"time"
)

// AuditEvent is one JSONL-safe audit record.
type AuditEvent struct {
	Time             time.Time `json:"time"`
	ToolName         string    `json:"tool_name"`
	ToolCallID       string    `json:"tool_call_id,omitempty"`
	Backend          Backend   `json:"backend"`
	Decision         Decision  `json:"decision"`
	PermissionAction string    `json:"permission_action"`
	RiskLevel        RiskLevel `json:"risk_level"`
	RuleID           string    `json:"rule_id,omitempty"`
	DurationMS       int64     `json:"duration_ms"`
	Blocked          bool      `json:"blocked"`
	Redacted         bool      `json:"redacted"`
	Recommendation   string    `json:"recommendation,omitempty"`
}

// AuditWriter writes one safety audit event.
type AuditWriter interface {
	WriteAuditEvent(context.Context, AuditEvent) error
}

// JSONLAuditWriter writes one audit event per line.
type JSONLAuditWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONLAuditWriter creates a JSONL audit writer.
func NewJSONLAuditWriter(w io.Writer) *JSONLAuditWriter {
	return &JSONLAuditWriter{w: w}
}

// WriteAuditEvent writes one event as JSON plus a newline.
func (w *JSONLAuditWriter) WriteAuditEvent(
	ctx context.Context,
	ev AuditEvent,
) error {
	if w == nil || w.w == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.w.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}

func auditEventFromReport(report Report) AuditEvent {
	return AuditEvent{
		Time:             time.Now().UTC(),
		ToolName:         report.ToolName,
		ToolCallID:       report.ToolCallID,
		Backend:          report.Backend,
		Decision:         report.Decision,
		PermissionAction: permissionAction(report.Decision),
		RiskLevel:        report.RiskLevel,
		RuleID:           report.RuleID,
		DurationMS:       report.DurationMS,
		Blocked:          report.Blocked,
		Redacted:         report.Redacted,
		Recommendation:   report.Recommendation,
	}
}

type failingAuditWriter struct {
	err error
}

func (w failingAuditWriter) WriteAuditEvent(context.Context, AuditEvent) error {
	if w.err != nil {
		return w.err
	}
	return errors.New("audit write failed")
}
