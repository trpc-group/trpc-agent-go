//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides a configurable pre-execution safety scanner for
// tool calls that execute commands, scripts, or open-ended tool arguments.
package safety

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// Decision is the scanner's original safety decision.
type Decision string

// Safety decisions.
const (
	DecisionAllow            Decision = "allow"
	DecisionDeny             Decision = "deny"
	DecisionAsk              Decision = "ask"
	DecisionNeedsHumanReview Decision = "needs_human_review"
)

// Valid reports whether d is a supported decision.
func (d Decision) Valid() bool {
	switch d {
	case DecisionAllow, DecisionDeny, DecisionAsk, DecisionNeedsHumanReview:
		return true
	default:
		return false
	}
}

// RiskLevel is the normalized severity vocabulary for findings and reports.
type RiskLevel string

// Risk levels.
const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// Valid reports whether r is a supported risk level.
func (r RiskLevel) Valid() bool {
	switch r {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	default:
		return false
	}
}

// Backend identifies the execution surface being scanned.
type Backend string

// Backend values.
const (
	BackendWorkspace Backend = "workspace"
	BackendHost      Backend = "host"
	BackendCodeExec  Backend = "codeexec"
	BackendSandbox   Backend = "sandbox"
	BackendUnknown   Backend = "unknown"
)

// Valid reports whether b is a supported backend.
func (b Backend) Valid() bool {
	switch b {
	case BackendWorkspace, BackendHost, BackendCodeExec, BackendSandbox, BackendUnknown:
		return true
	default:
		return false
	}
}

// ScanRequest describes one pending tool call or script execution.
type ScanRequest struct {
	ToolName     string            `json:"tool_name"`
	ToolCallID   string            `json:"tool_call_id,omitempty"`
	Backend      Backend           `json:"backend"`
	Command      string            `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Cwd          string            `json:"cwd,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Stdin        string            `json:"stdin,omitempty"`
	TimeoutSec   int               `json:"timeout_sec,omitempty"`
	Background   bool              `json:"background,omitempty"`
	TTY          bool              `json:"tty,omitempty"`
	Language     string            `json:"language,omitempty"`
	Code         string            `json:"code,omitempty"`
	RawArguments []byte            `json:"-"`
	Metadata     map[string]any    `json:"metadata,omitempty"`
}

// Finding describes one scanner finding.
type Finding struct {
	RuleID         string    `json:"rule_id"`
	RiskLevel      RiskLevel `json:"risk_level"`
	Decision       Decision  `json:"decision"`
	Evidence       string    `json:"evidence,omitempty"`
	Recommendation string    `json:"recommendation"`
	Redacted       bool      `json:"redacted,omitempty"`
}

// Report is the structured safety scan result.
type Report struct {
	ToolName       string        `json:"tool_name"`
	ToolCallID     string        `json:"tool_call_id,omitempty"`
	Backend        Backend       `json:"backend"`
	Command        string        `json:"command,omitempty"`
	Decision       Decision      `json:"decision"`
	RiskLevel      RiskLevel     `json:"risk_level"`
	RuleID         string        `json:"rule_id,omitempty"`
	Evidence       string        `json:"evidence,omitempty"`
	Recommendation string        `json:"recommendation,omitempty"`
	Blocked        bool          `json:"blocked"`
	Redacted       bool          `json:"redacted"`
	Findings       []Finding     `json:"findings,omitempty"`
	Duration       time.Duration `json:"-"`
	DurationMS     int64         `json:"duration_ms"`
	AuditError     string        `json:"audit_error,omitempty"`
}

// ToolSafetyAttributes returns short OpenTelemetry attributes for the report.
func (r Report) ToolSafetyAttributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("tool.safety.decision", string(r.Decision)),
		attribute.String("tool.safety.risk_level", string(r.RiskLevel)),
		attribute.String("tool.safety.rule_id", r.RuleID),
		attribute.String("tool.safety.backend", string(r.Backend)),
		attribute.Bool("tool.safety.blocked", r.Blocked),
		attribute.Bool("tool.safety.redacted", r.Redacted),
	}
}

// Scanner scans one request and returns a structured report.
type Scanner interface {
	Scan(ctx context.Context, req ScanRequest) (Report, error)
}

// ScannerFunc adapts a function to Scanner.
type ScannerFunc func(context.Context, ScanRequest) (Report, error)

// Scan implements Scanner.
func (f ScannerFunc) Scan(ctx context.Context, req ScanRequest) (Report, error) {
	return f(ctx, req)
}
