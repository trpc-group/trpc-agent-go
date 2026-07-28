//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package storage defines the storage interface for code review data.
// Implementations include SQLite and can be extended to other SQL backends.
package storage

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

// TaskStats contains updatable statistics for a review task.
type TaskStats struct {
	FindingCount      int
	HighRiskCount     int
	MediumRiskCount   int
	LowRiskCount      int
	WarningCount      int
	PermissionDenied  int
	PermissionAsked   int
	TotalDurationMs   int64
	SandboxDurationMs int64
	ToolCallCount     int
}

// Store is the storage interface for code review data.
// It is designed to support SQLite as the default implementation,
// with the interface abstracted enough to switch to other SQL backends.
type Store interface {
	// Task operations.
	CreateTask(ctx context.Context, task *finding.ReviewTask) error
	GetTask(ctx context.Context, taskID string) (*finding.ReviewTask, error)
	UpdateTaskStatus(ctx context.Context, taskID string, status string, errMsg string) error
	UpdateTaskStats(ctx context.Context, taskID string, stats TaskStats) error
	ListTasks(ctx context.Context, limit, offset int) ([]*finding.ReviewTask, error)

	// Finding operations.
	CreateFindings(ctx context.Context, findings []*finding.Finding) error
	GetFindings(ctx context.Context, taskID string, severities ...finding.Severity) ([]*finding.Finding, error)
	CountFindings(ctx context.Context, taskID string) (int, error)
	CheckDuplicate(ctx context.Context, taskID, file string, line int, ruleID string) (bool, error)

	// SandboxRun operations.
	CreateSandboxRun(ctx context.Context, run *finding.SandboxRun) error
	GetSandboxRuns(ctx context.Context, taskID string) ([]*finding.SandboxRun, error)

	// Permission operations.
	CreatePermissionDecision(ctx context.Context, pd *finding.PermissionDecision) error
	GetPermissionDecisions(ctx context.Context, taskID string) ([]*finding.PermissionDecision, error)

	// Report operations.
	SaveReport(ctx context.Context, taskID, reportType, content string) error
	GetReport(ctx context.Context, taskID, reportType string) (string, error)

	// Close releases store resources.
	Close() error
}
