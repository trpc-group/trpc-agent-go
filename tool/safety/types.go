//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides a Tool Execution Safety Guard that scans
// commands and code before execution, producing an allow / deny / ask
// decision based on a configurable policy.
//
// The scanner sits on top of the existing shellsafe parser (structural
// validation) and envscrub (environment scrubbing) layers, adding
// semantic risk assessment: dangerous commands, network egress to
// non-whitelisted hosts, shell-wrapper bypass attempts, hostexec
// session risks, dependency installation, resource abuse, and
// sensitive-information leakage.
//
// The scanner is designed to be plugged into the framework's
// PermissionPolicy extension point so that high-risk tool calls are
// blocked (or sent for human review) before execution, while every
// decision is recorded as a structured audit event and OpenTelemetry
// span attributes.
package safety

import "time"

// Verdict is the outcome of a safety scan.  Named Verdict (not Decision)
// to avoid collision with Decision types defined in other agent-related
// packages; this mirrors the choice in tool.PermissionDecision, which uses
// Action for the same reason.
type Verdict string

const (
	// VerdictAllow means the command is safe to execute.
	VerdictAllow Verdict = "allow"
	// VerdictDeny means the command must not execute.
	VerdictDeny Verdict = "deny"
	// VerdictAsk means the command needs human review before execution.
	VerdictAsk Verdict = "ask"
)

// RiskLevel classifies the severity of a detected risk.
type RiskLevel string

const (
	// RiskLow indicates a minor concern that does not block execution.
	RiskLow RiskLevel = "low"
	// RiskMedium indicates a moderate concern that should be reviewed.
	RiskMedium RiskLevel = "medium"
	// RiskHigh indicates a serious concern that normally requires review.
	RiskHigh RiskLevel = "high"
	// RiskCritical indicates an immediate danger that must be blocked.
	RiskCritical RiskLevel = "critical"
)

// Backend identifies the execution backend that a command targets.
type Backend string

const (
	// BackendWorkspaceExec is the workspaceexec backend.
	BackendWorkspaceExec Backend = "workspaceexec"
	// BackendHostExec is the hostexec backend.
	BackendHostExec Backend = "hostexec"
	// BackendCodeExec is the codeexec backend.
	BackendCodeExec Backend = "codeexec"
)

// Risk describes a single issue found by a rule.
type Risk struct {
	// RuleID is the unique identifier of the rule that fired.
	RuleID string `json:"rule_id"`
	// RuleName is a human-readable name for the rule.
	RuleName string `json:"rule_name"`
	// Level is the severity of this risk.
	Level RiskLevel `json:"level"`
	// Evidence is the concrete snippet that triggered the rule.
	Evidence string `json:"evidence"`
	// Suggestion is a recommended remediation.
	Suggestion string `json:"suggestion"`
	// ShouldBlock indicates whether this risk alone justifies denial.
	ShouldBlock bool `json:"should_block"`
}

// ScanRequest is the input to a safety scan.
type ScanRequest struct {
	// ToolName is the model-visible tool name (e.g. "workspace_exec").
	ToolName string
	// Command is the shell command or code to scan.
	Command string
	// Backend identifies the execution backend.
	Backend Backend
	// Language is the code language (only for codeexec).
	Language string
}

// ScanReport is the complete result of a safety scan.
type ScanReport struct {
	// Timestamp is when the scan completed.
	Timestamp time.Time `json:"timestamp"`
	// ToolName is the scanned tool name.
	ToolName string `json:"tool_name"`
	// Command is the scanned command (redacted in audit logs).
	Command string `json:"command"`
	// Backend is the execution backend.
	Backend Backend `json:"backend"`
	// Language is the code language, if applicable.
	Language string `json:"language,omitempty"`
	// Verdict is the final allow / deny / ask outcome.  Callers that
	// need a boolean "is the command blocked" predicate should compare
	// against VerdictDeny; we deliberately do not store a derived bool
	// to avoid a second source of truth.
	Verdict Verdict `json:"verdict"`
	// RiskLevel is the highest risk level detected (or low if none).
	RiskLevel RiskLevel `json:"risk_level"`
	// Risks lists every risk found by the rules.
	Risks []Risk `json:"risks"`
	// Recommendation is a human-readable summary and remediation advice.
	Recommendation string `json:"recommendation"`
}
