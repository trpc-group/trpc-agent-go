//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import "time"

const (
	TaskStatusRunning = "running"
	TaskStatusPassed  = "passed"
	TaskStatusFailed  = "failed"

	FindingStatusFinding          = "finding"
	FindingStatusWarning          = "warning"
	FindingStatusNeedsHumanReview = "needs_human_review"

	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"

	InputTypeFixture = "fixture"
	InputTypeDiff    = "diff"
)

// ReviewTask is the durable unit of work for a code review.
type ReviewTask struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	InputType  string    `json:"input_type"`
	RepoPath   string    `json:"repo_path,omitempty"`
	DiffHash   string    `json:"diff_hash"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// DiffFile is a parsed file entry from a unified diff.
type DiffFile struct {
	OldPath    string     `json:"old_path"`
	NewPath    string     `json:"new_path"`
	IsNew      bool       `json:"is_new"`
	IsDeleted  bool       `json:"is_deleted"`
	PackageDir string     `json:"package_dir,omitempty"`
	Hunks      []DiffHunk `json:"hunks"`
}

// DiffHunk is a parsed unified-diff hunk.
type DiffHunk struct {
	OldStart int        `json:"old_start"`
	OldLines int        `json:"old_lines"`
	NewStart int        `json:"new_start"`
	NewLines int        `json:"new_lines"`
	Lines    []DiffLine `json:"lines"`
}

// DiffLine is one parsed line in a hunk.
type DiffLine struct {
	Kind    string `json:"kind"`
	OldLine int    `json:"old_line,omitempty"`
	NewLine int    `json:"new_line,omitempty"`
	Content string `json:"content"`
}

// Finding is a structured code-review finding or routed warning.
type Finding struct {
	ID             string  `json:"id,omitempty"`
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
	Status         string  `json:"status"`
	Fingerprint    string  `json:"fingerprint,omitempty"`
}

// SandboxRun records the outcome of a command attempted in a workspace runtime.
type SandboxRun struct {
	ID              string `json:"id"`
	TaskID          string `json:"task_id"`
	Runtime         string `json:"runtime"`
	Command         string `json:"command"`
	Status          string `json:"status"`
	ExitCode        int    `json:"exit_code"`
	DurationMillis  int64  `json:"duration_ms"`
	StdoutRedacted  string `json:"stdout,omitempty"`
	StderrRedacted  string `json:"stderr,omitempty"`
	OutputTruncated bool   `json:"output_truncated"`
	ErrorType       string `json:"error_type,omitempty"`
}

// PermissionDecisionRecord preserves both framework and original safety decisions.
type PermissionDecisionRecord struct {
	ID              string    `json:"id"`
	TaskID          string    `json:"task_id"`
	ToolName        string    `json:"tool_name"`
	Command         string    `json:"command,omitempty"`
	FrameworkAction string    `json:"framework_action"`
	SafetyDecision  string    `json:"safety_decision"`
	RiskLevel       string    `json:"risk_level,omitempty"`
	RuleID          string    `json:"rule_id,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	Blocked         bool      `json:"blocked"`
	CreatedAt       time.Time `json:"created_at"`
}

// ArtifactRecord stores generated report metadata.
type ArtifactRecord struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Kind      string    `json:"kind"`
	Path      string    `json:"path"`
	MimeType  string    `json:"mime_type"`
	SHA256    string    `json:"sha256,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ReviewMetrics captures deterministic telemetry for a review run.
type ReviewMetrics struct {
	TaskID                   string         `json:"task_id"`
	TotalDurationMillis      int64          `json:"total_duration_ms"`
	SandboxDurationMillis    int64          `json:"sandbox_duration_ms"`
	ToolCallCount            int            `json:"tool_call_count"`
	PermissionBlockedCount   int            `json:"permission_blocked_count"`
	FindingCount             int            `json:"finding_count"`
	SeverityDistribution     map[string]int `json:"severity_distribution"`
	ErrorDistribution        map[string]int `json:"error_distribution"`
	RedactionCount           int            `json:"redaction_count"`
	SeverityDistributionJSON string         `json:"-"`
	ErrorDistributionJSON    string         `json:"-"`
}

// Report is the full rendered review result.
type Report struct {
	Task                ReviewTask                 `json:"task"`
	Summary             string                     `json:"summary"`
	ChangedFiles        []DiffFile                 `json:"changed_files"`
	Findings            []Finding                  `json:"findings"`
	SandboxRuns         []SandboxRun               `json:"sandbox_runs"`
	PermissionDecisions []PermissionDecisionRecord `json:"permission_decisions"`
	Artifacts           []ArtifactRecord           `json:"artifacts"`
	Metrics             ReviewMetrics              `json:"metrics"`
	Conclusion          string                     `json:"conclusion"`
}
