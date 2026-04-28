//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package store defines PromptIter run persistence contracts.
package store

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// Store stores persisted PromptIter runs.
type Store interface {
	// Create persists one new PromptIter run.
	Create(ctx context.Context, run *engine.RunResult) error
	// Get loads one persisted PromptIter run by run ID.
	Get(ctx context.Context, runID string) (*engine.RunResult, error)
	// Update persists changes to one existing PromptIter run.
	Update(ctx context.Context, run *engine.RunResult) error
	// Close releases store resources.
	Close() error
}
