//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides tool execution safety scanning, filter/
// permission interception and monitoring for trpc-agent-go.
//
// It builds on top of the shellsafe command parser and the tool.PermissionPolicy
// interface to add multi-dimensional risk scanning: dangerous commands,
// sensitive paths, network egress, host execution risks, resource abuse,
// secret leaks, and dependency installation — all configurable through a
// YAML/JSON policy file.
//
// This mechanism is a pre-execution guard, not a sandbox. It decides
// whether a command should run; the sandbox (container/e2b) limits what
// the command can do if it does run. The two are complementary, not
// interchangeable.
package safety

// Decision is the final verdict from a safety scan.
type Decision string

const (
	// DecisionAllow permits the tool call to execute.
	DecisionAllow Decision = "allow"
	// DecisionDeny blocks execution and returns the reason to the model.
	DecisionDeny Decision = "deny"
	// DecisionAsk pauses execution and requests human review.
	DecisionAsk Decision = "ask"
)

// RiskLevel indicates the severity of a finding.
type RiskLevel string

const (
	// RiskNone means no safety issue was detected.
	RiskNone RiskLevel = "none"
	// RiskLow is a minor concern that does not block execution.
	RiskLow RiskLevel = "low"
	// RiskMedium warrants an ask/human-review decision.
	RiskMedium RiskLevel = "medium"
	// RiskHigh is a serious risk deserving denial.
	RiskHigh RiskLevel = "high"
	// RiskCritical is an immediate threat; always denied.
	RiskCritical RiskLevel = "critical"
)

// riskPriority returns a numeric priority for RiskLevel comparison.
// Higher number = more severe.
func riskPriority(r RiskLevel) int {
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

// SafetyReport is the structured output of a safety scan.
// Every field required by the acceptance criteria is present:
// decision, risk_level, rule_id, evidence, recommendation,
// tool_name, command, backend, blocked.
type SafetyReport struct {
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id"`
	Evidence       string    `json:"evidence"`
	Recommendation string    `json:"recommendation"`
	ToolName       string    `json:"tool_name"`
	Command        string    `json:"command"`
	Backend        string    `json:"backend"`
	Blocked        bool      `json:"blocked"`
	DurationMs     int64     `json:"duration_ms"`
	Checkers       []string  `json:"checkers"`
}
