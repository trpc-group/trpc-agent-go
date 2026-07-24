//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package store

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

// Store is the persistence contract for review audit data. SQLite is the
// default implementation; another SQL backend can replace it by
// implementing this interface.
type Store interface {
	// CreateTask inserts a new review task.
	CreateTask(ctx context.Context, task review.ReviewTask) error
	// FinishTask marks a task as completed or failed.
	FinishTask(ctx context.Context, task review.ReviewTask) error
	// SaveFindings stores findings for a task.
	SaveFindings(ctx context.Context, taskID string, findings []review.Finding) error
	// SaveSandboxRuns stores sandbox runs.
	SaveSandboxRuns(ctx context.Context, taskID string, runs []review.SandboxRun) error
	// SavePermissionDecisions stores command governance decisions.
	SavePermissionDecisions(ctx context.Context, taskID string, decisions []review.PermissionDecision) error
	// SaveFilterDecisions stores noise-control filter decisions.
	SaveFilterDecisions(ctx context.Context, taskID string, decisions []review.FilterDecision) error
	// SaveArtifacts stores artifacts produced by the review.
	SaveArtifacts(ctx context.Context, taskID string, artifacts []review.Artifact) error
	// SaveReport stores the final report metadata and summary.
	SaveReport(ctx context.Context, taskID string, report review.ReviewReport, jsonPath, markdownPath string) error
	// CountFindings returns the number of stored findings.
	CountFindings(ctx context.Context, taskID string) (int, error)
	// GetTask returns a full persisted snapshot for one task ID.
	GetTask(ctx context.Context, taskID string) (review.TaskSnapshot, error)
	// Close releases the underlying storage resources.
	Close() error
}

// Compile-time check that the SQLite implementation satisfies Store.
var _ Store = (*SQLiteStore)(nil)
