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
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/internal/clone"
)

// Manager implements the evalset.Manager interface using in-memory storage.
//
// The manager keeps an in-memory copy of all eval sets. Each API returns
// deep-cloned objects to avoid accidental mutation by callers.
type manager struct {
	mu        sync.RWMutex
	evalSets  map[string]map[string]*evalset.EvalSet
	evalCases map[string]map[string]map[string]*evalset.EvalCase
}

// New creates a new in-memory evaluation set manager.
func New() evalset.Manager {
	return &manager{
		evalSets:  make(map[string]map[string]*evalset.EvalSet),
		evalCases: make(map[string]map[string]map[string]*evalset.EvalCase),
	}
}

func (m *manager) ensureApp(appName string) {
	if _, ok := m.evalSets[appName]; !ok {
		m.evalSets[appName] = make(map[string]*evalset.EvalSet)
		m.evalCases[appName] = make(map[string]map[string]*evalset.EvalCase)
	}
}

// Get returns an EvalSet identified by evalSetID. If the set does not exist,
// os.ErrNotExist is returned.
func (m *manager) Get(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()
	setsByApp, ok := m.evalSets[appName]
	if !ok {
		return nil, fmt.Errorf("%w: eval set %s", os.ErrNotExist, evalSetID)
	}
	es, ok := setsByApp[evalSetID]
	if !ok {
		return nil, fmt.Errorf("%w: eval set %s", os.ErrNotExist, evalSetID)
	}
	cloned, err := clone.CloneEvalSet(es)
	if err != nil {
		return nil, fmt.Errorf("clone eval set %s: %w", evalSetID, err)
	}
	return cloned, nil
}

// Create creates and returns an empty EvalSet given the evalSetID. If the set
// already exists, a cloned copy is returned.
func (m *manager) Create(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureApp(appName)
	if es, ok := m.evalSets[appName][evalSetID]; ok {
		cloned, err := clone.CloneEvalSet(es)
		if err != nil {
			return nil, fmt.Errorf("clone eval set %s: %w", evalSetID, err)
		}
		return cloned, nil
	}
	es := &evalset.EvalSet{
		EvalSetID:         evalSetID,
		EvalCases:         []*evalset.EvalCase{},
		CreationTimestamp: time.Now().UTC(),
	}
	m.evalSets[appName][evalSetID] = es
	m.evalCases[appName][evalSetID] = make(map[string]*evalset.EvalCase)
	cloned, err := clone.CloneEvalSet(es)
	if err != nil {
		return nil, fmt.Errorf("clone eval set %s: %w", evalSetID, err)
	}
	return cloned, nil
}

// GetCase returns an EvalCase if found, otherwise an error.
func (m *manager) GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()
	casesByApp, ok := m.evalCases[appName]
	if !ok {
		return nil, fmt.Errorf("%w: eval set %s", os.ErrNotExist, evalSetID)
	}
	casesBySet, ok := casesByApp[evalSetID]
	if !ok {
		return nil, fmt.Errorf("%w: eval set %s", os.ErrNotExist, evalSetID)
	}
	casePtr, ok := casesBySet[evalCaseID]
	if !ok {
		return nil, fmt.Errorf("%w: eval case %s", os.ErrNotExist, evalCaseID)
	}
	cloned, err := clone.CloneEvalCase(casePtr)
	if err != nil {
		return nil, fmt.Errorf("clone eval case %s: %w", evalCaseID, err)
	}
	return cloned, nil
}

// AddCase adds the given EvalCase to an existing EvalSet identified by evalSetID.
func (m *manager) AddCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	_ = ctx
	if evalCase == nil {
		return errors.New("evalCase is nil")
	}
	if evalCase.EvalID == "" {
		return errors.New("evalCase.EvalID is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureApp(appName)
	es, ok := m.evalSets[appName][evalSetID]
	if !ok {
		return fmt.Errorf("%w: eval set %s", os.ErrNotExist, evalSetID)
	}
	if _, exists := m.evalCases[appName][evalSetID][evalCase.EvalID]; exists {
		return fmt.Errorf("eval case %s.%s.%s already exists", appName, evalSetID, evalCase.EvalID)
	}
	cloned, err := clone.CloneEvalCase(evalCase)
	if err != nil {
		return fmt.Errorf("clone eval case %s: %w", evalCase.EvalID, err)
	}
	m.evalCases[appName][evalSetID][evalCase.EvalID] = cloned
	es.EvalCases = append(es.EvalCases, cloned)
	return nil
}

// UpdateCase updates an existing EvalCase given the evalSetID.
func (m *manager) UpdateCase(ctx context.Context, appName, evalSetID string, updatedEvalCase *evalset.EvalCase) error {
	_ = ctx
	if updatedEvalCase == nil {
		return errors.New("updatedEvalCase is nil")
	}
	if updatedEvalCase.EvalID == "" {
		return errors.New("updatedEvalCase.EvalID is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureApp(appName)
	es, ok := m.evalSets[appName][evalSetID]
	if !ok {
		return fmt.Errorf("%w: eval set %s", os.ErrNotExist, evalSetID)
	}
	if _, exists := m.evalCases[appName][evalSetID][updatedEvalCase.EvalID]; !exists {
		return fmt.Errorf("%w: eval case %s", os.ErrNotExist, updatedEvalCase.EvalID)
	}
	cloned, err := clone.CloneEvalCase(updatedEvalCase)
	if err != nil {
		return fmt.Errorf("clone eval case %s: %w", updatedEvalCase.EvalID, err)
	}
	m.evalCases[appName][evalSetID][updatedEvalCase.EvalID] = cloned
	for i, c := range es.EvalCases {
		if c != nil && c.EvalID == updatedEvalCase.EvalID {
			es.EvalCases[i] = cloned
			return nil
		}
	}
	return fmt.Errorf("%w: eval case %s", os.ErrNotExist, updatedEvalCase.EvalID)
}

// DeleteCase deletes the given EvalCase identified by evalSetID and evalCaseID.
func (m *manager) DeleteCase(ctx context.Context, appName, evalSetID, evalCaseID string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureApp(appName)
	es, ok := m.evalSets[appName][evalSetID]
	if !ok {
		return fmt.Errorf("%w: eval set %s", os.ErrNotExist, evalSetID)
	}
	if _, exists := m.evalCases[appName][evalSetID][evalCaseID]; !exists {
		return fmt.Errorf("%w: eval case %s", os.ErrNotExist, evalCaseID)
	}
	delete(m.evalCases[appName][evalSetID], evalCaseID)
	filtered := es.EvalCases[:0]
	for _, c := range es.EvalCases {
		if c != nil && c.EvalID != evalCaseID {
			filtered = append(filtered, c)
		}
	}
	es.EvalCases = filtered
	return nil
}
