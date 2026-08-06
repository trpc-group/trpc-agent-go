//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package types defines the shared data structures for the CR agent,
// including severity/category enums, findings, review tasks, and reports.
package types

import "time"

// Severity classifies the impact level of a Finding.
type Severity string

const (
	// SeverityCritical indicates a finding that must be fixed before merge,
	// such as a severe security vulnerability or data loss condition.
	SeverityCritical Severity = "critical"

	// SeverityHigh indicates a finding with significant user-facing or
	// operational impact that should be fixed before merge.
	SeverityHigh Severity = "high"

	// SeverityMedium indicates a finding with moderate impact or a clear
	// quality issue that is recommended to be fixed.
	SeverityMedium Severity = "medium"

	// SeverityLow indicates a minor style, readability, or consistency
	// issue with negligible runtime impact.
	SeverityLow Severity = "low"

	// SeverityWarning indicates an informational signal that is not a
	// defect on its own but may warrant reviewer attention.
	SeverityWarning Severity = "warning"
)

// Category groups Findings by the type of problem they describe.
type Category string

const (
	// CategorySecurity covers secrets, injection, authz, crypto misuse,
	// and other security-sensitive patterns.
	CategorySecurity Category = "security"

	// CategoryGoroutineLeak covers unbounded goroutines, missing
	// cancellation, blocked sends/receives, and leaky goroutine patterns.
	CategoryGoroutineLeak Category = "goroutine_leak"

	// CategoryResourceClose covers unclosed files, sockets, DB
	// connections, HTTP bodies, and other leaky resources.
	CategoryResourceClose Category = "resource_close"

	// CategoryErrorHandling covers swallowed errors, missing error
	// checks, error shadowing, and improper error wrapping.
	CategoryErrorHandling Category = "error_handling"

	// CategoryMissingTest flags substantive logic changes that are not
	// accompanied by new or updated tests.
	CategoryMissingTest Category = "missing_test"

	// CategorySensitiveLeak covers accidental logging or persistence of
	// PII, credentials, tokens, and other sensitive data.
	CategorySensitiveLeak Category = "sensitive_leak"

	// CategoryDBLifecycle covers transaction, connection, and statement
	// lifecycle issues in database code.
	CategoryDBLifecycle Category = "db_lifecycle"
)

// Finding represents a single actionable issue detected during a code
// review run.
type Finding struct {
	// ID uniquely identifies the finding within a review task.
	ID string

	// Severity is the impact level of this finding.
	Severity Severity

	// Category groups the finding by problem type.
	Category Category

	// File is the repository-relative path of the affected file.
	File string

	// Line is the 1-based line number in File where the issue is
	// located; 0 when the finding spans the whole file or is not
	// line-anchored.
	Line int

	// Title is a short, human-readable summary of the finding.
	Title string

	// Evidence is the excerpt or rationale showing why the finding
	// was raised.
	Evidence string

	// Recommendation describes the suggested remediation in
	// actionable terms.
	Recommendation string

	// Confidence is the analyzer's confidence on a 0..1 scale.
	Confidence float64

	// Source identifies the analyzer or rule engine that produced
	// the finding (e.g. "staticcheck", "llm-review", "custom-rule").
	Source string

	// RuleID is the source-specific rule identifier that fired.
	RuleID string

	// CreatedAt is the time the finding was generated.
	CreatedAt time.Time

	// NeedsHumanReview flags findings the analyzer cannot self-certify
	// and that require a human reviewer to confirm or dismiss.
	NeedsHumanReview bool
}

// InputType distinguishes the shape of ReviewInput so downstream stages
// can pick the correct parser.
type InputType string

const (
	// InputTypeDiff means ReviewInput carries a raw unified diff in
	// DiffContent.
	InputTypeDiff InputType = "diff"

	// InputTypeFiles means ReviewInput carries explicit file paths in
	// FilePaths for full-file review.
	InputTypeFiles InputType = "files"

	// InputTypeGit means ReviewInput carries RepoPath and CommitRange
	// so the review pipeline can produce the diff itself.
	InputTypeGit InputType = "git"
)

// ReviewInput describes the work that a review task should process.
// Exactly one of the input shapes should be populated per task.
type ReviewInput struct {
	// Type selects which of the following fields is authoritative.
	Type InputType

	// DiffContent is populated when Type == InputTypeDiff and holds
	// the raw unified diff text.
	DiffContent string

	// FilePaths is populated when Type == InputTypeFiles and lists
	// repository-relative file paths to review in full.
	FilePaths []string

	// RepoPath is populated when Type == InputTypeGit and points at
	// the local repository root.
	RepoPath string

	// CommitRange is populated when Type == InputTypeGit and holds
	// the git revision range (e.g. "HEAD~3..HEAD" or
	// "origin/main...feature").
	CommitRange string
}

// DiffSummary captures lightweight statistics about the diff that was
// reviewed, suitable for list views and dashboards.
type DiffSummary struct {
	// AddedLines counts added lines in the diff.
	AddedLines int

	// DeletedLines counts deleted lines in the diff.
	DeletedLines int

	// FilesChanged counts the number of files touched by the diff.
	FilesChanged int

	// DiffHash is a stable hash of the normalized diff content, used
	// to de-duplicate repeat reviews.
	DiffHash string

	// DiffPreview is a short head excerpt of the diff, shown in list
	// UIs to help reviewers identify the task at a glance.
	DiffPreview string
}

// SandboxRun records a single tool invocation that was executed inside
// the isolated sandbox during a review. It is the primary audit trail
// for everything the CR agent did to a repository.
type SandboxRun struct {
	// ID uniquely identifies this sandbox invocation.
	ID string

	// TaskID is the ReviewTask that requested this run.
	TaskID string

	// ToolName is the CR-agent tool that executed the run
	// (e.g. "shell", "read-file", "run-tests").
	ToolName string

	// Command is the logical command or operation that was executed,
	// rendered for human audit.
	Command string

	// StdoutTruncated is the tail of stdout, truncated to the storage
	// budget.
	StdoutTruncated string

	// StderrTruncated is the tail of stderr, truncated to the storage
	// budget.
	StderrTruncated string

	// ExitCode is the process exit status; -1 when the run never
	// produced an exit code (e.g. timed out before exec).
	ExitCode int

	// DurationMs is the wall-clock duration of the run in
	// milliseconds.
	DurationMs int64

	// TimedOut reports whether the run was killed by the sandbox
	// timeout.
	TimedOut bool

	// OutputBytes counts the raw bytes produced by stdout + stderr
	// before truncation.
	OutputBytes int

	// CreatedAt is the time the run started.
	CreatedAt time.Time
}

// ReviewTaskStatus enumerates the lifecycle states of a ReviewTask.
type ReviewTaskStatus string

const (
	// StatusPending means the task has been created but not yet picked
	// up by a worker.
	StatusPending ReviewTaskStatus = "pending"

	// StatusRunning means a worker is actively reviewing the input.
	StatusRunning ReviewTaskStatus = "running"

	// StatusCompleted means the review produced a final report.
	StatusCompleted ReviewTaskStatus = "completed"

	// StatusFailed means the review aborted with an error captured in
	// ReviewTask.ErrorMsg.
	StatusFailed ReviewTaskStatus = "failed"
)

// ReviewTask is the persistent, queryable record of a single code
// review run, from submission through report generation.
type ReviewTask struct {
	// ID uniquely identifies the task.
	ID string

	// Status is the current lifecycle state.
	Status ReviewTaskStatus

	// Input is the lightweight diff summary shown in list views.
	Input DiffSummary

	// StartedAt is the time a worker claimed the task; zero when the
	// task is still pending.
	StartedAt time.Time

	// CompletedAt is the time the task transitioned to Completed or
	// Failed; nil while pending or running.
	CompletedAt *time.Time

	// TotalDurationMs is wall-clock time from StartedAt to
	// CompletedAt, in milliseconds.
	TotalDurationMs int64

	// SandboxDurationMs is the portion of TotalDurationMs spent
	// waiting on sandbox runs.
	SandboxDurationMs int64

	// ToolCalls counts how many agent tool calls were dispatched
	// during the review.
	ToolCalls int

	// PermissionDenials counts tool calls rejected by the CR agent's
	// permission policy.
	PermissionDenials int

	// FindingsCount counts findings attached to the task.
	FindingsCount int

	// WarningsCount counts non-fatal warnings attached to the task.
	WarningsCount int

	// ErrorMsg is populated when Status == StatusFailed and holds the
	// top-level failure message.
	ErrorMsg string

	// CreatedAt is the time the task was submitted.
	CreatedAt time.Time
}

// ReviewMetrics holds instrumentation counters from a single review,
// rendered into the report for operators and for offline analysis.
type ReviewMetrics struct {
	// TotalDurationMs is end-to-end latency for the full review.
	TotalDurationMs int64

	// SandboxDurationMs is time spent inside sandbox tool runs.
	SandboxDurationMs int64

	// ParseDurationMs is time spent parsing the input into the
	// internal diff representation.
	ParseDurationMs int64

	// ReviewDurationMs is time spent by the analyzer/LLM stage,
	// excluding sandbox time.
	ReviewDurationMs int64

	// ReportDurationMs is time spent serializing findings and
	// assembling the final report.
	ReportDurationMs int64

	// ToolCalls counts agent tool calls dispatched.
	ToolCalls int

	// SandboxRuns counts sandbox invocations recorded.
	SandboxRuns int

	// PermissionDenials counts rejected tool calls.
	PermissionDenials int

	// RulesEvaluated counts analyzer rules or LLM sub-checks that
	// executed.
	RulesEvaluated int
}

// ReviewReport is the final, self-contained output of a review run.
// It is the shape consumed by APIs, UIs, and export pipelines.
type ReviewReport struct {
	// TaskID links the report back to its originating ReviewTask.
	TaskID string

	// GeneratedAt is the time the report was finalized.
	GeneratedAt time.Time

	// Summary rolls up counts by severity and a few overall counters
	// so list views and badges can render without loading Findings.
	Summary struct {
		Critical         int
		High             int
		Medium           int
		Low              int
		Warning          int
		TotalFiles       int
		NeedsHumanReview int
	}

	// Findings is the list of detected issues, highest severity
	// first.
	Findings []Finding

	// Warnings holds non-fatal, non-finding signals from the review
	// pipeline (e.g. "file X skipped: binary", "LLM timed out on Y").
	Warnings []string

	// Metrics captures instrumentation counters for this review.
	Metrics ReviewMetrics
}
