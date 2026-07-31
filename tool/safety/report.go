//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package safety provides opt-in, conservative pre-execution safety checks for
// tools. It reports policy decisions and integrates with tool.PermissionPolicy,
// but it does not enforce runtime resource limits or replace sandbox isolation.
package safety

import "trpc.group/trpc-go/trpc-agent-go/tool"

// Decision describes the action selected by a safety check.
type Decision string

const (
	// DecisionAllow permits execution.
	DecisionAllow Decision = "allow"
	// DecisionDeny prevents execution.
	DecisionDeny Decision = "deny"
	// DecisionAsk requires human approval before execution.
	DecisionAsk Decision = "ask"
)

// RuleID identifies a stable safety rule for reports and monitoring systems.
type RuleID string

const (
	// RuleAllow identifies a scan that found no policy violation.
	RuleAllow RuleID = "allow"
	// RuleInvalidInput identifies malformed or incomplete scan input.
	RuleInvalidInput RuleID = "invalid_input"
	// RuleShellBypass identifies shell syntax that cannot be safely parsed.
	RuleShellBypass RuleID = "shell_bypass"
	// RuleCommandDenied identifies a command rejected by command policy.
	RuleCommandDenied RuleID = "command_denied"
	// RuleDangerousDelete identifies recursive or forced deletion.
	RuleDangerousDelete RuleID = "dangerous_delete"
	// RuleSystemModification identifies an unsafe system-level modification.
	RuleSystemModification RuleID = "system_modification"
	// RuleForbiddenPath identifies access to a prohibited or sensitive path.
	RuleForbiddenPath RuleID = "forbidden_path"
	// RuleNetworkEgress identifies network access outside the domain allowlist.
	RuleNetworkEgress RuleID = "network_egress"
	// RuleDependencyChange identifies a dependency or package installation.
	RuleDependencyChange RuleID = "dependency_change"
	// RuleTimeoutLimit identifies a timeout above the configured maximum.
	RuleTimeoutLimit RuleID = "timeout_limit"
	// RuleResourceAbuse identifies unbounded or excessive resource use.
	RuleResourceAbuse RuleID = "resource_abuse"
	// RuleHostSession identifies a host PTY or background session needing review.
	RuleHostSession RuleID = "host_session"
	// RuleInteractiveInput identifies input written to an existing interactive session.
	RuleInteractiveInput RuleID = "interactive_input"
	// RuleEnvironment identifies an environment variable rejected by policy.
	RuleEnvironment RuleID = "environment"
	// RuleSecretExposure identifies secret material in execution input.
	RuleSecretExposure RuleID = "secret_exposure"
	// RuleUnknownLanguage identifies a code language without a scanner.
	RuleUnknownLanguage RuleID = "unknown_language"
	// RuleToolMetadata identifies an opaque open-world or destructive tool.
	RuleToolMetadata RuleID = "tool_metadata"
)

// Backend identifies the execution boundary described by a scan input.
type Backend string

const (
	// BackendUnknown indicates that the execution boundary is not known.
	BackendUnknown Backend = "unknown"
	// BackendGeneric identifies a generic command tool.
	BackendGeneric Backend = "generic"
	// BackendWorkspace identifies workspace-isolated command execution.
	BackendWorkspace Backend = "workspace"
	// BackendHost identifies direct host shell execution.
	BackendHost Backend = "host"
	// BackendCodeExecutor identifies source-code execution through a CodeExecutor.
	BackendCodeExecutor Backend = "codeexecutor"
)

// Finding describes one rule matched during a safety scan.
type Finding struct {
	// Decision is the action selected by this finding.
	Decision Decision `json:"decision"`
	// RiskLevel is the severity of this finding.
	RiskLevel RiskLevel `json:"risk_level"`
	// RuleID is the stable identifier of the matched rule.
	RuleID RuleID `json:"rule_id"`
	// Evidence is a redacted, bounded explanation of the match.
	Evidence string `json:"evidence"`
	// Recommendation describes how a caller can remove or review the risk.
	Recommendation string `json:"recommendation"`
}

// RiskLevel describes the severity of a safety finding.
type RiskLevel string

const (
	// RiskLevelLow indicates minimal risk.
	RiskLevelLow RiskLevel = "low"
	// RiskLevelMedium indicates reviewable risk.
	RiskLevelMedium RiskLevel = "medium"
	// RiskLevelHigh indicates serious risk.
	RiskLevelHigh RiskLevel = "high"
	// RiskLevelCritical indicates execution must be blocked.
	RiskLevelCritical RiskLevel = "critical"
)

// Report is the structured result of a tool safety check.
type Report struct {
	// SchemaVersion identifies the report and audit schema.
	SchemaVersion string `json:"schema_version"`
	// PolicyID identifies the configured policy.
	PolicyID string `json:"policy_id"`
	// PolicyRevision is the SHA-256 revision of the normalized policy.
	PolicyRevision string `json:"policy_revision"`
	// Decision is the aggregate action for this scan.
	Decision Decision `json:"decision"`
	// RiskLevel is the highest severity across all findings.
	RiskLevel RiskLevel `json:"risk_level"`
	// RuleID is the stable identifier of the primary finding.
	RuleID RuleID `json:"rule_id"`
	// Evidence is the redacted, bounded evidence of the primary finding.
	Evidence string `json:"evidence"`
	// Recommendation describes how to remove or review the primary risk.
	Recommendation string `json:"recommendation"`
	// ToolName is the redacted, bounded model-visible tool name.
	ToolName string `json:"tool_name"`
	// Command is a redacted, bounded command or code preview.
	Command string `json:"command"`
	// CommandSHA256 identifies the original command or code without logging it.
	CommandSHA256 string `json:"command_sha256"`
	// Backend identifies the execution boundary evaluated by the scan. Invalid
	// input values are reported as BackendUnknown.
	Backend Backend `json:"backend"`
	// Intercepted reports whether this decision skips immediate execution.
	Intercepted bool `json:"intercepted"`
	// Redacted reports whether a report value was replaced or truncated.
	Redacted bool `json:"redacted"`
	// Findings contains every distinct matched rule in stable scan order.
	Findings []Finding `json:"findings"`
}

// CodeBlock is one source block submitted to a code execution tool.
type CodeBlock struct {
	// Language identifies the source language.
	Language string `json:"language"`
	// Code contains the source text to scan.
	Code string `json:"code"`
}

// ScanInput contains the execution data evaluated before a tool runs.
//
// Command is a shell command string unless Arguments is non-empty, in which
// case Command is treated as one executable name and Arguments as structured
// argv values. TimeoutSeconds is zero when the caller did not request a
// timeout. RequestedOutputBytes is advisory and is only checked when a tool
// exposes an output-size argument.
type ScanInput struct {
	// ToolName is the model-visible tool name.
	ToolName string `json:"tool_name" yaml:"tool_name"`
	// Backend identifies the intended execution boundary. Empty values are inferred.
	Backend Backend `json:"backend" yaml:"backend"`
	// Command is either a shell command string or one structured executable name.
	Command string `json:"command,omitempty" yaml:"command,omitempty"`
	// Arguments contains literal argv values for a structured command.
	Arguments []string `json:"arguments,omitempty" yaml:"arguments,omitempty"`
	// WorkingDirectory is the requested command or code working directory.
	WorkingDirectory string `json:"working_directory,omitempty" yaml:"working_directory,omitempty"`
	// Environment contains call-level environment overrides.
	Environment map[string]string `json:"environment,omitempty" yaml:"environment,omitempty"`
	// Metadata describes an opaque tool when no command or source is available.
	Metadata tool.ToolMetadata `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	// CodeBlocks contains source blocks submitted to a code executor.
	CodeBlocks []CodeBlock `json:"code_blocks,omitempty" yaml:"code_blocks,omitempty"`
	// TimeoutSeconds is an explicit requested timeout; zero means unspecified.
	TimeoutSeconds int `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
	// RequestedOutputBytes is an explicit output limit; zero means unspecified.
	RequestedOutputBytes int `json:"requested_output_bytes,omitempty" yaml:"requested_output_bytes,omitempty"`
	// Background reports whether the request starts a resumable background session.
	Background bool `json:"background,omitempty" yaml:"background,omitempty"`
	// PTY reports whether the request allocates an interactive terminal.
	PTY bool `json:"pty,omitempty" yaml:"pty,omitempty"`

	initialFindings []Finding
	extraValues     []string
	sessionWrite    bool
}
