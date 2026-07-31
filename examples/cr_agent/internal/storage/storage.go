//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package storage defines the persistence contract for the CR agent.
//
// The Store interface captures every read/write operation the review
// pipeline performs against durable storage so implementations can be
// swapped (SQLite for local/dev, Postgres/MySQL for service deployments,
// a memory fake for tests) without touching pipeline code.
package storage

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/types"
)

// Store is the persistence contract for CR agent review tasks.
//
// Implementations must be safe for concurrent use from multiple
// goroutines. Contexts passed to the methods should be respected for
// cancellation and deadline semantics; implementations should prefer
// the context-aware variants of their underlying driver APIs where
// available.
type Store interface {
	// Init prepares the underlying storage: opening connections,
	// creating or migrating schemas, and validating connectivity.
	//
	// Init must be called exactly once before any other method and is
	// not safe to call concurrently. A Store returned by a constructor
	// may not be fully ready until Init returns nil.
	Init(ctx context.Context) error

	// SaveTask inserts or updates a ReviewTask.
	//
	// The task's ID field must already be set. Implementations should
	// perform an upsert keyed on ID so callers can use SaveTask both
	// for initial creation and for transitioning Status/metrics as
	// the review progresses.
	SaveTask(ctx context.Context, task *types.ReviewTask) error

	// GetTask retrieves a task by ID.
	//
	// Returns a wrapped, inspectable "not found" error when no task
	// with the given ID exists. Implementations should not return a
	// partially-populated task on error.
	GetTask(ctx context.Context, id string) (*types.ReviewTask, error)

	// ListTasks returns a paginated slice of tasks in reverse
	// creation order (newest first).
	//
	// limit is the maximum number of tasks to return. offset is the
	// number of tasks to skip from the head of the ordered set.
	// Callers should treat the returned slice as read-only.
	ListTasks(ctx context.Context, limit, offset int) ([]*types.ReviewTask, error)

	// SaveSandboxRun persists a sandbox invocation record.
	//
	// The run's ID and TaskID must already be set. Implementations
	// should index TaskID to make GetSandboxRuns efficient.
	SaveSandboxRun(ctx context.Context, run *types.SandboxRun) error

	// GetSandboxRuns returns every SandboxRun attached to the given
	// task ID, ordered by CreatedAt ascending (oldest first).
	//
	// Returns an empty slice with no error when no runs exist for
	// the task.
	GetSandboxRuns(ctx context.Context, taskID string) ([]*types.SandboxRun, error)

	// SaveFinding attaches a Finding to the given task ID.
	//
	// The finding's ID must already be set; SaveFinding may fill in
	// TaskID on the struct for convenience but is not required to.
	SaveFinding(ctx context.Context, taskID string, f *types.Finding) error

	// GetFindings returns every Finding attached to the given task
	// ID, ordered by severity (critical first) then by CreatedAt
	// ascending.
	//
	// Returns an empty slice with no error when no findings exist
	// for the task.
	GetFindings(ctx context.Context, taskID string) ([]*types.Finding, error)

	// Close releases any underlying resources held by the Store
	// (connections, file handles, etc.).
	//
	// Close must be safe to call after Init, including when Init
	// returned an error. Subsequent calls to Close should be no-ops
	// returning nil. Behaviour of other methods after Close is
	// undefined.
	Close() error
}
