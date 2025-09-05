//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides a in-memory storage implementation for evaluation sets.
package inmemory

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

// Manager implements the evalset.Manager interface using in-memory storage.
type Manager struct {
}

// NewManager creates a new in-memory evaluation set manager.
func NewManager() *Manager {
	return &Manager{}
}

// Get returns an EvalSet identified by evalSetID.
func (m *Manager) Get(ctx context.Context, evalSetID string) (*evalset.EvalSet, error) {
	// Implementation would go here
	return nil, nil
}

// Create creates and returns an empty EvalSet given the evalSetID.
func (m *Manager) Create(ctx context.Context, evalSetID string) (*evalset.EvalSet, error) {
	// Implementation would go here
	return nil, nil
}

// GetCase returns an EvalCase if found, otherwise nil.
func (m *Manager) GetCase(ctx context.Context, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	// Implementation would go here
	return nil, nil
}

// AddCase adds the given EvalCase to an existing EvalSet identified by evalSetID.
func (m *Manager) AddCase(ctx context.Context, evalSetID string, evalCase *evalset.EvalCase) error {
	// Implementation would go here
	return nil
}

// UpdateCase updates an existing EvalCase given the evalSetID.
func (m *Manager) UpdateCase(ctx context.Context, evalSetID string, updatedEvalCase *evalset.EvalCase) error {
	// Implementation would go here
	return nil
}

// DeleteCase deletes the given EvalCase identified by evalSetID and evalCaseID.
func (m *Manager) DeleteCase(ctx context.Context, evalSetID, evalCaseID string) error {
	// Implementation would go here
	return nil
}
