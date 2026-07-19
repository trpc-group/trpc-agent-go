//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package review implements an auditable, sandboxed code review pipeline.
package review

import "time"

// TaskStatus describes the lifecycle state of a review task.
type TaskStatus string

// Severity describes the impact of a review finding.
type Severity string

// PermissionAction describes a command-governance decision.
type PermissionAction string

// RunStatus describes the outcome of a sandbox command.
type RunStatus string

// ErrorType classifies a sandbox execution failure.
type ErrorType string

// ExecutionMode identifies how review findings were produced.
type ExecutionMode string

// Executor identifies a supported sandbox backend.
type Executor string

// FilterAction describes how a finding candidate was routed.
type FilterAction string

// FileStatus describes how a path changed in the reviewed diff.
type FileStatus string

const (
	// TaskRunning indicates that a review is in progress.
	TaskRunning TaskStatus = "running"
	// TaskCompleted indicates that a review finished and was persisted.
	TaskCompleted TaskStatus = "completed"

	// SeverityCritical identifies a merge-blocking security or correctness risk.
	SeverityCritical Severity = "critical"
	// SeverityHigh identifies an issue that should be fixed before merge.
	SeverityHigh Severity = "high"
	// SeverityMedium identifies a material but non-critical issue.
	SeverityMedium Severity = "medium"
	// SeverityLow identifies an informational or low-confidence issue.
	SeverityLow Severity = "low"

	// PermissionAllow permits an allowlisted command to execute.
	PermissionAllow PermissionAction = "allow"
	// PermissionDeny blocks a command from executing.
	PermissionDeny PermissionAction = "deny"
	// PermissionAsk records that explicit approval is required.
	PermissionAsk PermissionAction = "ask"

	// RunSuccess indicates that a sandbox command completed successfully.
	RunSuccess RunStatus = "success"
	// RunFailed indicates that a sandbox command failed.
	RunFailed RunStatus = "failed"
	// RunSkipped indicates that a sandbox command was intentionally not run.
	RunSkipped RunStatus = "skipped"

	// ErrorDryRun records a check skipped by deterministic dry-run mode.
	ErrorDryRun ErrorType = "dry_run"
	// ErrorExecutor records an executor-level failure.
	ErrorExecutor ErrorType = "executor_error"
	// ErrorPermissionDecision records a command blocked by governance.
	ErrorPermissionDecision ErrorType = "permission_decision"
	// ErrorNonZeroExit records a completed command with an unsuccessful exit code.
	ErrorNonZeroExit ErrorType = "non_zero_exit"
	// ErrorTimeout records an execution deadline.
	ErrorTimeout ErrorType = "timeout"
	// ErrorToolUnavailable records an optional executable missing from the sandbox.
	ErrorToolUnavailable ErrorType = "tool_unavailable"
	// ErrorDependencyUnavailable records dependencies absent from the offline sandbox cache.
	ErrorDependencyUnavailable ErrorType = "dependency_unavailable"
	// ErrorSetup records a sandbox lifecycle or staging failure.
	ErrorSetup ErrorType = "setup_error"

	// ExecutorContainer selects the local container sandbox backend.
	ExecutorContainer Executor = "container"
	// ExecutorE2B selects the E2B sandbox backend.
	ExecutorE2B Executor = "e2b"
	// ExecutorLocal selects the explicitly enabled local backend.
	ExecutorLocal Executor = "local"
	// ExecutorLocalDev identifies execution through the local development fallback.
	ExecutorLocalDev Executor = "local-dev-fallback"
	// ExecutorFake selects deterministic execution without running commands.
	ExecutorFake Executor = "fake"
	// ExecutorFakeFailure selects deterministic execution with an injected failure.
	ExecutorFakeFailure Executor = "fake-fail"

	// FilterKeep retains a candidate in its target report bucket.
	FilterKeep FilterAction = "keep"
	// FilterDropDuplicate drops a lower-priority duplicate candidate.
	FilterDropDuplicate FilterAction = "drop_duplicate"
	// FilterRouteHuman sends a candidate to human review.
	FilterRouteHuman FilterAction = "route_human"

	fileAdded    FileStatus = "added"
	fileModified FileStatus = "modified"
	fileDeleted  FileStatus = "deleted"
)

// Config configures a review pipeline run.
type Config struct {
	TaskID       string
	DiffFile     string
	RepoPath     string
	FileList     string
	Fixture      string
	OutputDir    string
	DatabasePath string
	Executor     Executor
	AllowLocal   bool
	DryRun       bool
	FakeModel    bool
	Timeout      time.Duration
	OutputLimit  int
}

// Task records the identity, input mode, and lifecycle of a review.
type Task struct {
	ID        string     `json:"id"`
	Status    TaskStatus `json:"status"`
	InputMode string     `json:"input_mode"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   time.Time  `json:"ended_at"`
}

// DiffSummary records bounded aggregate information about the reviewed change.
type DiffSummary struct {
	Digest       string `json:"digest"`
	FilesChanged int    `json:"files_changed"`
	GoFiles      int    `json:"go_files"`
	AddedLines   int    `json:"added_lines"`
	DeletedLines int    `json:"deleted_lines"`
}

// ChangedLine represents an added line and its source location.
type ChangedLine struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Text    string `json:"text"`
	Package string `json:"package,omitempty"`
}

// ParsedInput contains the bounded diff data consumed by review rules.
type ParsedInput struct {
	Raw      string                `json:"-"`
	Files    []string              `json:"files"`
	Statuses map[string]FileStatus `json:"-"`
	Lines    []ChangedLine         `json:"lines"`
	Context  map[string]string     `json:"-"`
	Summary  DiffSummary           `json:"summary"`
}

// Finding is a structured code review observation.
type Finding struct {
	Severity       Severity `json:"severity"`
	Category       string   `json:"category"`
	File           string   `json:"file"`
	Line           int      `json:"line"`
	Title          string   `json:"title"`
	Evidence       string   `json:"evidence"`
	Recommendation string   `json:"recommendation"`
	Confidence     float64  `json:"confidence"`
	Source         string   `json:"source"`
	RuleID         string   `json:"rule_id"`
	Fingerprint    string   `json:"fingerprint"`
}

// PermissionDecision records whether a command was allowed, denied, or deferred.
type PermissionDecision struct {
	Command   string           `json:"command"`
	Action    PermissionAction `json:"action"`
	Reason    string           `json:"reason,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
}

// FilterDecision records how a finding candidate was retained or suppressed.
type FilterDecision struct {
	Fingerprint  string       `json:"fingerprint"`
	Action       FilterAction `json:"action"`
	Reason       string       `json:"reason"`
	TargetBucket string       `json:"target_bucket,omitempty"`
}

// SandboxRun records the auditable outcome of one sandbox command.
type SandboxRun struct {
	Command         string        `json:"command"`
	Args            []string      `json:"args"`
	Executor        Executor      `json:"executor"`
	Status          RunStatus     `json:"status"`
	ExitCode        int           `json:"exit_code"`
	Stdout          string        `json:"stdout,omitempty"`
	Stderr          string        `json:"stderr,omitempty"`
	ErrorType       ErrorType     `json:"error_type,omitempty"`
	Duration        time.Duration `json:"-"`
	DurationMS      int64         `json:"duration_ms"`
	TimedOut        bool          `json:"timed_out"`
	OutputTruncated bool          `json:"output_truncated"`
}

// Artifact describes a bounded file produced by the review pipeline.
type Artifact struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	MIMEType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
}

// Metrics contains monitoring and audit counters for a review.
type Metrics struct {
	TotalDurationMS      int64          `json:"total_duration_ms"`
	SandboxDurationMS    int64          `json:"sandbox_duration_ms"`
	ToolCallCount        int            `json:"tool_call_count"`
	PermissionDenyCount  int            `json:"permission_deny_count"`
	PermissionAskCount   int            `json:"permission_ask_count"`
	FindingCount         int            `json:"finding_count"`
	WarningCount         int            `json:"warning_count"`
	NeedsHumanCount      int            `json:"needs_human_review_count"`
	SeverityDistribution map[string]int `json:"severity_distribution"`
	ErrorDistribution    map[string]int `json:"error_distribution"`
}

// Report contains the complete structured result of a review task.
type Report struct {
	Task                Task                 `json:"task"`
	Input               DiffSummary          `json:"input"`
	Findings            []Finding            `json:"findings"`
	Warnings            []Finding            `json:"warnings"`
	NeedsHumanReview    []Finding            `json:"needs_human_review"`
	SandboxRuns         []SandboxRun         `json:"sandbox_runs"`
	PermissionDecisions []PermissionDecision `json:"permission_decisions"`
	FilterDecisions     []FilterDecision     `json:"filter_decisions"`
	Artifacts           []Artifact           `json:"artifacts"`
	Metrics             Metrics              `json:"metrics"`
	Conclusion          string               `json:"conclusion"`
	Mode                ExecutionMode        `json:"mode"`
}

// ReportPaths identifies the published JSON and Markdown reports.
type ReportPaths struct {
	JSON     string
	Markdown string
}
