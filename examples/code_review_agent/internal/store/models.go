//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package store defines the persistent storage types and contract for the
// code review agent.
//
// The domain types in this file map one-to-one onto the SQLite tables defined
// in schema.sql. Nullable database columns (sandbox_run.exit_code, .stdout,
// .stderr) are represented with sql.NullInt64 / sql.NullString so that the
// genuine SQL NULL is preserved instead of being collapsed into a 0/"" sentinel
// that could be confused with a real value.
package store

import "database/sql"

// ReviewTask is the top-level row for a single code review run. Every child
// table (finding, sandbox_run, ...) references this row via task_id.
type ReviewTask struct {
	TaskID            string
	CreatedAt         string
	RepoPath          string
	DiffSource        string
	Status            string
	Conclusion        string
	TotalDurationMs   int64
	SandboxDurationMs int64
}

// Finding is a single issue discovered during review. Fingerprint is unique
// per task and is used to deduplicate repeated detections of the same issue.
type Finding struct {
	ID             int64
	TaskID         string
	Severity       string
	Category       string
	File           string
	Line           int
	Title          string
	Evidence       string
	Recommendation string
	Confidence     float64
	Source         string
	RuleID         string
	Fingerprint    string
	CreatedAt      string
}

// SandboxRun captures the result of one command executed inside the sandbox.
// ExitCode, Stdout and Stderr are nullable because a timed-out command may
// never have produced a value for them.
type SandboxRun struct {
	ID         int64
	TaskID     string
	Command    string
	Status     string
	ExitCode   sql.NullInt64
	DurationMs int64
	TimedOut   bool
	Truncated  bool
	Stdout     sql.NullString
	Stderr     sql.NullString
	CreatedAt  string
}

// PermissionDecision records whether a potentially dangerous command was
// permitted or blocked and why.
type PermissionDecision struct {
	ID        int64
	TaskID    string
	Command   string
	Action    string
	Reason    string
	CreatedAt string
}

// Artifact is a file produced by a review run (e.g. the rendered report).
type Artifact struct {
	ID        int64
	TaskID    string
	Name      string
	Path      string
	SizeBytes int64
	CreatedAt string
}

// ReportRow stores the on-disk locations of the JSON and Markdown reports for
// a task. TaskID is UNIQUE so a task can have at most one report row.
type ReportRow struct {
	ID           int64
	TaskID       string
	JSONPath     string
	MarkdownPath string
	CreatedAt    string
}

// TelemetryMetrics is a denormalised snapshot of the per-task telemetry
// counters. TaskID is UNIQUE so a task can have at most one metrics row.
type TelemetryMetrics struct {
	ID                     int64
	TaskID                 string
	TotalDurationMs        int64
	SandboxDurationMs      int64
	ToolCalls              int64
	PermissionBlockedCount int64
	FindingCount           int64
	SeverityCritical       int64
	SeverityHigh           int64
	SeverityMedium         int64
	SeverityLow            int64
	CreatedAt              string
}

// TaskReport is the aggregate root returned by LoadTaskReport. It bundles a
// ReviewTask with every child row that references it.
type TaskReport struct {
	Task        ReviewTask
	Findings    []Finding
	SandboxRuns []SandboxRun
	Permissions []PermissionDecision
	Artifacts   []Artifact
	Report      ReportRow
	Metrics     TelemetryMetrics
}

// TaskSummary is the lightweight projection returned by ListTasks. FindingCount
// is the number of findings associated with the task.
type TaskSummary struct {
	TaskID       string
	CreatedAt    string
	Status       string
	Conclusion   string
	FindingCount int
}
