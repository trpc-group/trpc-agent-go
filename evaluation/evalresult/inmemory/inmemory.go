//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides a in-memory storage implementation for evaluation results.
package inmemory

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
)

// Manager implements the evalresult.Manager interface using in-memory storage.
type Manager struct {
}

// NewManager creates a new in-memory evaluation result manager.
func NewManager() *Manager {
	return &Manager{}
}

// Save stores an evaluation result in memory.
func (m *Manager) Save(ctx context.Context, result *evalresult.EvalSetResult) error {
	// Implementation would go here
	return nil
}

// Get retrieves an evaluation result by evalSetResultID from memory.
func (m *Manager) Get(ctx context.Context, evalSetResultID string) (*evalresult.EvalSetResult, error) {
	// Implementation would go here
	return nil, nil
}

// List returns all available evaluation results from memory.
func (m *Manager) List(ctx context.Context) ([]*evalresult.EvalSetResult, error) {
	// Implementation would go here
	return nil, nil
}
