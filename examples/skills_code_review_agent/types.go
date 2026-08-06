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

	sourceRule       = "rule"
	sourcePermission = "permission"
)

// DiffLine is one line in a unified diff hunk.
type DiffLine struct {
	Kind    byte
	Text    string
	OldLine int
	NewLine int
}

// Hunk is one parsed unified diff hunk.
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Header   string
	Lines    []DiffLine
}

// ChangedFile describes one file in a unified diff.
type ChangedFile struct {
	OldPath string
	Path    string
	Package string
	Hunks   []Hunk
}

// ParsedDiff contains normalized files and hunks.
type ParsedDiff struct {
	Files []ChangedFile
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

// InputSummary is the persisted, redacted description of review input.
type InputSummary struct {
	Kind            string   `json:"kind"`
	SHA256          string   `json:"sha256"`
	Bytes           int      `json:"bytes"`
	ChangedFiles    []string `json:"changed_files"`
	GoPackages      []string `json:"go_packages"`
	RedactedPreview string   `json:"redacted_preview,omitempty"`
}

// PermissionDecision records a governance decision before a command runs.
type PermissionDecision struct {
	Tool      string    `json:"tool"`
	Command   string    `json:"command"`
	Action    string    `json:"action"`
	Reason    string    `json:"reason,omitempty"`
	Risk      string    `json:"risk"`
	CreatedAt time.Time `json:"created_at"`
}

// SandboxRun records one bounded command execution.
type SandboxRun struct {
	Command      string `json:"command"`
	Status       string `json:"status"`
	ExitCode     int    `json:"exit_code"`
	DurationMS   int64  `json:"duration_ms"`
	TimedOut     bool   `json:"timed_out"`
	Output       string `json:"output,omitempty"`
	ErrorType    string `json:"error_type,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// ArtifactRecord describes a bounded report artifact.
type ArtifactRecord struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

// Metrics contains review telemetry and audit counters.
type Metrics struct {
	TotalDurationMS   int64          `json:"total_duration_ms"`
	SandboxDurationMS int64          `json:"sandbox_duration_ms"`
	ToolCalls         int            `json:"tool_calls"`
	PermissionBlocked int            `json:"permission_blocked"`
	FindingCount      int            `json:"finding_count"`
	WarningCount      int            `json:"warning_count"`
	Severity          map[string]int `json:"severity"`
	Errors            map[string]int `json:"errors"`
}

// ReviewReport is the complete redacted output of one review.
type ReviewReport struct {
	TaskID           string               `json:"task_id"`
	Status           string               `json:"status"`
	Conclusion       string               `json:"conclusion"`
	Mode             string               `json:"mode"`
	Runtime          string               `json:"runtime"`
	Skill            string               `json:"skill"`
	StartedAt        time.Time            `json:"started_at"`
	CompletedAt      time.Time            `json:"completed_at"`
	Input            InputSummary         `json:"input"`
	Findings         []Finding            `json:"findings"`
	Warnings         []Finding            `json:"warnings"`
	NeedsHumanReview []Finding            `json:"needs_human_review"`
	Decisions        []PermissionDecision `json:"permission_decisions"`
	SandboxRuns      []SandboxRun         `json:"sandbox_runs"`
	Artifacts        []ArtifactRecord     `json:"artifacts"`
	Metrics          Metrics              `json:"metrics"`
}

// ReviewRequest configures one deterministic review.
type ReviewRequest struct {
	Diff           []byte
	InputKind      string
	RepoPath       string
	Runtime        string
	DryRun         bool
	FakeModel      bool
	RunStaticcheck bool
	OutputDir      string
}
