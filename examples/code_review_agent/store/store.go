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
	// SaveReview atomically stores all final review audit records. Implementations
	// must commit every category or none of them. The task must already exist,
	// and callers must invoke SaveReview at most once for a task.
	SaveReview(ctx context.Context, taskID string, report review.ReviewReport,
		artifacts []review.Artifact, jsonPath, markdownPath string) error
	// GetTask returns a full persisted snapshot for one task ID.
	GetTask(ctx context.Context, taskID string) (review.TaskSnapshot, error)
	// Close releases the underlying storage resources.
	Close() error
}

// Compile-time check that the SQLite implementation satisfies Store.
var _ Store = (*SQLiteStore)(nil)
