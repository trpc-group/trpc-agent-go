// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety

import (
	"encoding/json"
	"time"
)

// RiskLevel is the severity level of a safety finding.
type RiskLevel string

const (
	// RiskLevelNone indicates no risk.
	RiskLevelNone RiskLevel = "none"
	// RiskLevelLow indicates low severity.
	RiskLevelLow RiskLevel = "low"
	// RiskLevelMedium indicates medium severity.
	RiskLevelMedium RiskLevel = "medium"
	// RiskLevelHigh indicates high severity.
	RiskLevelHigh RiskLevel = "high"
	// RiskLevelCritical indicates critical severity.
	RiskLevelCritical RiskLevel = "critical"
)

// Decision is the result of a safety check.
type Decision string

const (
	// DecisionAllow permits execution.
	DecisionAllow Decision = "allow"
	// DecisionDeny blocks execution.
	DecisionDeny Decision = "deny"
	// DecisionAsk requests human approval.
	DecisionAsk Decision = "ask"
)

// RiskFinding describes one safety finding from a single rule.
type RiskFinding struct {
	RuleID         RuleID          `json:"rule_id"`
	RiskLevel      RiskLevel       `json:"risk_level"`
	Evidence       string          `json:"evidence"`
	Recommendation string          `json:"recommendation"`
	SeverityScore  int             `json:"severity_score"`
	MatchedPattern string          `json:"matched_pattern,omitempty"`
	Context        json.RawMessage `json:"context,omitempty"`
}

// ScanReport is the complete structured output of a safety scan.
type ScanReport struct {
	ToolName    string        `json:"tool_name"`
	Command     string        `json:"command"`
	Backend     string        `json:"backend"`
	Decision    Decision      `json:"decision"`
	RiskLevel   RiskLevel     `json:"risk_level"`
	Findings    []RiskFinding `json:"findings"`
	IsShellSafe bool          `json:"is_shell_safe"`
	Duration    time.Duration `json:"duration_ms"`
	Intercepted bool          `json:"intercepted"`
	Sanitized   bool          `json:"sanitized"`
	Timestamp   time.Time     `json:"timestamp"`
}

// String returns a human-readable summary of the report.
func (r *ScanReport) String() string {
	return r.ToolName + ": " + string(r.Decision) + " (" + string(r.RiskLevel) + ")"
}

// ToJSON serializes the report to indented JSON bytes.
func (r *ScanReport) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// FormatFinding returns a short string from the first finding's evidence.
func FormatFinding(r *ScanReport) string {
	if len(r.Findings) == 0 {
		return "no findings"
	}
	return r.Findings[0].Evidence
}

// HighestRiskLevel returns the highest risk level among all findings.
func HighestRiskLevel(findings []RiskFinding) RiskLevel {
	levels := []RiskLevel{RiskLevelNone, RiskLevelLow, RiskLevelMedium, RiskLevelHigh, RiskLevelCritical}
	for i := len(levels) - 1; i >= 0; i-- {
		for _, f := range findings {
			if f.RiskLevel == levels[i] {
				return levels[i]
			}
		}
	}
	return RiskLevelNone
}

// ScanRequest describes a command or script to be scanned.
type ScanRequest struct {
	ToolName  string
	Command   string
	Backend   string
	TimeoutS  int
	OutputMax int64
}
