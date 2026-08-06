//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolsafety

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Decision is the scan outcome: allow execution, deny it, or ask for
// human review.
type Decision string

const (
	// DecisionAllow allows the tool execution to proceed.
	DecisionAllow Decision = "allow"
	// DecisionDeny blocks the tool execution.
	DecisionDeny Decision = "deny"
	// DecisionAsk requires human review before execution.
	DecisionAsk Decision = "ask"
)

// RiskLevel indicates the severity of a finding.
type RiskLevel string

const (
	// RiskCritical indicates a critical safety risk.
	RiskCritical RiskLevel = "critical"
	// RiskHigh indicates a high safety risk.
	RiskHigh RiskLevel = "high"
	// RiskMedium indicates a medium safety risk.
	RiskMedium RiskLevel = "medium"
	// RiskLow indicates a low safety risk.
	RiskLow RiskLevel = "low"
	// RiskNone indicates that no safety risk was found.
	RiskNone RiskLevel = "none"
)

// Category labels for findings.
const (
	// CatDangerousCmd identifies dangerous command findings.
	CatDangerousCmd = "dangerous_cmd"
	// CatNetwork identifies network access findings.
	CatNetwork = "network"
	// CatShellBypass identifies shell bypass findings.
	CatShellBypass = "shell_bypass"
	// CatHostRisk identifies host environment risk findings.
	CatHostRisk = "host_risk"
	// CatInstall identifies dependency installation findings.
	CatInstall = "dependency_install"
	// CatResource identifies resource abuse findings.
	CatResource = "resource_abuse"
	// CatSensitive identifies sensitive data leakage findings.
	CatSensitive = "sensitive_leak"
)

// ScanReport is the structured output produced after scanning a
// command for safety risks.
type ScanReport struct {
	Decision    Decision      `json:"decision"`
	RiskLevel   RiskLevel     `json:"risk_level"`
	ToolName    string        `json:"tool_name"`
	Backend     string        `json:"backend"`
	Command     string        `json:"command"`
	CommandArgs []string      `json:"command_args,omitempty"`
	WorkDir     string        `json:"work_dir,omitempty"`
	EnvKeys     []string      `json:"env_keys,omitempty"`
	Findings    []RuleFinding `json:"findings"`
	Intercepted bool          `json:"intercepted"`
	DurationMs  int64         `json:"duration_ms"`
	Timestamp   time.Time     `json:"timestamp"`
}

// RuleFinding describes a single risk detected during scanning.
type RuleFinding struct {
	RuleID         string    `json:"rule_id"`
	RiskLevel      RiskLevel `json:"risk_level"`
	Category       string    `json:"category"`
	Evidence       string    `json:"evidence"`
	Recommendation string    `json:"recommendation"`
}

// AuditEvent is a machine-readable event written to a JSONL audit
// log for downstream monitoring and SIEM consumption.
type AuditEvent struct {
	Timestamp    time.Time `json:"timestamp"`
	ToolName     string    `json:"tool_name"`
	Backend      string    `json:"backend"`
	Command      string    `json:"command"`
	Decision     Decision  `json:"decision"`
	RiskLevel    RiskLevel `json:"risk_level"`
	RuleIDs      []string  `json:"rule_ids"`
	Intercepted  bool      `json:"intercepted"`
	DurationMs   int64     `json:"duration_ms"`
	Sanitized    bool      `json:"sanitized"`
	SessionID    string    `json:"session_id,omitempty"`
	InvocationID string    `json:"invocation_id,omitempty"`
}

// highestRisk returns the most severe risk level among a list.
// Returns RiskNone when called with no levels.
func highestRisk(levels ...RiskLevel) RiskLevel {
	worst := RiskNone
	for _, l := range levels {
		if riskOrder(l) > riskOrder(worst) {
			worst = l
		}
	}
	return worst
}

func riskOrder(r RiskLevel) int {
	switch r {
	case RiskCritical:
		return 4
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	default:
		return 0
	}
}

// AuditWriter writes audit events in JSONL format to an io.Writer.
// It is safe for concurrent use when the underlying writer is safe.
type AuditWriter struct {
	w io.Writer
}

// NewAuditWriter creates an AuditWriter that appends JSONL events to w.
func NewAuditWriter(w io.Writer) *AuditWriter {
	return &AuditWriter{w: w}
}

// Write writes an audit event as a single JSON line.
func (aw *AuditWriter) Write(event AuditEvent) error {
	if aw == nil || aw.w == nil {
		return nil
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal audit event: %w", err)
	}
	data = append(data, '\n')
	_, err = aw.w.Write(data)
	return err
}
