//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import "time"

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

type ReviewStatus string

const (
	StatusCompleted ReviewStatus = "completed"
	StatusFailed    ReviewStatus = "failed"
)

type ReviewConfig struct {
	DiffFile           string
	RepoPath           string
	FileList           string
	Fixture            string
	ContainerSmoke     bool
	OutputDir          string
	DBPath             string
	Executor           string
	ContainerBaseImage string
	InstallStaticcheck bool
	AllowLocalFallback bool
	DryRun             bool
	RuleOnly           bool
	FakeModel          bool
	LLMReview          bool
	ModelProvider      string
	Model              string
	ModelBaseURL       string
	Timeout            time.Duration
	OutputLimitBytes   int
}

type ReviewTask struct {
	ID        string       `json:"id"`
	Status    ReviewStatus `json:"status"`
	StartedAt time.Time    `json:"started_at"`
	EndedAt   time.Time    `json:"ended_at"`
	InputMode string       `json:"input_mode"`
}

type ParsedDiff struct {
	RawHash  string          `json:"raw_hash"`
	Files    []DiffFile      `json:"files"`
	Hunks    []DiffHunk      `json:"hunks"`
	Summary  DiffSummary     `json:"summary"`
	Packages []GoPackageInfo `json:"packages"`
	Raw      string          `json:"-"`
}

type DiffSummary struct {
	FilesChanged int `json:"files_changed"`
	GoFiles      int `json:"go_files"`
	AddedLines   int `json:"added_lines"`
	DeletedLines int `json:"deleted_lines"`
}

type DiffFile struct {
	OldPath     string `json:"old_path"`
	NewPath     string `json:"new_path"`
	IsGo        bool   `json:"is_go"`
	IsTest      bool   `json:"is_test"`
	PackageName string `json:"package_name,omitempty"`
	PackagePath string `json:"package_path,omitempty"`
}

type GoPackageInfo struct {
	PackagePath string   `json:"package_path"`
	PackageName string   `json:"package_name,omitempty"`
	Files       []string `json:"files"`
}

type DiffHunk struct {
	File     string     `json:"file"`
	OldStart int        `json:"old_start"`
	OldCount int        `json:"old_count"`
	NewStart int        `json:"new_start"`
	NewCount int        `json:"new_count"`
	Lines    []DiffLine `json:"lines"`
}

type DiffLine struct {
	Kind    byte   `json:"kind"`
	OldLine int    `json:"old_line,omitempty"`
	NewLine int    `json:"new_line,omitempty"`
	Text    string `json:"text"`
}

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
	Fingerprint    string   `json:"fingerprint,omitempty"`
}

type SandboxRun struct {
	ID              string    `json:"id"`
	TaskID          string    `json:"task_id"`
	Command         string    `json:"command"`
	Args            []string  `json:"args,omitempty"`
	Executor        string    `json:"executor"`
	Status          string    `json:"status"`
	ExitCode        int       `json:"exit_code"`
	Stdout          string    `json:"stdout,omitempty"`
	Stderr          string    `json:"stderr,omitempty"`
	ErrorType       string    `json:"error_type,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	DurationMS      int64     `json:"duration_ms"`
	TimedOut        bool      `json:"timed_out"`
	OutputTruncated bool      `json:"output_truncated"`
}

type SandboxResult struct {
	Runs        []SandboxRun               `json:"runs"`
	Decisions   []PermissionDecisionRecord `json:"decisions"`
	Findings    []Finding                  `json:"findings"`
	Artifacts   []ArtifactRecord           `json:"artifacts"`
	SkillLoaded bool                       `json:"skill_loaded"`
}

type PermissionDecisionRecord struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	Tool        string    `json:"tool"`
	Command     string    `json:"command"`
	Action      string    `json:"action"`
	Disposition string    `json:"disposition"`
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type PermissionSummary struct {
	AllowCount            int `json:"allow_count"`
	DenyCount             int `json:"deny_count"`
	AskCount              int `json:"ask_count"`
	NeedsHumanReviewCount int `json:"needs_human_review_count"`
}

type ArtifactRecord struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	MimeType  string    `json:"mime_type"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

type ArtifactPolicy struct {
	MaxArtifacts     int      `json:"max_artifacts"`
	MaxBytesPerFile  int64    `json:"max_bytes_per_file"`
	AllowedFileNames []string `json:"allowed_file_names"`
	RetainedCount    int      `json:"retained_count"`
	RejectedCount    int      `json:"rejected_count"`
}

type AuditMetrics struct {
	TotalDurationMS       int64          `json:"total_duration_ms"`
	SandboxDurationMS     int64          `json:"sandbox_duration_ms"`
	ToolCallCount         int            `json:"tool_call_count"`
	PermissionDenyCount   int            `json:"permission_deny_count"`
	PermissionAskCount    int            `json:"permission_ask_count"`
	FindingCount          int            `json:"finding_count"`
	WarningCount          int            `json:"warning_count"`
	NeedsHumanReviewCount int            `json:"needs_human_review_count"`
	SeverityCounts        map[string]int `json:"severity_counts"`
	ErrorTypeCounts       map[string]int `json:"error_type_counts"`
}

type ReviewReport struct {
	Task              ReviewTask                 `json:"task"`
	Input             DiffSummary                `json:"input"`
	Packages          []GoPackageInfo            `json:"packages"`
	Findings          []Finding                  `json:"findings"`
	Warnings          []Finding                  `json:"warnings"`
	NeedsHumanReview  []Finding                  `json:"needs_human_review"`
	SandboxRuns       []SandboxRun               `json:"sandbox_runs"`
	Permissions       []PermissionDecisionRecord `json:"permission_decisions"`
	PermissionSummary PermissionSummary          `json:"permission_summary"`
	Artifacts         []ArtifactRecord           `json:"artifacts"`
	ArtifactPolicy    ArtifactPolicy             `json:"artifact_policy"`
	Metrics           AuditMetrics               `json:"metrics"`
	Conclusion        string                     `json:"conclusion"`
}
