//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "time"

const (
	severityCritical = "critical"
	severityHigh     = "high"
	severityMedium   = "medium"
	severityLow      = "low"
)

const (
	taskStatusCompleted = "completed"
	taskStatusFailed    = "failed"
)

// ReviewOptions configures one code review run.
type ReviewOptions struct {
	DiffFile          string
	RepoPath          string
	FileList          string
	Fixture           string
	FixtureDir        string
	OutDir            string
	DBPath            string
	Runtime           string
	AllowTrustedLocal bool
	DryRun            bool
	SandboxTimeout    time.Duration
	OutputLimit       int64
	SkillsRoot        string
	MaxDiffLines      int
	MaxChangedFiles   int
}

// ReviewTask captures the persisted task identity and input summary.
type ReviewTask struct {
	ID          string    `json:"id"`
	InputKind   string    `json:"input_kind"`
	DiffHash    string    `json:"diff_hash"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

// ChangedFile is a file touched by the parsed diff.
type ChangedFile struct {
	OldPath string `json:"old_path,omitempty"`
	NewPath string `json:"new_path,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

// AddedLine is one added line in a unified diff.
type AddedLine struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// PackageInfo describes a Go package touched by the diff.
type PackageInfo struct {
	Dir     string `json:"dir"`
	Name    string `json:"name"`
	GoFiles int    `json:"go_files"`
}

// DiffSummary is the deterministic representation used by rules.
type DiffSummary struct {
	Raw        string        `json:"-"`
	Hash       string        `json:"hash"`
	Files      []ChangedFile `json:"files"`
	AddedLines []AddedLine   `json:"added_lines"`
	Packages   []PackageInfo `json:"packages"`
	LineCount  int           `json:"line_count"`
}

// Finding is the public structured code review result.
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

// PermissionRecord records one command governance decision.
type PermissionRecord struct {
	TaskID    string    `json:"task_id"`
	ToolName  string    `json:"tool_name"`
	Command   string    `json:"command"`
	Action    string    `json:"action"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// FilterRecord records deterministic input or governance filter decisions.
type FilterRecord struct {
	TaskID    string    `json:"task_id"`
	Filter    string    `json:"filter"`
	Action    string    `json:"action"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SandboxRun records one sandbox command execution.
type SandboxRun struct {
	TaskID      string        `json:"task_id"`
	Runtime     string        `json:"runtime"`
	Command     string        `json:"command"`
	Status      string        `json:"status"`
	ExitCode    int           `json:"exit_code"`
	Output      string        `json:"output,omitempty"`
	Duration    time.Duration `json:"duration"`
	TimedOut    bool          `json:"timed_out"`
	Truncated   bool          `json:"truncated"`
	ErrorType   string        `json:"error_type,omitempty"`
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt time.Time     `json:"completed_at"`
}

// ArtifactRecord records generated review artifacts with scoped paths.
type ArtifactRecord struct {
	TaskID    string    `json:"task_id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	MIMEType  string    `json:"mime_type"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// Metrics summarizes review observability data.
type Metrics struct {
	TotalDurationMS       int64          `json:"total_duration_ms"`
	SandboxDurationMS     int64          `json:"sandbox_duration_ms"`
	ToolCalls             int            `json:"tool_calls"`
	PermissionBlocks      int            `json:"permission_blocks"`
	FindingCount          int            `json:"finding_count"`
	WarningCount          int            `json:"warning_count"`
	NeedsHumanReviewCount int            `json:"needs_human_review_count"`
	SeverityCounts        map[string]int `json:"severity_counts"`
	ErrorCounts           map[string]int `json:"error_counts"`
}

// ReviewReport is written as JSON and rendered to Markdown.
type ReviewReport struct {
	Task              ReviewTask         `json:"task"`
	Input             DiffSummary        `json:"input"`
	Findings          []Finding          `json:"findings"`
	Warnings          []Finding          `json:"warnings"`
	NeedsHumanReview  []Finding          `json:"needs_human_review"`
	PermissionSummary []PermissionRecord `json:"permission_summary"`
	FilterSummary     []FilterRecord     `json:"filter_summary"`
	SandboxRuns       []SandboxRun       `json:"sandbox_runs"`
	Artifacts         []ArtifactRecord   `json:"artifacts"`
	Metrics           Metrics            `json:"metrics"`
	Conclusion        string             `json:"conclusion"`
}
