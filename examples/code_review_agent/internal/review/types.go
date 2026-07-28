//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	// TaskFailed indicates that review input or execution failed after audit
	// persistence was initialized.
	TaskFailed TaskStatus = "failed"

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

// Config configures a review pipeline run. Supply exactly one primary input:
// DiffFile, RepoPath, FileList, or Fixture; FileList may additionally use
// RepoPath to read the named files. TaskID is optional; when set it must
// contain at most 80 ASCII letters, digits, hyphens, or underscores and cannot
// look like a credential. Empty values select the documented safe defaults: a
// container executor, a 45-second timeout, a 64 KiB output limit, output/ for
// reports, and OutputDir/reviews.sqlite for persistence.
type Config struct {
	// TaskID is the queryable task identity. An empty value generates a random ID.
	TaskID string
	// DiffFile is a unified-diff file input.
	DiffFile string
	// RepoPath selects Git diff input from a repository path.
	RepoPath string
	// FileList is a newline-delimited list of files to review.
	FileList string
	// Fixture selects a deterministic example fixture for testing.
	Fixture string
	// OutputDir is the parent directory for the published task directory.
	OutputDir string
	// DatabasePath is the SQLite audit database path.
	DatabasePath string
	// Executor selects the sandbox backend; local development fallback requires AllowLocal.
	Executor Executor
	// AllowLocal explicitly permits the local development fallback executor.
	AllowLocal bool
	// DryRun selects deterministic execution without launching sandbox commands.
	DryRun bool
	// FakeModel enables deterministic model-like fixture behavior.
	FakeModel bool
	// Timeout bounds each sandbox command; non-positive values use the default.
	Timeout time.Duration
	// OutputLimit bounds each captured sandbox stream; non-positive values use the default.
	OutputLimit int
	// StoreFactory creates the persistence adapter used by Run. The returned
	// Store is owned and closed by Run. Nil selects the SQLite adapter.
	StoreFactory StoreFactory
}

// Task records the identity, input mode, and lifecycle of a review. EndedAt is
// the terminal timestamp in the canonical Store record. Immutable report files
// retain a pre-publication snapshot; callers that need the final timestamp
// must query the Store by task ID.
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
	Hunk    int    `json:"hunk"`
}

// DiffHunk preserves one unified-diff hunk and its bounded review context.
type DiffHunk struct {
	File     string        `json:"file"`
	OldStart int           `json:"old_start"`
	OldLines int           `json:"old_lines"`
	NewStart int           `json:"new_start"`
	NewLines int           `json:"new_lines"`
	Package  string        `json:"package,omitempty"`
	Lines    []ChangedLine `json:"lines"`
	Context  string        `json:"-"`
}

// ParsedInput contains the bounded diff data consumed by review rules.
type ParsedInput struct {
	Raw      string                `json:"-"`
	Files    []string              `json:"files"`
	Statuses map[string]FileStatus `json:"-"`
	Lines    []ChangedLine         `json:"lines"`
	Hunks    []DiffHunk            `json:"hunks"`
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

// Artifact describes a bounded file produced by the review pipeline. Path is a
// slash-separated, task-directory-relative path safe to store portably.
// Provenance is "validated_sandbox", "synthetic_dry_run", or another explicit
// fallback source; consumers must not treat synthetic artifacts as script output.
type Artifact struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	MIMEType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	// Provenance identifies whether an artifact was produced by a validated
	// sandbox script or synthesized by an explicitly documented fallback.
	Provenance string `json:"provenance,omitempty"`
	content    string
}

// Metrics contains monitoring and audit counters for a review.
type Metrics struct {
	// PreparationDurationMS measures completed work before the final report
	// directory is atomically published. Publication is intentionally excluded
	// because the report itself contains this metric.
	PreparationDurationMS int64 `json:"preparation_duration_ms"`
	// FinalizationDurationMS measures the first terminal Store transaction that
	// makes a completed review durable after report publication.
	FinalizationDurationMS int64 `json:"finalization_duration_ms"`
	// VerificationDurationMS measures the durable Store read and consistency
	// checks performed after terminal finalization.
	VerificationDurationMS int64 `json:"verification_duration_ms"`
	// TotalDurationMS measures completed review work through the first durable
	// Store finalization and its read-back verification. The derived timing
	// fields are then persisted in a separate metadata update, which cannot be
	// included in the value it stores.
	TotalDurationMS int64 `json:"total_duration_ms"`
	// TotalDurationScope identifies the completed phases included in
	// TotalDurationMS: "pre_publication_snapshot",
	// "verified_before_metric_persistence", "recovered_publication", or
	// "failure_audit". Query the finalized SQLite record for the canonical
	// value rather than treating an immutable report artifact's pre-publication
	// snapshot as final.
	TotalDurationScope   string         `json:"total_duration_scope"`
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

// ReportPaths identifies the published JSON and Markdown reports. The paths
// are rooted at Config.OutputDir (and therefore are relative when OutputDir is
// relative); they exist only after Run returns successfully.
type ReportPaths struct {
	JSON     string
	Markdown string
}
