//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package storage 定义与具体 SQL 后端无关的持久化边界。
package storage

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

// Task 是审查任务。
type Task struct {
	ID          string
	InputType   string
	InputRef    string
	InputDigest string
	RepoPath    string
	Status      string
	Mode        string
	CreatedAt   time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
}

// ReportRecord 保存报告内容。
type ReportRecord struct {
	JSON      []byte
	Markdown  []byte
	CreatedAt time.Time
}

// DecisionRecord 是权限决策记录。
type DecisionRecord struct {
	TaskID  string
	Command string
	Action  string
	Reason  string
	At      time.Time
}

// FilterDecisionRecord 是过滤决策记录。
type FilterDecisionRecord struct {
	TaskID string
	Target string
	Action string
	Reason string
	At     time.Time
}

// SandboxRunRecord 是沙箱运行记录。
type SandboxRunRecord struct {
	TaskID           string
	Command          string
	Runtime          string
	Status           string
	TimeoutMS        int64
	OutputLimitBytes int
	EnvWhitelist     string
	ExitCode         int
	StdoutDigest     string
	StderrDigest     string
	DurationMS       int64
	Output           string
	At               time.Time
	FinishedAt       time.Time
	ArtifactCount    int
	// ExecutionStarted is an in-memory signal; persisted status records the outcome.
	ExecutionStarted bool
}

// ArtifactRecord 是产物引用记录。
type ArtifactRecord struct {
	TaskID string
	Name   string
	Kind   string
	Path   string
	Digest string
	Size   int64
	At     time.Time
}

// MetricsRecord 是聚合指标记录及查询结果。
type MetricsRecord struct {
	TaskID               string
	Mode                 *string
	SandboxRequested     *bool
	SandboxExecuted      *bool
	ModelRequested       *bool
	ModelExecuted        *bool
	TotalDurationMS      int64
	SandboxDurationMS    int64
	ModelDurationMS      int64
	ToolCallCount        int
	ModelCallCount       int
	ModelProvider        string
	ModelName            string
	ModelBackend         string
	PermissionBlockCount int
	FindingCount         int
	ModelFindingCount    int
	ModelExceptionCount  int
	SeverityCountsJSON   string
	ExceptionCountsJSON  string
	RedactionCount       int
	At                   time.Time
}

// ReviewRecord 是一次审查需要原子保存的完整记录。
type ReviewRecord struct {
	Task            Task
	Decisions       []DecisionRecord
	FilterDecisions []FilterDecisionRecord
	SandboxRuns     []SandboxRunRecord
	Findings        []review.Finding
	Metrics         MetricsRecord
	Artifacts       []ArtifactRecord
	Report          ReportRecord
}

// Store 定义最小存储能力。细粒度方法保留给任务状态和兼容查询写入；
// 完整审查必须通过 SaveReview 原子提交。
type Store interface {
	SaveTask(context.Context, Task) error
	SaveFinding(context.Context, string, review.Finding) error
	SaveDecision(context.Context, DecisionRecord) error
	SaveFilterDecision(context.Context, FilterDecisionRecord) error
	SaveSandboxRun(context.Context, SandboxRunRecord) error
	SaveArtifact(context.Context, ArtifactRecord) error
	SaveMetrics(context.Context, MetricsRecord) error
	SaveReport(context.Context, string, []byte, []byte) error
	SaveReview(context.Context, ReviewRecord) error
	Close() error
}
