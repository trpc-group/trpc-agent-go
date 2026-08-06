//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package findings defines review finding models and helpers.
package findings

// Finding is a structured code review issue.
// 风险记录格式
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

// PermissionDecision is a governance gate record for reports.
type PermissionDecision struct {
	ToolName string `json:"tool_name"`
	Command  string `json:"command"`
	Action   string `json:"action"`
	Reason   string `json:"reason,omitempty"`
}

// SandboxRunSummary is a sandbox execution record for reports.
type SandboxRunSummary struct {
	Command    string `json:"command"`
	Runtime    string `json:"runtime"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int    `json:"duration_ms"`
	ErrorType  string `json:"error_type,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
}

// ReviewMetrics captures monitoring summary for a review task.
// 统计信息
type ReviewMetrics struct {
	TotalDurationMs     int            `json:"total_duration_ms"`
	SandboxDurationMs   int            `json:"sandbox_duration_ms"`
	FindingCount        int            `json:"finding_count"`
	WarningCount        int            `json:"warning_count"`
	ToolCallCount       int            `json:"tool_call_count"`
	PermissionDenyCount int            `json:"permission_deny_count"`
	SeverityCounts      map[string]int `json:"severity_counts"`
	ExceptionCounts     map[string]int `json:"exception_counts,omitempty"`
}

// ReviewResult is the final structured output of a review run.
// 输出结果
type ReviewResult struct {
	TaskID              string               `json:"task_id"`
	Status              string               `json:"status"`
	InputSummary        string               `json:"input_summary"`
	RepoPath            string               `json:"repo_path,omitempty"`
	Findings            []Finding            `json:"findings"`
	Warnings            []Finding            `json:"warnings"`
	PermissionDecisions []PermissionDecision `json:"permission_decisions,omitempty"`
	SandboxRuns         []SandboxRunSummary  `json:"sandbox_runs,omitempty"`
	Metrics             ReviewMetrics        `json:"metrics"`
	DryRun              bool                 `json:"dry_run"`
	SandboxRuntime      string               `json:"sandbox_runtime,omitempty"`
}
