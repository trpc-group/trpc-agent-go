//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package reviewmodel defines the core domain types for the code review agent.
package reviewmodel

import "time"

// Severity levels for findings.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
	SeverityWarning  = "warning"
)

// Category labels for findings.
const (
	CategorySecurity      = "security"
	CategoryGoroutine     = "goroutine_context"
	CategoryResource      = "resource_lifecycle"
	CategoryDB            = "database_lifecycle"
	CategoryErrorHandling = "error_handling"
	CategoryTest          = "missing_tests"
	CategorySensitive     = "secret_redaction"
	CategoryOther         = "other"
)

// ReviewStatus tracks the lifecycle of a review task.
type ReviewStatus string

// ReviewStatus values.
const (
	StatusPending               ReviewStatus = "pending"
	StatusRunning               ReviewStatus = "running"
	StatusCompleted             ReviewStatus = "completed"
	StatusFailed                ReviewStatus = "failed"
	StatusCompletedWithWarnings ReviewStatus = "completed_with_warnings"
)

// Finding represents a single issue discovered during review.
type Finding struct {
	Severity         string  `json:"severity"`
	Category         string  `json:"category"`
	FilePath         string  `json:"file"`
	Line             int     `json:"line"`
	Title            string  `json:"title"`
	Evidence         string  `json:"evidence"`
	Recommendation   string  `json:"recommendation"`
	Confidence       float64 `json:"confidence"`
	Source           string  `json:"source"`
	RuleID           string  `json:"rule_id"`
	NeedsHumanReview bool    `json:"needs_human_review,omitempty"`
}

// SandboxRun records a single sandbox execution.
type SandboxRun struct {
	ID         string        `json:"id"`
	Command    string        `json:"command"`
	ExitCode   int           `json:"exit_code"`
	Stdout     string        `json:"stdout,omitempty"`
	Stderr     string        `json:"stderr,omitempty"`
	TimedOut   bool          `json:"timed_out"`
	Duration   time.Duration `json:"-"`
	DurationMs int64         `json:"duration_ms"`
	Error      string        `json:"error,omitempty"`
}

// PermissionDecision records a single governance decision.
type PermissionDecision struct {
	ToolName string `json:"tool_name"`
	Action   string `json:"action"`
	Reason   string `json:"reason,omitempty"`
}

// ReviewTask holds metadata for a complete review run.
type ReviewTask struct {
	ID                   string       `json:"id"`
	RepoPath             string       `json:"repo_path,omitempty"`
	DiffFile             string       `json:"diff_file,omitempty"`
	DiffSummary          string       `json:"diff_summary"`
	Status               ReviewStatus `json:"status"`
	DryRun               bool         `json:"dry_run"`
	SandboxType          string       `json:"sandbox_type"`
	TotalDurationMs      int64        `json:"total_duration_ms"`
	SandboxDurationMs    int64        `json:"sandbox_duration_ms"`
	ToolCallCount        int          `json:"tool_call_count"`
	PermissionDenyCount  int          `json:"permission_deny_count"`
	FindingsTotal        int          `json:"findings_total"`
	FindingsCritical     int          `json:"findings_critical"`
	FindingsHigh         int          `json:"findings_high"`
	FindingsMedium       int          `json:"findings_medium"`
	FindingsLow          int          `json:"findings_low"`
	FindingsWarning      int          `json:"findings_warning"`
	ErrorMessage         string       `json:"error_message,omitempty"`
	NeedHumanReviewCount int          `json:"need_human_review_count"`
	CreatedAt            time.Time    `json:"created_at"`
	CompletedAt          *time.Time   `json:"completed_at,omitempty"`
}

// ReviewReport is the top-level structured output.
type ReviewReport struct {
	Task                ReviewTask           `json:"task"`
	Findings            []Finding            `json:"findings"`
	SandboxRuns         []SandboxRun         `json:"sandbox_runs"`
	PermissionDecisions []PermissionDecision `json:"permission_decisions"`
	Summary             ReportSummary        `json:"summary"`
	GeneratedAt         time.Time            `json:"generated_at"`
}

// ReportSummary holds aggregate statistics.
type ReportSummary struct {
	TotalFiles        int               `json:"total_files"`
	TotalHunks        int               `json:"total_hunks"`
	SeverityCounts    map[string]int    `json:"severity_counts"`
	CategoryCounts    map[string]int    `json:"category_counts"`
	HumanReviewItems  []string          `json:"human_review_items,omitempty"`
	GovernanceSummary string            `json:"governance_summary,omitempty"`
	SandboxSummary    string            `json:"sandbox_summary,omitempty"`
	Monitoring        MonitoringSummary `json:"monitoring"`
}

// MonitoringSummary holds telemetry/audit data.
type MonitoringSummary struct {
	TotalDurationMs     int64    `json:"total_duration_ms"`
	SandboxDurationMs   int64    `json:"sandbox_duration_ms"`
	ToolCallCount       int      `json:"tool_call_count"`
	PermissionDenyCount int      `json:"permission_deny_count"`
	AnomalyTypes        []string `json:"anomaly_types,omitempty"`
}
