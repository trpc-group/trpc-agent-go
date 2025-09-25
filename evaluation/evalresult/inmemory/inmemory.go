//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides an in-memory storage implementation for evaluation results.
package inmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
)

// manager implements evalresult.Manager backed by process memory.
type manager struct {
	mu      sync.RWMutex
	results map[string]*evalresult.EvalSetResult
}

// NewManager creates a new in-memory evaluation result manager.
func NewManager() evalresult.Manager {
	return &manager{
		results: make(map[string]*evalresult.EvalSetResult),
	}
}

// Save stores a deep-copied evaluation result keyed by EvalSetResultID.
func (m *manager) Save(ctx context.Context, result *evalresult.EvalSetResult) error {
	_ = ctx
	if result == nil {
		return errors.New("result is nil")
	}
	if result.EvalSetResultID == "" {
		return errors.New("result id is empty")
	}

	cloned, err := cloneEvalSetResult(result)
	if err != nil {
		return fmt.Errorf("clone result: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[result.EvalSetResultID] = cloned
	return nil
}

// Get retrieves a copied evaluation result by its identifier.
func (m *manager) Get(ctx context.Context, evalSetResultID string) (*evalresult.EvalSetResult, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()

	stored, ok := m.results[evalSetResultID]
	if !ok {
		return nil, fmt.Errorf("%w: eval set result %s", os.ErrNotExist, evalSetResultID)
	}
	cloned, err := cloneEvalSetResult(stored)
	if err != nil {
		return nil, fmt.Errorf("clone result: %w", err)
	}
	return cloned, nil
}

// List returns copies of all stored evaluation results.
func (m *manager) List(ctx context.Context) ([]*evalresult.EvalSetResult, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make([]*evalresult.EvalSetResult, 0, len(m.results))
	for _, stored := range m.results {
		cloned, err := cloneEvalSetResult(stored)
		if err != nil {
			return nil, fmt.Errorf("clone result: %w", err)
		}
		results = append(results, cloned)
	}
	return results, nil
}

// cloneEvalSetResult performs a deep copy using JSON round-tripping to avoid shared references.
func cloneEvalSetResult(result *evalresult.EvalSetResult) (*evalresult.EvalSetResult, error) {
	if result == nil {
		return nil, errors.New("result is nil")
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var cloned evalresult.EvalSetResult
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}
