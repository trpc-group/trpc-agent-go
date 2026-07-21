//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package review contains the shared data model for the code review example.
package review

import "time"

const (
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"

	InputTypeDiffFile = "diff_file"
	InputTypeRepoPath = "repo_path"
	InputTypeFixture  = "fixture"
	InputTypeFiles    = "files"

	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
)

// ReviewTask records one review invocation.
type ReviewTask struct {
	ID           string     `json:"id"`
	Status       string     `json:"status"`
	InputType    string     `json:"input_type"`
	InputSummary string     `json:"input_summary"`
	RepoPath     string     `json:"repo_path,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	Error        string     `json:"error,omitempty"`
}

// ChangedFile is a file touched by a unified diff.
type ChangedFile struct {
	OldPath     string `json:"old_path,omitempty"`
	NewPath     string `json:"new_path"`
	Language    string `json:"language,omitempty"`
	PackageName string `json:"package_name,omitempty"`
	Hunks       []Hunk `json:"hunks"`
}

// Hunk is one unified diff hunk.
type Hunk struct {
	OldStart int        `json:"old_start"`
	OldCount int        `json:"old_count"`
	NewStart int        `json:"new_start"`
	NewCount int        `json:"new_count"`
	Header   string     `json:"header,omitempty"`
	Lines    []DiffLine `json:"lines"`
}

// DiffLine is one line inside a hunk.
type DiffLine struct {
	Kind    string `json:"kind"` // added, removed, context
	OldLine int    `json:"old_line,omitempty"`
	NewLine int    `json:"new_line,omitempty"`
	Content string `json:"content"`
}

// Finding is a structured review result.
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

// Filter decision stages and outcomes for the noise-control pipeline.
const (
	FilterStageDedup      = "dedup"
	FilterStageConfidence = "confidence"

	FilterDecisionKeep          = "keep"
	FilterDecisionHumanReview   = "needs_human_review"
	FilterDecisionWarning       = "warning"
	FilterDecisionDropDuplicate = "drop_duplicate"
)

// FilterDecision records why the noise-control pipeline kept, demoted,
// or dropped a finding.
type FilterDecision struct {
	RuleID     string    `json:"rule_id"`
	File       string    `json:"file"`
	Line       int       `json:"line"`
	Source     string    `json:"source"`
	Confidence float64   `json:"confidence"`
	Stage      string    `json:"stage"`
	Decision   string    `json:"decision"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}

// PermissionDecision records whether a command may run.
type PermissionDecision struct {
	Command   string    `json:"command"`
	Decision  string    `json:"decision"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// SandboxRun records one external check execution.
type SandboxRun struct {
	Command       string `json:"command"`
	Status        string `json:"status"`
	ExitCode      int    `json:"exit_code,omitempty"`
	DurationMS    int64  `json:"duration_ms"`
	StdoutExcerpt string `json:"stdout_excerpt,omitempty"`
	StderrExcerpt string `json:"stderr_excerpt,omitempty"`
	Error         string `json:"error,omitempty"`
}

// Artifact records files produced by the review.
type Artifact struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// ReportRecord points to persisted report outputs.
type ReportRecord struct {
	JSONPath     string `json:"json_path"`
	MarkdownPath string `json:"markdown_path"`
	SummaryJSON  string `json:"summary_json"`
}

// TaskSnapshot is a database query result for one review task.
type TaskSnapshot struct {
	Task                ReviewTask           `json:"task"`
	Findings            []Finding            `json:"findings"`
	SandboxRuns         []SandboxRun         `json:"sandbox_runs"`
	PermissionDecisions []PermissionDecision `json:"permission_decisions"`
	FilterDecisions     []FilterDecision     `json:"filter_decisions"`
	Artifacts           []Artifact           `json:"artifacts"`
	Report              ReportRecord         `json:"report"`
}

// MetricsSummary is the audit and monitoring summary for one review.
type MetricsSummary struct {
	TotalDurationMS       int64          `json:"total_duration_ms"`
	SandboxDurationMS     int64          `json:"sandbox_duration_ms"`
	ModelDurationMS       int64          `json:"model_duration_ms"`
	ToolCallCount         int            `json:"tool_call_count"`
	ModelCallCount        int            `json:"model_call_count"`
	PermissionDenyCount   int            `json:"permission_deny_count"`
	FindingCount          int            `json:"finding_count"`
	WarningCount          int            `json:"warning_count"`
	NeedsHumanReviewCount int            `json:"needs_human_review_count"`
	SeverityCounts        map[string]int `json:"severity_counts"`
	ExceptionCounts       map[string]int `json:"exception_counts"`
	FilterDecisionCounts  map[string]int `json:"filter_decision_counts"`
}

// ReviewReport is the final serializable report.
type ReviewReport struct {
	Task                ReviewTask           `json:"task"`
	Files               []ChangedFile        `json:"files"`
	Findings            []Finding            `json:"findings"`
	Warnings            []Finding            `json:"warnings"`
	NeedsHumanReview    []Finding            `json:"needs_human_review"`
	SandboxRuns         []SandboxRun         `json:"sandbox_runs"`
	PermissionDecisions []PermissionDecision `json:"permission_decisions"`
	FilterDecisions     []FilterDecision     `json:"filter_decisions"`
	Metrics             MetricsSummary       `json:"metrics"`
	Artifacts           []Artifact           `json:"artifacts"`
	Summary             string               `json:"summary"`
}
