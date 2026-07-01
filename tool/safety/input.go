//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety implements a Tool Execution Safety Guard: it statically
// scans a shell command, script or code block before a tool executes it,
// decides allow / ask / needs_human_review / deny, and produces a
// structured report, a JSONL audit trail and OpenTelemetry span attributes.
//
// The guard is the "before execution" layer of a defence-in-depth strategy.
// It is NOT a sandbox replacement: a static scanner cannot observe runtime
// behaviour or confine syscalls. Pair it with an isolating executor
// (codeexecutor/container, codeexecutor/e2b) and post-hoc audit.
//
// The engine builds on internal/shellsafe for conservative command parsing
// and plugs into the framework through tool.PermissionPolicy (see
// permission.go), so a deny/ask decision skips tool execution.
package safety

// Backend identifies which execution surface a scan targets. Some rules are
// weighted more severely on the host backend, which runs on the real machine
// rather than an isolated workspace.
type Backend string

// Backend values.
const (
	// BackendWorkspaceExec is the isolated workspace_exec tool.
	BackendWorkspaceExec Backend = "workspace_exec"
	// BackendHostExec is the hostexec exec_command tool (real host shell).
	BackendHostExec Backend = "hostexec"
	// BackendCodeExec is the codeexec tool (runs code blocks).
	BackendCodeExec Backend = "codeexec"
	// BackendUnknown is used when the tool is not a recognised exec backend.
	BackendUnknown Backend = "unknown"
)

// CodeBlock is one language-tagged code fragment from a codeexec call.
type CodeBlock struct {
	Language string
	Code     string
}

// ToolMetadataView is a minimal read-only projection of tool.ToolMetadata.
// It is populated by the permission adapter so the engine does not depend on
// the semantic evolution of the tool package.
type ToolMetadataView struct {
	// ReadOnly reports the tool does not intentionally mutate external state.
	ReadOnly bool
	// Destructive reports the tool may irreversibly change external state.
	Destructive bool
}

// ScanInput is the normalised input to a scan. Exactly one of Command or
// CodeBlocks is typically populated depending on the backend.
type ScanInput struct {
	// ToolName is the model-visible tool name, e.g. "workspace_exec".
	ToolName string
	// Backend is the normalised execution surface.
	Backend Backend
	// Command is the shell command or script (may be multi-line) for exec
	// backends.
	Command string
	// CodeBlocks are the language-tagged fragments for the codeexec backend.
	CodeBlocks []CodeBlock
	// Cwd is the requested working directory, if any.
	Cwd string
	// Env is the per-call environment override map, if any.
	Env map[string]string
	// TimeoutSec is the requested timeout in seconds (0 if unset).
	TimeoutSec int
	// Metadata is the projection of the tool's published metadata.
	Metadata ToolMetadataView
}

// Decision is the guard's verdict for a rule or an overall report. The zero
// value is not valid; use the constants.
type Decision string

// Decision values, ordered from least to most restrictive by decisionRank.
const (
	// DecisionAllow permits execution.
	DecisionAllow Decision = "allow"
	// DecisionAsk requests approval before execution.
	DecisionAsk Decision = "ask"
	// DecisionNeedsHumanReview requires explicit human review (stronger ask).
	DecisionNeedsHumanReview Decision = "needs_human_review"
	// DecisionDeny blocks execution.
	DecisionDeny Decision = "deny"
)

// RiskLevel classifies how dangerous a finding is.
type RiskLevel string

// RiskLevel values, ordered from lowest to highest by riskRank.
const (
	// RiskNone means no risk detected.
	RiskNone RiskLevel = "none"
	// RiskLow is an informational or low-impact signal.
	RiskLow RiskLevel = "low"
	// RiskMedium is a notable but not immediately dangerous signal.
	RiskMedium RiskLevel = "medium"
	// RiskHigh is a dangerous signal.
	RiskHigh RiskLevel = "high"
	// RiskCritical is a severe signal that must be blocked.
	RiskCritical RiskLevel = "critical"
)

// riskRank orders risk levels so callers can pick the most severe.
func riskRank(r RiskLevel) int {
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

// maxRisk returns the more severe of a and b.
func maxRisk(a, b RiskLevel) RiskLevel {
	if riskRank(b) > riskRank(a) {
		return b
	}
	return a
}

// decisionRank orders decisions so aggregation can pick the most restrictive:
// deny > needs_human_review > ask > allow.
func decisionRank(d Decision) int {
	switch d {
	case DecisionDeny:
		return 3
	case DecisionNeedsHumanReview:
		return 2
	case DecisionAsk:
		return 1
	default:
		return 0
	}
}

// maxDecision returns the more restrictive of a and b.
func maxDecision(a, b Decision) Decision {
	if decisionRank(b) > decisionRank(a) {
		return b
	}
	return a
}

// blocks reports whether a decision prevents execution (anything but allow).
func (d Decision) blocks() bool {
	return d != DecisionAllow && d != ""
}
