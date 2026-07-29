//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package store defines the persistence interface and SQLite implementation
// for review tasks, findings, sandbox runs, and permission decisions.
package store

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
)

// ReviewStore is the persistence contract for review data.
type ReviewStore interface {
	// CreateTask inserts a new review task.
	CreateTask(ctx context.Context, task *reviewmodel.ReviewTask) error

	// SaveFinding persists a single finding for a task.
	SaveFinding(ctx context.Context, taskID string, finding *reviewmodel.Finding) error

	// SaveSandboxRun persists a single sandbox execution record.
	SaveSandboxRun(ctx context.Context, taskID string, run *reviewmodel.SandboxRun) error

	// SavePermissionDecision persists a single governance decision.
	SavePermissionDecision(ctx context.Context, taskID string, dec *reviewmodel.PermissionDecision) error

	// GetTask retrieves a review task by ID.
	GetTask(ctx context.Context, taskID string) (*reviewmodel.ReviewTask, error)

	// GetFindings retrieves all findings for a task.
	GetFindings(ctx context.Context, taskID string) ([]reviewmodel.Finding, error)

	// GetSandboxRuns retrieves all sandbox execution records for a task.
	GetSandboxRuns(ctx context.Context, taskID string) ([]reviewmodel.SandboxRun, error)

	// GetPermissionDecisions retrieves all governance decisions for a task.
	GetPermissionDecisions(ctx context.Context, taskID string) ([]reviewmodel.PermissionDecision, error)

	// Finalize marks a task as completed. Status must be StatusCompleted or
	// StatusCompletedWithWarnings; any other value returns ErrInvalidTransition.
	Finalize(ctx context.Context, taskID string, task *reviewmodel.ReviewTask) error

	// Close releases resources held by the store.
	Close() error
}
