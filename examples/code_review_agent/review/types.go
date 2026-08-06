//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package review defines shared code-review report types.
package review

import "time"

// Finding is one structured review issue.
type Finding struct {
	Severity       string  `json:"severity"`
	Category       string  `json:"category"`
	File           string  `json:"file"`
	Line           int     `json:"line"`
	Title          string  `json:"title"`
	Evidence       string  `json:"evidence"`
	Recommendation string  `json:"recommendation"`
	Confidence     float64 `json:"confidence"`
	Source         string  `json:"source"`
	RuleID         string  `json:"rule_id"`
}

// PermissionDecision records a governance decision.
type PermissionDecision struct {
	ToolName  string    `json:"tool_name,omitempty"`
	Command   string    `json:"command"`
	Action    string    `json:"action"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SandboxRunSummary summarizes one sandbox execution.
type SandboxRunSummary struct {
	ID           string `json:"id"`
	Executor     string `json:"executor"`
	Command      string `json:"command"`
	Status       string `json:"status"`
	ExitCode     int    `json:"exit_code"`
	DurationMS   int64  `json:"duration_ms"`
	StdoutBytes  int    `json:"stdout_bytes"`
	StderrBytes  int    `json:"stderr_bytes"`
	Truncated    bool   `json:"truncated"`
	Error        string `json:"error,omitempty"`
	StdoutSample string `json:"stdout_sample,omitempty"`
	StderrSample string `json:"stderr_sample,omitempty"`
}

// ArtifactRef points to a saved artifact.
type ArtifactRef struct {
	Name      string `json:"name"`
	PathOrRef string `json:"path_or_ref"`
	MIME      string `json:"mime,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

// MetricsSummary captures audit metrics for one review.
type MetricsSummary struct {
	TotalDurationMS     int64          `json:"total_duration_ms"`
	SandboxDurationMS   int64          `json:"sandbox_duration_ms"`
	ToolCallCount       int            `json:"tool_call_count"`
	PermissionDenyCount int            `json:"permission_deny_count"`
	PermissionAskCount  int            `json:"permission_ask_count"`
	FindingCount        int            `json:"finding_count"`
	WarningCount        int            `json:"warning_count"`
	SeverityDist        map[string]int `json:"severity_dist"`
	ExceptionDist       map[string]int `json:"exception_dist"`
}

// InputMeta describes the review input.
type InputMeta struct {
	Kind    string `json:"kind"`
	Digest  string `json:"digest"`
	Summary string `json:"summary"`
}

// GovernanceSummary aggregates permission outcomes.
type GovernanceSummary struct {
	PermissionDecisions []PermissionDecision `json:"permission_decisions"`
	ExecutorFallback    string               `json:"executor_fallback,omitempty"`
	AgentAssistNote     string               `json:"agent_assist_note,omitempty"`
}

// Report is the full review report written to JSON/Markdown and DB.
type Report struct {
	TaskID      string              `json:"task_id"`
	Status      string              `json:"status"`
	Mode        string              `json:"mode"`
	Executor    string              `json:"executor"`
	GeneratedAt time.Time           `json:"generated_at"`
	Input       InputMeta           `json:"input"`
	Findings    []Finding           `json:"findings"`
	Warnings    []Finding           `json:"warnings"`
	Governance  GovernanceSummary   `json:"governance"`
	SandboxRuns []SandboxRunSummary `json:"sandbox_runs"`
	Metrics     MetricsSummary      `json:"metrics"`
	Artifacts   []ArtifactRef       `json:"artifacts"`
	Conclusion  string              `json:"conclusion"`
}

// Severity helpers.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
)

// Status helpers.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusPartial   = "partial"
	StatusFailed    = "failed"
)

// Bucket helpers.
const (
	BucketFinding = "finding"
	BucketWarning = "warning"
)

// Mode helpers.
const (
	ModeRuleOnly = "rule-only"
	ModeDryRun   = "dry-run"
	ModeLLM      = "llm"
)
