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

// ReviewMetrics captures monitoring summary for a review task.
// 统计信息
type ReviewMetrics struct {
	TotalDurationMs int            `json:"total_duration_ms"`
	FindingCount    int            `json:"finding_count"`
	WarningCount    int            `json:"warning_count"`
	SeverityCounts  map[string]int `json:"severity_counts"`
}

// ReviewResult is the final structured output of a review run.
// 输出结果
type ReviewResult struct {
	TaskID       string        `json:"task_id"`
	Status       string        `json:"status"`
	InputSummary string        `json:"input_summary"`
	RepoPath     string        `json:"repo_path,omitempty"`
	Findings     []Finding     `json:"findings"`
	Warnings     []Finding     `json:"warnings"`
	Metrics      ReviewMetrics `json:"metrics"`
	DryRun       bool          `json:"dry_run"`
}
