//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Decision is the safety decision returned after scanning.
type Decision string

const (
	// DecisionAllow means the tool call is safe to execute.
	DecisionAllow Decision = "allow"
	// DecisionDeny means the tool call must be blocked.
	DecisionDeny Decision = "deny"
	// DecisionAsk means the tool call requires explicit user approval.
	DecisionAsk Decision = "ask"
	// DecisionNeedsHumanReview means the tool call should be reviewed by a
	// human before execution.
	DecisionNeedsHumanReview Decision = "needs_human_review"
)

// RiskLevel represents the severity of a safety finding.
type RiskLevel string

const (
	// RiskLevelCritical indicates an immediate, severe security risk.
	RiskLevelCritical RiskLevel = "critical"
	// RiskLevelHigh indicates a significant security risk.
	RiskLevelHigh RiskLevel = "high"
	// RiskLevelMedium indicates a moderate security risk.
	RiskLevelMedium RiskLevel = "medium"
	// RiskLevelLow indicates a minor security concern.
	RiskLevelLow RiskLevel = "low"
	// RiskLevelInfo indicates an informational finding with no direct risk.
	RiskLevelInfo RiskLevel = "info"
)

// ScanInput is the input to the safety scanner.
type ScanInput struct {
	// Command is the shell command to scan (used by workspaceexec/hostexec).
	Command string
	// Stdin is additional stdin content that will be written before execution.
	Stdin string
	// CodeBlocks is the list of code blocks to scan (used by codeexec).
	CodeBlocks []string
	// Args are additional command-line arguments.
	Args []string
	// WorkDir is the working directory for the command.
	WorkDir string
	// Env is the environment variables for the command.
	Env map[string]string
	// ToolName is the name of the tool being scanned.
	ToolName string
	// Backend identifies the execution backend (workspaceexec, hostexec, codeexec).
	Backend string
	// Timeout is the requested execution timeout in seconds.
	Timeout int
	// Background reports whether the command runs in the background.
	Background bool
	// PTY reports whether a pseudo-terminal is requested.
	PTY bool
}

// Finding represents a single safety finding from a rule.
type Finding struct {
	// RuleID is the unique identifier of the rule that matched.
	RuleID string `json:"rule_id"`
	// RuleName is the human-readable name of the rule.
	RuleName string `json:"rule_name"`
	// RiskLevel is the severity of this finding.
	RiskLevel RiskLevel `json:"risk_level"`
	// Decision is the recommended action for this finding.
	Decision Decision `json:"decision"`
	// Evidence describes what was detected.
	Evidence string `json:"evidence"`
	// Recommendation suggests how to address the finding.
	Recommendation string `json:"recommendation"`
}

// ScanResult is the complete result of a safety scan.
type ScanResult struct {
	// Decision is the aggregated decision across all findings.
	// Priority: deny > ask > needs_human_review > allow
	Decision Decision `json:"decision"`
	// RiskLevel is the highest risk level across all findings.
	RiskLevel RiskLevel `json:"risk_level"`
	// Findings lists all findings from the scan.
	Findings []Finding `json:"findings"`
	// ToolName is the name of the tool that was scanned.
	ToolName string `json:"tool_name"`
	// Command is the scanned command (may be redacted).
	Command string `json:"command"`
	// Backend identifies the execution backend.
	Backend string `json:"backend"`
	// Intercepted reports whether execution was blocked.
	Intercepted bool `json:"intercepted"`
}

// Report is the structured scan report for output.
type Report struct {
	// Version is the report schema version.
	Version string `json:"version"`
	// GeneratedAt is the time the report was generated.
	GeneratedAt *time.Time `json:"generated_at,omitempty"`
	// Decision is the aggregated scan decision.
	Decision Decision `json:"decision"`
	// RiskLevel is the highest risk level.
	RiskLevel RiskLevel `json:"risk_level"`
	// Findings lists all findings.
	Findings []Finding `json:"findings"`
	// ToolName is the name of the tool.
	ToolName string `json:"tool_name"`
	// Command is the scanned command (may be redacted).
	Command string `json:"command"`
	// Backend identifies the execution backend.
	Backend string `json:"backend"`
	// Intercepted reports whether execution was blocked.
	Intercepted bool `json:"intercepted"`
}

// AuditEvent is a single audit event for JSONL output.
type AuditEvent struct {
	// Timestamp is the ISO 8601 time of the event.
	Timestamp string `json:"timestamp"`
	// ToolName is the name of the tool.
	ToolName string `json:"tool_name"`
	// Decision is the scan decision.
	Decision Decision `json:"decision"`
	// RiskLevel is the highest risk level.
	RiskLevel RiskLevel `json:"risk_level"`
	// RuleID is the identifier of the highest-severity matching rule.
	RuleID string `json:"rule_id"`
	// DurationMS is the scan duration in milliseconds.
	DurationMS int64 `json:"duration_ms"`
	// Redacted reports whether sensitive data was redacted.
	Redacted bool `json:"redacted"`
	// Intercepted reports whether execution was blocked.
	Intercepted bool `json:"intercepted"`
	// Backend identifies the execution backend.
	Backend string `json:"backend"`
}

// decisionOrder returns a numeric priority for a Decision.
// Lower values are higher priority (deny is most urgent).
func decisionOrder(d Decision) int {
	switch d {
	case DecisionDeny:
		return 0
	case DecisionAsk:
		return 1
	case DecisionNeedsHumanReview:
		return 2
	case DecisionAllow:
		return 3
	default:
		// Fail-closed: unrecognized decisions are treated as deny.
		return 0
	}
}

// riskLevelOrder returns a numeric order for RiskLevel comparison.
// Higher values are more severe.
func riskLevelOrder(r RiskLevel) int {
	switch r {
	case RiskLevelInfo:
		return 0
	case RiskLevelLow:
		return 1
	case RiskLevelMedium:
		return 2
	case RiskLevelHigh:
		return 3
	case RiskLevelCritical:
		return 4
	default:
		return 0
	}
}

// aggregateDecision returns the highest-priority Decision from findings.
// Priority: deny > ask > needs_human_review > allow.
// If no findings are present, DecisionAllow is returned.
// Unrecognized decision values are treated as deny (fail-closed).
func aggregateDecision(findings []Finding) Decision {
	result := DecisionAllow
	for _, f := range findings {
		if decisionOrder(f.Decision) < decisionOrder(result) {
			result = f.Decision
		}
	}
	return result
}

// aggregateRiskLevel returns the highest RiskLevel from findings.
// If no findings are present, RiskLevelInfo is returned.
func aggregateRiskLevel(findings []Finding) RiskLevel {
	result := RiskLevelInfo
	for _, f := range findings {
		if riskLevelOrder(f.RiskLevel) > riskLevelOrder(result) {
			result = f.RiskLevel
		}
	}
	return result
}

// decisionFromTool converts a safety Decision to a tool.PermissionDecision.
// DecisionNeedsHumanReview is mapped to AskPermission since both require
// human intervention before proceeding.
func decisionFromTool(d Decision) tool.PermissionDecision {
	switch d {
	case DecisionAllow:
		return tool.AllowPermission()
	case DecisionDeny:
		return tool.DenyPermission("safety guard: execution denied")
	case DecisionAsk, DecisionNeedsHumanReview:
		return tool.AskPermission("safety guard: human review required")
	default:
		return tool.DenyPermission("safety guard: unknown decision")
	}
}
