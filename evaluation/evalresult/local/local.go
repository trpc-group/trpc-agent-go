//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package local provides a local file storage implementation for evaluation results.
package local

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
)

// Manager implements the evalresult.Manager interface using local file storage.
type Manager struct {
}

// NewManager creates a new local file evaluation result manager.
func NewManager() *Manager {
	return &Manager{}
}

// Save stores an evaluation result to local file.
func (m *Manager) Save(ctx context.Context, result *evalresult.EvalSetResult) error {
	// Implementation would go here
	return nil
}

// Get retrieves an evaluation result by evalSetResultID from local file.
func (m *Manager) Get(ctx context.Context, evalSetResultID string) (*evalresult.EvalSetResult, error) {
	// Implementation would go here
	return nil, nil
}

// List returns all available evaluation results from local files.
func (m *Manager) List(ctx context.Context) ([]*evalresult.EvalSetResult, error) {
	// Implementation would go here
	return nil, nil
}
