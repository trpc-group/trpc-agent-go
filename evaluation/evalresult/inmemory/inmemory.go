//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides an in-memory storage evaluation result manager implementation.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/clone"
)

// manager implements evalresult.Manager backed by in-memory.
// Each API returns deep-copied objects to avoid accidental mutation.
type manager struct {
	mu             sync.RWMutex
	evalSetResults map[string]map[string]*evalresult.EvalSetResult // appName -> evalSetResultID -> EvalSetResult.
}

// New creates a in-memory evaluation result manager.
func New() evalresult.Manager {
	return &manager{
		evalSetResults: make(map[string]map[string]*evalresult.EvalSetResult),
	}
}

// Save stores a evaluation result keyed by EvalSetResultID.
// If the eval set result id is empty, it will be generated.
// Returns an error if the app name is empty or the eval set result is nil or the eval set id is empty.
func (m *manager) Save(_ context.Context, appName string, evalSetResult *evalresult.EvalSetResult) (string, error) {
	if appName == "" {
		return "", errors.New("app name is empty")
	}
	if evalSetResult == nil {
		return "", errors.New("eval set result is nil")
	}
	if evalSetResult.EvalSetID == "" {
		return "", errors.New("the eval set id of eval set result is empty")
	}
	evalSetResultID := evalSetResult.EvalSetResultID
	if evalSetResultID == "" {
		evalSetResultID = fmt.Sprintf("%s_%s_%s", appName, evalSetResult.EvalSetID, uuid.New().String())
	}
	cloned, err := clone.Clone(evalSetResult)
	if err != nil {
		return "", fmt.Errorf("clone result: %w", err)
	}
	cloned.EvalSetResultID = evalSetResultID
	if cloned.EvalSetResultName == "" {
		cloned.EvalSetResultName = evalSetResultID
	}
	if cloned.CreationTimestamp == nil {
		cloned.CreationTimestamp = &epochtime.EpochTime{Time: time.Now()}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.evalSetResults[appName]; !ok {
		m.evalSetResults[appName] = make(map[string]*evalresult.EvalSetResult)
	}
	m.evalSetResults[appName][evalSetResultID] = cloned
	return evalSetResultID, nil
}

// Get retrieves evaluation result by evalSetResultID.
func (m *manager) Get(_ context.Context, appName, evalSetResultID string) (*evalresult.EvalSetResult, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	if evalSetResultID == "" {
		return nil, errors.New("eval set result id is empty")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	evalSetResults, ok := m.evalSetResults[appName]
	if !ok {
		return nil, fmt.Errorf("app %s not found: %w", appName, os.ErrNotExist)
	}
	evalSetResult, ok := evalSetResults[evalSetResultID]
	if !ok {
		return nil, fmt.Errorf("eval set result %s.%s not found: %w", appName, evalSetResultID, os.ErrNotExist)
	}
	cloned, err := clone.Clone(evalSetResult)
	if err != nil {
		return nil, fmt.Errorf("clone eval set result %s.%s: %w", appName, evalSetResultID, err)
	}
	return cloned, nil
}

// List returns all stored evaluation results.
func (m *manager) List(_ context.Context, appName string) ([]string, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	evalSetResults, ok := m.evalSetResults[appName]
	if !ok {
		return []string{}, nil
	}
	evalSetResultIDs := make([]string, 0, len(evalSetResults))
	for id := range evalSetResults {
		evalSetResultIDs = append(evalSetResultIDs, id)
	}
	return evalSetResultIDs, nil
}
