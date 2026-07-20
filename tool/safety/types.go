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
)

// Decision is the canonical safety decision returned by the scanner.
//
// "needs_human_review" is accepted as a policy alias and is normalized to
// DecisionAsk by the loader. The framework adapter always returns
// tool.PermissionActionAsk because that is the only approval action
// supported by tool.PermissionPolicy.
type Decision string

const (
	// DecisionAllow authorizes execution.
	DecisionAllow Decision = "allow"
	// DecisionDeny blocks execution and returns a structured denial.
	DecisionDeny Decision = "deny"
	// DecisionAsk pauses execution pending human review.
	DecisionAsk Decision = "ask"
	// DecisionNeedsHumanReview is the alias accepted in policy files.
	DecisionNeedsHumanReview Decision = "needs_human_review"
)

// RiskLevel classifies the severity of a Finding.
type RiskLevel string

const (
	// RiskLow is informational; the threshold default is allow.
	RiskLow RiskLevel = "low"
	// RiskMedium is moderate; the threshold default is ask.
	RiskMedium RiskLevel = "medium"
	// RiskHigh is serious; the threshold default is deny.
	RiskHigh RiskLevel = "high"
	// RiskCritical is severe and always denies regardless of threshold.
	RiskCritical RiskLevel = "critical"
)

// Backend identifies the execution surface a tool targets.
type Backend string

const (
	// BackendUnknown is used when no profile is registered.
	BackendUnknown Backend = "unknown"
	// BackendWorkspaceExec is the workspaceexec.ExecTool backend.
	BackendWorkspaceExec Backend = "workspace_exec"
	// BackendHostExec is the hostexec exec_command backend.
	BackendHostExec Backend = "hostexec"
	// BackendCodeExec is the codeexec execute_code backend.
	BackendCodeExec Backend = "codeexec"
	// BackendMCP is a remote MCP server tool.
	BackendMCP Backend = "mcp"
)

// CodeBlock is one decoded code segment from execute_code or an equivalent
// MCP profile.
type CodeBlock struct {
	Language string `json:"language" yaml:"language"`
	Code     string `json:"code" yaml:"code"`
}

// ScanInput is the data the scanner inspects. Callers may populate it
// directly or via Guard.CheckToolPermission, which decodes the
// tool.PermissionRequest arguments through a registered ToolProfile.
//
// Command is always a truncated/redacted summary in the resulting report;
// the original is never persisted.
type ScanInput struct {
	// ToolName is the model-visible tool name.
	ToolName string
	// Backend identifies the execution surface.
	Backend Backend
	// Command is the raw shell command, when present.
	Command string
	// Args is the explicit argv when the caller already split it.
	Args []string
	// CodeBlocks is the decoded code blocks for execute_code shapes.
	CodeBlocks []CodeBlock
	// Cwd is the requested working directory.
	Cwd string
	// Env is the requested environment override.
	Env map[string]string
	// Timeout is the requested timeout.
	Timeout time.Duration
	// OutputSizeHint is the declared or default output-size limit.
	OutputSizeHint int64
	// Background marks a hostexec background session request.
	Background bool
	// PTY marks a hostexec PTY session request.
	PTY bool
	// SessionID is the opaque hostexec/workspace session id.
	SessionID string
	// SessionInput is the chars argument for write_stdin.
	SessionInput string
	// ToolProfile is the registered profile name (for custom/MCP tools).
	ToolProfile string
	// Metadata carries the tool's published metadata. The guard maps
	// Destructive, OpenWorld, ConcurrencySafe, and ReadOnly into
	// findings so the policy can deny or ask based on tool-declared
	// capabilities without inspecting the arguments.
	Metadata ToolMetadata
}

// ToolMetadata mirrors the tool's published metadata. It is populated
// by Guard.CheckToolPermission from tool.PermissionRequest.Metadata.
type ToolMetadata struct {
	// ReadOnly reports that the tool does not intentionally mutate
	// external state.
	ReadOnly bool
	// Destructive reports that the tool may delete, overwrite, or
	// otherwise irreversibly change external state.
	Destructive bool
	// ConcurrencySafe reports that independent calls to the same tool
	// can run at the same time without corrupting shared state.
	ConcurrencySafe bool
	// SearchOrRead reports that the tool primarily searches or reads
	// data.
	SearchOrRead bool
	// OpenWorld reports that the tool can reach outside the current
	// process or workspace.
	OpenWorld bool
	// MaxResultSize is an optional advisory result-size limit in bytes.
	MaxResultSize int
}

// Finding is one rule result. Decision is the action contributed by this
// finding after rule overrides and risk thresholds are applied.
type Finding struct {
	// RuleID is the stable identifier, e.g. "command.dangerous_delete".
	RuleID string `json:"rule_id"`
	// RiskLevel is the severity assigned by the rule.
	RiskLevel RiskLevel `json:"risk_level"`
	// Decision is the action this finding contributes.
	Decision Decision `json:"decision"`
	// Evidence is a redacted snippet, pattern id, or path pattern. It
	// never contains the matched secret value or raw command payload.
	Evidence string `json:"evidence"`
	// Recommendation is the human-readable remediation.
	Recommendation string `json:"recommendation"`
}

// ScanReport is the structured output of one scan.
type ScanReport struct {
	// SchemaVersion is the report schema version.
	SchemaVersion string `json:"schema_version"`
	// ScanID is a unique identifier for this scan.
	ScanID string `json:"scan_id"`
	// Timestamp is when the scan ran.
	Timestamp time.Time `json:"timestamp"`
	// ToolName is the scanned tool name.
	ToolName string `json:"tool_name"`
	// Backend is the scanned backend.
	Backend Backend `json:"backend"`
	// Command is a truncated/redacted summary of the command.
	Command string `json:"command"`
	// CommandHash is a SHA-256 digest of the raw command, allowing
	// correlation without storing the original.
	CommandHash string `json:"command_hash,omitempty"`
	// Decision is the aggregated decision.
	Decision Decision `json:"decision"`
	// RiskLevel is the aggregated risk level.
	RiskLevel RiskLevel `json:"risk_level"`
	// Findings lists every rule result, sorted by risk descending,
	// then rule id, then evidence.
	Findings []Finding `json:"findings"`
	// Intercepted reports whether the final decision is not allow.
	Intercepted bool `json:"intercepted"`
	// Redacted reports whether any redaction was applied to evidence.
	Redacted bool `json:"redacted"`
	// DurationMs is the scan duration in milliseconds.
	DurationMs float64 `json:"duration_ms"`
}

// BatchSummary aggregates a BatchReport.
type BatchSummary struct {
	// Total is the number of scanned inputs.
	Total int `json:"total"`
	// Allowed counts allow decisions.
	Allowed int `json:"allowed"`
	// Denied counts deny decisions.
	Denied int `json:"denied"`
	// Asked counts ask decisions.
	Asked int `json:"asked"`
}

// BatchReport aggregates several ScanReports using one scanner/policy.
type BatchReport struct {
	// SchemaVersion is the batch schema version.
	SchemaVersion string `json:"schema_version"`
	// GeneratedAt is the batch generation time.
	GeneratedAt time.Time `json:"generated_at"`
	// Reports is the per-input scan reports in input order.
	Reports []ScanReport `json:"reports"`
	// Summary is the aggregate decision counts.
	Summary BatchSummary `json:"summary"`
}

// ruleSeverity ranks RiskLevel for sorting and threshold lookups.
func ruleSeverity(r RiskLevel) int {
	switch r {
	case RiskCritical:
		return 4
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	}
	return 0
}

// decisionSeverity ranks Decision for aggregation (deny > ask > allow).
func decisionSeverity(d Decision) int {
	switch d {
	case DecisionDeny:
		return 3
	case DecisionAsk:
		return 2
	case DecisionAllow:
		return 1
	}
	return 0
}
