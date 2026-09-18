//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package safety provides opt-in policy checks for tool execution requests.
package safety

import (
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Decision is the action selected by a safety policy.
type Decision string

// RiskLevel describes the severity of a safety finding.
type RiskLevel string

// Backend identifies the execution backend requested by a tool call.
type Backend string

const (
	// DecisionAllow permits execution.
	DecisionAllow Decision = "allow"
	// DecisionDeny prevents execution.
	DecisionDeny Decision = "deny"
	// DecisionAsk requires confirmation before execution.
	DecisionAsk Decision = "ask"
	// DecisionNeedsHumanReview requires a human review before execution.
	DecisionNeedsHumanReview Decision = "needs_human_review"
)

const (
	// RiskLow represents low risk.
	RiskLow RiskLevel = "low"
	// RiskMedium represents medium risk.
	RiskMedium RiskLevel = "medium"
	// RiskHigh represents high risk.
	RiskHigh RiskLevel = "high"
	// RiskCritical represents critical risk.
	RiskCritical RiskLevel = "critical"
)

const (
	// BackendWorkspaceExec identifies the workspace execution backend.
	BackendWorkspaceExec Backend = "workspaceexec"
	// BackendHostExec identifies the host execution backend.
	BackendHostExec Backend = "hostexec"
	// BackendCodeExec identifies the code execution backend.
	BackendCodeExec Backend = "codeexec"
	// BackendUnknown identifies an unspecified execution backend.
	BackendUnknown Backend = "unknown"
)

// Policy configures the safety checks applied by a Guard.
type Policy struct {
	AllowedCommands   []string `json:"allowed_commands" yaml:"allowed_commands"`
	DeniedCommands    []string `json:"denied_commands" yaml:"denied_commands"`
	DeniedPaths       []string `json:"denied_paths" yaml:"denied_paths"`
	NetworkAllowlist  []string `json:"network_allowlist" yaml:"network_allowlist"`
	EnvAllowlist      []string `json:"env_allowlist" yaml:"env_allowlist"`
	ReviewCommands    []string `json:"review_commands" yaml:"review_commands"`
	MaxTimeoutSeconds int      `json:"max_timeout_seconds" yaml:"max_timeout_seconds"`
	MaxOutputBytes    int64    `json:"max_output_bytes" yaml:"max_output_bytes"`
	ParseErrorAction  Decision `json:"parse_error_action" yaml:"parse_error_action"`
	PipelineAction    Decision `json:"pipeline_action" yaml:"pipeline_action"`
}

// Request describes an execution request to scan.
type Request struct {
	ToolName       string                   `json:"tool_name,omitempty"`
	Backend        Backend                  `json:"backend,omitempty"`
	Command        string                   `json:"command,omitempty"`
	Args           []string                 `json:"args,omitempty"`
	Cwd            string                   `json:"cwd,omitempty"`
	Env            map[string]string        `json:"env,omitempty"`
	TimeoutSeconds int                      `json:"timeout_seconds,omitempty"`
	MaxOutputBytes int64                    `json:"max_output_bytes,omitempty"`
	Background     bool                     `json:"background,omitempty"`
	TTY            bool                     `json:"tty,omitempty"`
	CodeBlocks     []codeexecutor.CodeBlock `json:"code_blocks,omitempty"`
	RawArguments   json.RawMessage          `json:"raw_arguments,omitempty"`
	Metadata       tool.ToolMetadata        `json:"metadata,omitempty"`
}

// Finding records one policy finding produced while scanning a request.
type Finding struct {
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id"`
	Evidence       []string  `json:"evidence"`
	Recommendation string    `json:"recommendation"`
}

// Report is the result of scanning an execution request.
type Report struct {
	SchemaVersion  int       `json:"schema_version"`
	ScanID         string    `json:"scan_id"`
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id"`
	Evidence       []string  `json:"evidence"`
	Recommendation string    `json:"recommendation"`
	ToolName       string    `json:"tool_name"`
	Command        string    `json:"command,omitempty"`
	Backend        Backend   `json:"backend"`
	Blocked        bool      `json:"blocked"`
	Redacted       bool      `json:"redacted"`
	DurationMillis int64     `json:"duration_ms"`
	SafeSummary    string    `json:"safe_summary,omitempty"`
	Findings       []Finding `json:"findings,omitempty"`
}

func isZeroPolicy(policy Policy) bool {
	return len(policy.AllowedCommands) == 0 && len(policy.DeniedCommands) == 0 &&
		len(policy.DeniedPaths) == 0 && len(policy.NetworkAllowlist) == 0 &&
		len(policy.EnvAllowlist) == 0 && len(policy.ReviewCommands) == 0 &&
		policy.MaxTimeoutSeconds == 0 && policy.MaxOutputBytes == 0 &&
		policy.ParseErrorAction == "" && policy.PipelineAction == ""
}
