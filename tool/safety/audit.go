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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"
)

// AuditEvent is the stable JSONL record emitted for each guarded decision. It
// deliberately stores a command digest instead of command text.
type AuditEvent struct {
	Timestamp      time.Time `json:"timestamp"`
	ToolName       string    `json:"tool_name"`
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id"`
	Backend        Backend   `json:"backend"`
	DurationMicros int64     `json:"duration_us"`
	Redacted       bool      `json:"redacted"`
	Blocked        bool      `json:"blocked"`
	CommandSHA256  string    `json:"command_sha256,omitempty"`
}

// Auditor consumes structured safety events. Implementations must expect
// concurrent calls and honor context cancellation while waiting on external
// sinks.
type Auditor interface {
	Record(context.Context, AuditEvent) error
}

// JSONLAuditor serializes one AuditEvent per line to an io.Writer.
type JSONLAuditor struct {
	lock    chan struct{}
	encoder *json.Encoder
}

// NewJSONLAuditor creates a concurrency-safe JSONL auditor.
func NewJSONLAuditor(writer io.Writer) *JSONLAuditor {
	if writer == nil {
		return &JSONLAuditor{}
	}
	lock := make(chan struct{}, 1)
	lock <- struct{}{}
	return &JSONLAuditor{
		lock:    lock,
		encoder: json.NewEncoder(writer),
	}
}

// Record implements Auditor.
func (a *JSONLAuditor) Record(
	ctx context.Context,
	event AuditEvent,
) error {
	if a == nil || a.encoder == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.lock:
	}
	defer func() {
		a.lock <- struct{}{}
	}()
	return a.encoder.Encode(event)
}

func auditEvent(report Report) AuditEvent {
	event := AuditEvent{
		Timestamp:      time.Now().UTC(),
		ToolName:       report.ToolName,
		Decision:       report.Decision,
		RiskLevel:      report.RiskLevel,
		RuleID:         report.RuleID,
		Backend:        report.Backend,
		DurationMicros: report.DurationMicros,
		Redacted:       report.Redacted,
		Blocked:        report.Blocked,
	}
	if report.Command != "" {
		sum := sha256.Sum256([]byte(report.Command))
		event.CommandSHA256 = hex.EncodeToString(sum[:])
	}
	return event
}
