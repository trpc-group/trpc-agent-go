// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

// Decision is the verdict the Safety Guard returns for a tool call.
// The string values are deliberately lower-case to align with
// [tool.PermissionAction] ("allow", "deny", "ask") so the guard can
// be wired into the existing permission pipeline without a translation
// layer.
//
// [tool.PermissionAction]: ../../tool/permission.go
type Decision string

const (
	// DecisionAllow means the call is safe and may proceed.
	DecisionAllow Decision = "allow"

	// DecisionDeny means the call violates a hard rule and must not
	// execute. The caller should return a structured denial to the
	// model.
	DecisionDeny Decision = "deny"

	// DecisionAsk means the call carries elevated risk. The caller
	// should prompt a human for approval before executing.
	DecisionAsk Decision = "ask"

	// DecisionNeedsHumanReview means the guard could not classify
	// the call with sufficient confidence. A human must inspect the
	// [Report] and decide manually. This value has no counterpart in
	// [tool.PermissionAction]; callers that need to map back should
	// treat it as "ask" with an additional review-required flag.
	DecisionNeedsHumanReview Decision = "needs_human_review"
)

// RiskLevel classifies the severity of a finding. Higher levels
// indicate greater potential for harm. The values are lower-case
// strings so they serialise cleanly to JSON / YAML.
type RiskLevel string

const (
	// RiskNone indicates no risk was identified.
	RiskNone RiskLevel = "none"

	// RiskLow indicates a minor policy concern that does not block
	// execution on its own but may contribute to a deny decision.
	RiskLow RiskLevel = "low"

	// RiskMedium indicates a notable risk that warrants attention.
	// Calls at this level are typically escalated to [DecisionAsk].
	RiskMedium RiskLevel = "medium"

	// RiskHigh indicates a serious risk. Calls at this level are
	// typically denied unless the operator has explicitly allowed
	// the pattern.
	RiskHigh RiskLevel = "high"

	// RiskCritical indicates an immediate, unconditional block.
	// This is reserved for patterns that are always unsafe, such
	// as shell injection vectors or known exploit payloads.
	RiskCritical RiskLevel = "critical"
)

// Evidence describes a single rule match that contributed to the
// decision. A [Report] may contain zero or more evidence entries;
// when the decision is deny or ask, at least one evidence entry
// should be present so the caller can explain the decision.
type Evidence struct {
	// RuleID is the stable identifier of the rule that matched,
	// e.g. "deny-curl", "forbidden-path-etc-shadow", or
	// "network-non-whitelist".
	RuleID string `json:"rule_id"`

	// RiskLevel is the severity this evidence carries. Multiple
	// evidence entries may combine; the overall [Report.RiskLevel]
	// is the maximum across all entries.
	RiskLevel RiskLevel `json:"risk_level"`

	// MatchedSnippet is the portion of the command or arguments
	// that triggered the rule. It may be truncated for very long
	// inputs; see [Report.Redacted].
	MatchedSnippet string `json:"matched_snippet,omitempty"`

	// Line is the 1-based line number in the source where the match
	// occurred, when applicable. A value of 0 means the rule does
	// not have a meaningful line association (e.g. it matched on
	// argv[0] rather than a multi-line script).
	Line int `json:"line,omitempty"`

	// Reason is a human-readable explanation of why this rule
	// matched and what risk it represents.
	Reason string `json:"reason,omitempty"`

	// Recommendation is the rule-specific, safe next step. It is kept on
	// each evidence item so callers do not have to infer a remedy from a
	// broad aggregate recommendation.
	Recommendation string `json:"recommendation,omitempty"`
}

// Report is the structured output of a Safety Guard scan. It carries
// the decision, the evidence that led to it, and metadata about the
// scan itself. All fields carry JSON tags in snake_case so the report
// can be serialised to structured tool results, audit logs, or API
// responses without additional mapping.
type Report struct {
	// ToolName is the model-visible name of the tool that was
	// inspected.
	ToolName string `json:"tool_name"`

	// Backend identifies the execution backend, e.g. "shellsafe",
	// "powershell", or "codeexec".
	Backend string `json:"backend"`

	// Command is the raw command string (or argument summary) that
	// was inspected. It may be redacted if [Redacted] is true.
	Command string `json:"command"`

	// Decision is the verdict: allow, deny, ask, or
	// needs_human_review.
	Decision Decision `json:"decision"`

	// RiskLevel is the aggregate risk across all evidence entries.
	// It is the maximum [Evidence.RiskLevel], or [RiskNone] when
	// there is no evidence.
	RiskLevel RiskLevel `json:"risk_level"`

	// Evidences lists the individual rule matches that contributed
	// to the decision. May be empty when the decision is allow.
	Evidences []Evidence `json:"evidences,omitempty"`

	// Recommendation is an optional human-readable suggestion for
	// the model or operator, e.g. "Use an audited workspace script
	// instead of curl".
	Recommendation string `json:"recommendation,omitempty"`

	// Intercepted is true when the guard prevented the call from
	// reaching the execution backend (i.e. the decision was deny,
	// ask, or needs_human_review and the caller honoured it).
	Intercepted bool `json:"intercepted"`

	// DurationMS is the wall-clock time the scan took, in
	// milliseconds.
	DurationMS int64 `json:"duration_ms"`

	// Redacted is true when sensitive content (secrets, PII) was
	// removed from [Command] or [Evidence.MatchedSnippet] before
	// the report was emitted.
	Redacted bool `json:"redacted"`
}
