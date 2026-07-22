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
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

const (
	auditPhasePrecheck  = "precheck"
	auditPhasePostcheck = "postcheck"
	auditFileMode       = os.FileMode(0o600)
)

// AuditEvent is the low-cardinality record emitted for one safety decision.
type AuditEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	Phase      string    `json:"phase"`
	ToolName   string    `json:"tool_name"`
	Backend    Backend   `json:"backend"`
	Decision   Decision  `json:"decision"`
	RiskLevel  RiskLevel `json:"risk_level"`
	RuleID     string    `json:"rule_id"`
	DurationMS int64     `json:"duration_ms"`
	Redacted   bool      `json:"redacted"`
	Blocked    bool      `json:"blocked"`
}

// Auditor records safety decisions before they leave the safety package.
type Auditor interface {
	Record(ctx context.Context, event AuditEvent) error
}

// WithAuditor configures synchronous audit recording for a Guard.
func WithAuditor(auditor Auditor) Option {
	return func(options *guardOptions) error {
		if isNilAuditor(auditor) {
			return errors.New("nil auditor")
		}
		options.auditor = auditor
		return nil
	}
}

func isNilAuditor(auditor Auditor) bool {
	return isNilInterface(auditor)
}

// JSONLAuditor appends one complete JSON object per line.
type JSONLAuditor struct {
	mu      sync.Mutex
	file    *os.File
	encoder *json.Encoder
	closed  bool
}

// NewJSONLAuditor opens path for append with owner-only permissions.
func NewJSONLAuditor(path string) (*JSONLAuditor, error) {
	if path == "" {
		return nil, errors.New("tool safety: audit path is empty")
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, auditFileMode)
	if err != nil {
		return nil, fmt.Errorf("tool safety: open audit file: %w", err)
	}
	if err := file.Chmod(auditFileMode); err != nil {
		secureErr := fmt.Errorf("tool safety: secure audit file: %w", err)
		if closeErr := file.Close(); closeErr != nil {
			return nil, errors.Join(
				secureErr,
				fmt.Errorf("tool safety: close insecure audit file: %w", closeErr),
			)
		}
		return nil, secureErr
	}
	return &JSONLAuditor{
		file:    file,
		encoder: json.NewEncoder(file),
	}, nil
}

// Record appends one event. Calls are serialized to preserve JSONL records.
func (auditor *JSONLAuditor) Record(_ context.Context, event AuditEvent) error {
	if auditor == nil {
		return errors.New("tool safety: nil JSONL auditor")
	}
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	if auditor.closed || auditor.file == nil || auditor.encoder == nil {
		return errors.New("tool safety: JSONL auditor is closed")
	}
	if err := auditor.encoder.Encode(event); err != nil {
		return fmt.Errorf("tool safety: write audit event: %w", err)
	}
	return nil
}

// Close flushes operating-system file state and closes the audit file.
func (auditor *JSONLAuditor) Close() error {
	if auditor == nil {
		return errors.New("tool safety: nil JSONL auditor")
	}
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	if auditor.closed {
		return errors.New("tool safety: JSONL auditor is closed")
	}
	auditor.closed = true
	if err := auditor.file.Close(); err != nil {
		return fmt.Errorf("tool safety: close audit file: %w", err)
	}
	return nil
}

func auditEventFromReport(report Report, phase string) AuditEvent {
	return AuditEvent{
		Timestamp:  time.Now().UTC(),
		Phase:      phase,
		ToolName:   report.ToolName,
		Backend:    report.Backend,
		Decision:   report.Decision,
		RiskLevel:  report.RiskLevel,
		RuleID:     report.RuleID,
		DurationMS: report.DurationMS,
		Redacted:   report.Redacted,
		Blocked:    report.Blocked,
	}
}

func auditFailureReport(report Report, blocked bool) Report {
	finding := newFinding(
		"AUDIT_WRITE_FAILED",
		RiskLevelHigh,
		DecisionDeny,
		"safety audit event could not be recorded",
		"restore the configured auditor before tool execution",
	)
	report.Decision = finding.Decision
	report.RiskLevel = finding.RiskLevel
	report.RuleID = finding.RuleID
	report.Evidence = finding.Evidence
	report.Recommendation = finding.Recommendation
	report.Blocked = blocked
	report.Findings = []Finding{finding}
	return redactReport(report)
}

func (guard *Guard) finalizeReport(
	ctx context.Context,
	report Report,
	phase string,
) (Report, error) {
	report = redactReport(report)
	if guard.auditor == nil {
		return report, nil
	}
	if err := guard.auditor.Record(ctx, auditEventFromReport(report, phase)); err != nil {
		return auditFailureReport(report, phase == auditPhasePrecheck), fmt.Errorf(
			"tool safety: record audit event: %w",
			err,
		)
	}
	return report, nil
}
