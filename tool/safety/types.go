// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import "time"

// Decision is the normalized safety outcome for a pending execution.
type Decision string

// Decision values used by reports and permission decisions.
const (
	DecisionAllow            Decision = "allow"
	DecisionDeny             Decision = "deny"
	DecisionAsk              Decision = "ask"
	DecisionNeedsHumanReview Decision = "needs_human_review"
)

// RiskLevel is the severity assigned to a finding or final report.
type RiskLevel string

// RiskLevel values are ordered from no risk to critical risk.
const (
	RiskNone     RiskLevel = "none"
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// Backend classifies the execution surface being scanned.
type Backend string

// Backend values identify execution surfaces known to the scanner.
const (
	BackendWorkspaceExec Backend = "workspaceexec"
	BackendHostExec      Backend = "hostexec"
	BackendCodeExec      Backend = "codeexec"
	BackendMCP           Backend = "mcp"
	BackendSkill         Backend = "skill"
	BackendUnknown       Backend = "unknown"
)

// Category classifies a safety finding.
type Category string

// Category values classify safety findings for audit and telemetry.
const (
	CategoryDangerousCommand Category = "dangerous_command"
	CategoryNetwork          Category = "network"
	CategoryShellBypass      Category = "shell_bypass"
	CategoryHostExec         Category = "hostexec"
	CategoryDependency       Category = "dependency"
	CategoryResource         Category = "resource"
	CategorySecretLeak       Category = "secret_leak"
	CategoryPolicy           Category = "policy"
)

// ExecutionRequest is the normalized input scanned before a tool executes.
type ExecutionRequest struct {
	ID             string            `json:"id,omitempty" yaml:"id,omitempty"`
	ToolName       string            `json:"tool_name" yaml:"tool_name"`
	ToolCallID     string            `json:"tool_call_id,omitempty" yaml:"tool_call_id,omitempty"`
	Backend        Backend           `json:"backend" yaml:"backend"`
	Command        string            `json:"command,omitempty" yaml:"command,omitempty"`
	Args           []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Script         string            `json:"script,omitempty" yaml:"script,omitempty"`
	Language       string            `json:"language,omitempty" yaml:"language,omitempty"`
	Cwd            string            `json:"cwd,omitempty" yaml:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Timeout        time.Duration     `json:"-" yaml:"-"`
	TimeoutMS      int64             `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	MaxOutputBytes int64             `json:"max_output_bytes,omitempty" yaml:"max_output_bytes,omitempty"`
	TTY            bool              `json:"tty,omitempty" yaml:"tty,omitempty"`
	Background     bool              `json:"background,omitempty" yaml:"background,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// Finding describes one matched safety rule.
type Finding struct {
	RuleID         string    `json:"rule_id"`
	Category       Category  `json:"category"`
	RiskLevel      RiskLevel `json:"risk_level"`
	Action         Decision  `json:"action"`
	Evidence       string    `json:"evidence"`
	Location       string    `json:"location,omitempty"`
	Recommendation string    `json:"recommendation"`
	Redacted       bool      `json:"redacted"`
}

// Report is the structured scan result returned to callers.
type Report struct {
	SchemaVersion       string         `json:"schema_version"`
	RequestID           string         `json:"request_id,omitempty"`
	ToolName            string         `json:"tool_name"`
	Backend             Backend        `json:"backend"`
	Command             string         `json:"command,omitempty"`
	Decision            Decision       `json:"decision"`
	RiskLevel           RiskLevel      `json:"risk_level"`
	Blocked             bool           `json:"blocked"`
	DurationMS          float64        `json:"duration_ms"`
	RuleIDs             []string       `json:"rule_ids"`
	Findings            []Finding      `json:"findings"`
	Recommendation      string         `json:"recommendation"`
	Redacted            bool           `json:"redacted"`
	TelemetryAttributes map[string]any `json:"telemetry_attributes,omitempty"`
}

// AuditEvent is the compact append-only event form for audit sinks.
type AuditEvent struct {
	Timestamp   time.Time `json:"timestamp"`
	RequestID   string    `json:"request_id,omitempty"`
	ToolName    string    `json:"tool_name"`
	Backend     Backend   `json:"backend"`
	Decision    Decision  `json:"decision"`
	RiskLevel   RiskLevel `json:"risk_level"`
	RuleID      string    `json:"rule_id"`
	AllRuleIDs  []string  `json:"all_rule_ids,omitempty"`
	DurationMS  float64   `json:"duration_ms"`
	Blocked     bool      `json:"blocked"`
	Redacted    bool      `json:"redacted"`
	CommandHash string    `json:"command_hash,omitempty"`
	Summary     string    `json:"summary"`
}
