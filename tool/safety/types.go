//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides a reusable pre-execution safety guard for
// shell-like tool calls.
package safety

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// DecisionAllow allows execution to continue.
	DecisionAllow Decision = "allow"
	// DecisionDeny blocks execution.
	DecisionDeny Decision = "deny"
	// DecisionAsk requires human review before execution.
	DecisionAsk Decision = "ask"

	// RiskLow is informational or safe under the current policy.
	RiskLow RiskLevel = "low"
	// RiskMedium means execution is plausible but deserves review.
	RiskMedium RiskLevel = "medium"
	// RiskHigh means the command is unsafe by policy.
	RiskHigh RiskLevel = "high"
	// RiskCritical means the command risks data loss or credential exposure.
	RiskCritical RiskLevel = "critical"

	// BackendWorkspaceExec is the workspace executor backend.
	BackendWorkspaceExec Backend = "workspaceexec"
	// BackendHostExec is the direct host executor backend.
	BackendHostExec Backend = "hostexec"
	// BackendCodeExec is the code executor backend.
	BackendCodeExec Backend = "codeexec"
	// BackendUnknown is used when the tool is not recognized.
	BackendUnknown Backend = "unknown"
)

// OpenTelemetry attribute names reserved by the safety guard.
const (
	OTelAttrDecision  = "tool.safety.decision"
	OTelAttrRiskLevel = "tool.safety.risk_level"
	OTelAttrRuleID    = "tool.safety.rule_id"
	OTelAttrBackend   = "tool.safety.backend"
)

// Decision is the normalized safety decision.
type Decision string

// RiskLevel is the normalized risk level.
type RiskLevel string

// Backend identifies the execution backend being guarded.
type Backend string

// Policy is loaded from tool_safety_policy.yaml or JSON.
type Policy struct {
	AllowedCommands     []string `json:"allowed_commands,omitempty" yaml:"allowed_commands,omitempty"`
	DeniedCommands      []string `json:"denied_commands,omitempty" yaml:"denied_commands,omitempty"`
	ForbiddenPaths      []string `json:"forbidden_paths,omitempty" yaml:"forbidden_paths,omitempty"`
	NetworkAllowlist    []string `json:"network_allowlist,omitempty" yaml:"network_allowlist,omitempty"`
	EnvAllowlist        []string `json:"env_allowlist,omitempty" yaml:"env_allowlist,omitempty"`
	MaxTimeoutSec       int      `json:"max_timeout_sec,omitempty" yaml:"max_timeout_sec,omitempty"`
	MaxOutputBytes      int      `json:"max_output_bytes,omitempty" yaml:"max_output_bytes,omitempty"`
	MaxConcurrency      int      `json:"max_concurrency,omitempty" yaml:"max_concurrency,omitempty"`
	ParseFailureAction  Decision `json:"parse_failure_action,omitempty" yaml:"parse_failure_action,omitempty"`
	UnknownToolAction   Decision `json:"unknown_tool_action,omitempty" yaml:"unknown_tool_action,omitempty"`
	DependencyAction    Decision `json:"dependency_action,omitempty" yaml:"dependency_action,omitempty"`
	PipelineAction      Decision `json:"pipeline_action,omitempty" yaml:"pipeline_action,omitempty"`
	HostPTYAction       Decision `json:"host_pty_action,omitempty" yaml:"host_pty_action,omitempty"`
	BackgroundAction    Decision `json:"background_action,omitempty" yaml:"background_action,omitempty"`
	DisallowedEnvAction Decision `json:"disallowed_env_action,omitempty" yaml:"disallowed_env_action,omitempty"`
}

// ScanRequest describes one pending tool execution.
type ScanRequest struct {
	ToolName       string            `json:"tool_name,omitempty"`
	ToolCallID     string            `json:"tool_call_id,omitempty"`
	Command        string            `json:"command,omitempty"`
	Backend        Backend           `json:"backend,omitempty"`
	Cwd            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSec     int               `json:"timeout_sec,omitempty"`
	YieldTimeMS    int               `json:"yield_time_ms,omitempty"`
	Background     bool              `json:"background,omitempty"`
	TTY            bool              `json:"tty,omitempty"`
	MaxOutputBytes int               `json:"max_output_bytes,omitempty"`
	Metadata       tool.ToolMetadata `json:"-"`
	validatedCode  bool
	shellCommand   string
	codeBlocks     []codeBlock
}

type codeBlock struct {
	language string
	code     string
}

// Finding is one rule hit in a scan report.
type Finding struct {
	RuleID         string    `json:"rule_id"`
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	Evidence       string    `json:"evidence"`
	Recommendation string    `json:"recommendation"`
}

// ScanReport is the structured scan result emitted before execution.
type ScanReport struct {
	Decision       Decision          `json:"decision"`
	RiskLevel      RiskLevel         `json:"risk_level"`
	RuleID         string            `json:"rule_id"`
	Evidence       []string          `json:"evidence"`
	Recommendation string            `json:"recommendation"`
	ToolName       string            `json:"tool_name"`
	Command        string            `json:"command"`
	Backend        Backend           `json:"backend"`
	Blocked        bool              `json:"blocked"`
	Redacted       bool              `json:"redacted"`
	DurationMS     int64             `json:"duration_ms"`
	Findings       []Finding         `json:"findings,omitempty"`
	OTelAttributes map[string]string `json:"otel_attributes,omitempty"`
}

// AuditEvent is the JSONL-safe event written for every scan.
type AuditEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	ToolName   string    `json:"tool_name"`
	Decision   Decision  `json:"decision"`
	RiskLevel  RiskLevel `json:"risk_level"`
	RuleID     string    `json:"rule_id"`
	DurationMS int64     `json:"duration_ms"`
	Redacted   bool      `json:"redacted"`
	Blocked    bool      `json:"blocked"`
	Backend    Backend   `json:"backend"`
}
