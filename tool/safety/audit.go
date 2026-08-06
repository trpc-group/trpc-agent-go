//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// AuditRecord is one line of the tool_safety_audit.jsonl trail. It never
// contains plaintext secrets: it is derived from an already-redacted report.
type AuditRecord struct {
	Time       string    `json:"time,omitempty"`
	ToolName   string    `json:"tool_name"`
	Backend    Backend   `json:"backend"`
	Decision   Decision  `json:"decision"`
	RiskLevel  RiskLevel `json:"risk_level"`
	RuleID     string    `json:"rule_id,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	Redacted   bool      `json:"redacted"`
	Blocked    bool      `json:"blocked"`
}

// AuditRecordFrom builds an AuditRecord from a report. The time string is set
// by the writer, not here, so the builder stays deterministic and testable.
func AuditRecordFrom(report ScanReport) AuditRecord {
	return AuditRecord{
		ToolName:   report.ToolName,
		Backend:    report.Backend,
		Decision:   report.Decision,
		RiskLevel:  report.RiskLevel,
		RuleID:     report.PrimaryRuleID(),
		DurationMS: report.DurationMS,
		Redacted:   report.Redacted,
		Blocked:    report.Blocked,
	}
}

// AuditWriter appends JSONL audit records to an io.Writer. It is safe for
// concurrent use.
type AuditWriter struct {
	mu          sync.Mutex
	w           io.Writer
	now         func() time.Time
	includeTime bool
}

// AuditOption configures an AuditWriter.
type AuditOption func(*AuditWriter)

// WithAuditClock overrides the timestamp source (useful for deterministic
// output in tests and golden files).
func WithAuditClock(fn func() time.Time) AuditOption {
	return func(a *AuditWriter) {
		if fn != nil {
			a.now = fn
		}
	}
}

// WithoutTimestamp omits the time field entirely, yielding fully deterministic
// records for committed example outputs.
func WithoutTimestamp() AuditOption {
	return func(a *AuditWriter) { a.includeTime = false }
}

// NewAuditWriter returns an AuditWriter that writes to w. By default it stamps
// each record with an RFC3339 UTC time from time.Now.
func NewAuditWriter(w io.Writer, opts ...AuditOption) *AuditWriter {
	a := &AuditWriter{w: w, now: time.Now, includeTime: true}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	return a
}

// Record writes one audit line for report. A nil writer is a no-op.
func (a *AuditWriter) Record(report ScanReport) error {
	if a == nil || a.w == nil {
		return nil
	}
	rec := AuditRecordFrom(report)
	if a.includeTime {
		rec.Time = a.now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.w.Write(line); err != nil {
		return err
	}
	_, err = a.w.Write([]byte("\n"))
	return err
}
