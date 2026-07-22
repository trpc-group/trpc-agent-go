//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package store defines persistence contracts for code reviews.
package store

import (
	"context"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
)

var (
	// ErrNotFound reports an unknown review task.
	ErrNotFound = errors.New("review task not found")
	// ErrInvalidTransition reports a non-running or invalid terminal state.
	ErrInvalidTransition = errors.New("invalid review task transition")
)

// TaskStatus is a terminal-safe review state.
type TaskStatus string

const (
	// StatusRunning is the only non-terminal state.
	StatusRunning TaskStatus = "running"
	// StatusCompleted is a successful review without infrastructure warnings.
	StatusCompleted TaskStatus = "completed"
	// StatusCompletedWithWarnings preserves partial sandbox failures.
	StatusCompletedWithWarnings TaskStatus = "completed_with_warnings"
	// StatusFailed is an infrastructure or input failure.
	StatusFailed TaskStatus = "failed"
)

// Task is the durable review lifecycle root.
type Task struct {
	ID          string     `json:"id"`
	Status      TaskStatus `json:"status"`
	InputKind   string     `json:"input_kind"`
	InputDigest string     `json:"input_digest"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Conclusion  string     `json:"conclusion,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// InputSummary is bounded metadata; it never contains the original diff.
type InputSummary struct {
	FileCount  int      `json:"file_count"`
	HunkCount  int      `json:"hunk_count"`
	AddedLines int      `json:"added_lines"`
	Packages   []string `json:"packages"`
}

// SandboxRun records one governed check.
type SandboxRun struct {
	ID              string `json:"id"`
	CheckID         string `json:"check_id"`
	Runtime         string `json:"runtime"`
	Status          string `json:"status"`
	DurationMS      int64  `json:"duration_ms"`
	ExitCode        int    `json:"exit_code"`
	TimedOut        bool   `json:"timed_out"`
	OutputTruncated bool   `json:"output_truncated"`
	Stdout          string `json:"stdout,omitempty"`
	Stderr          string `json:"stderr,omitempty"`
	ErrorType       string `json:"error_type,omitempty"`
	Error           string `json:"error,omitempty"`
}

// Decision records durable filter or permission evidence.
type Decision struct {
	ID         string    `json:"id"`
	Stage      string    `json:"stage"`
	Tool       string    `json:"tool"`
	CheckID    string    `json:"check_id"`
	ArgsDigest string    `json:"args_digest"`
	Risk       string    `json:"risk"`
	Action     string    `json:"action"`
	Reason     string    `json:"reason"`
	At         time.Time `json:"at"`
}

// Metrics is the stable audit summary persisted independently of telemetry.
type Metrics struct {
	TotalDurationMS   int64          `json:"total_duration_ms"`
	SandboxDurationMS int64          `json:"sandbox_duration_ms"`
	ToolCalls         int            `json:"tool_calls"`
	PermissionBlocks  int            `json:"permission_blocks"`
	FindingCount      int            `json:"finding_count"`
	SeverityCounts    map[string]int `json:"severity_counts"`
	ErrorTypeCounts   map[string]int `json:"error_type_counts"`
}

// Artifact is a bounded external output reference.
type Artifact struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id,omitempty"`
	Kind      string    `json:"kind"`
	Path      string    `json:"path"`
	SHA256    string    `json:"sha256"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// Report stores canonical rendered output and optional external copies.
type Report struct {
	SchemaVersion  string `json:"schema_version"`
	Conclusion     string `json:"conclusion"`
	JSON           string `json:"json"`
	Markdown       string `json:"markdown"`
	JSONPath       string `json:"json_path"`
	JSONSHA256     string `json:"json_sha256"`
	MarkdownPath   string `json:"markdown_path"`
	MarkdownSHA256 string `json:"markdown_sha256"`
}

// FinalizeRequest atomically persists results and a terminal success state.
// Status must be StatusCompleted or StatusCompletedWithWarnings; any other
// status causes Finalize to return ErrInvalidTransition.
type FinalizeRequest struct {
	TaskID     string
	Status     TaskStatus
	Conclusion string
	Findings   []reviewmodel.Finding
	Metrics    Metrics
	Artifacts  []Artifact
	Report     Report
	FinishedAt time.Time
}

// FailRequest moves a running task to failed.
type FailRequest struct {
	TaskID, Error string
	FinishedAt    time.Time
	Metrics       Metrics
}

// Review is the complete replayable aggregate returned by task ID.
type Review struct {
	Task      Task                  `json:"task"`
	Input     InputSummary          `json:"input"`
	Runs      []SandboxRun          `json:"sandbox_runs"`
	Decisions []Decision            `json:"governance_decisions"`
	Findings  []reviewmodel.Finding `json:"findings"`
	Metrics   Metrics               `json:"metrics"`
	Artifacts []Artifact            `json:"artifacts"`
	Report    Report                `json:"report"`
}

// Store supports a replaceable SQL-backed review lifecycle.
type Store interface {
	CreateTask(context.Context, Task) error
	SaveInputSummary(context.Context, string, InputSummary) error
	SaveRun(context.Context, string, SandboxRun) error
	SaveDecision(context.Context, string, Decision) error
	Finalize(context.Context, FinalizeRequest) error
	FailTask(context.Context, FailRequest) error
	GetReview(context.Context, string) (Review, error)
	Close() error
}
