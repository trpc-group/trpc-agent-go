//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides a in-memory manager implementation for evaluation sets.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/clone"
)

// Manager implements the evalset.Manager interface using in-memory manager.
// Each API returns deep-copied objects to avoid accidental mutation.
type manager struct {
	mu        sync.RWMutex
	evalSets  map[string]map[string]*evalset.EvalSet             // appName -> evalSetID -> EvalSet.
	evalCases map[string]map[string]map[string]*evalset.EvalCase // appName -> evalSetID -> evalCaseID -> EvalCase.
}

// New creates a in-memory evaluation set manager.
func New() evalset.Manager {
	return &manager{
		evalSets:  make(map[string]map[string]*evalset.EvalSet),
		evalCases: make(map[string]map[string]map[string]*evalset.EvalCase),
	}
}

// Get gets an EvalSet identified by evalSetID.
// Returns an error if the EvalSet does not exist.
func (m *manager) Get(_ context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	evalSet, err := m.loadEvalSet(appName, evalSetID)
	if err != nil {
		return nil, fmt.Errorf("load eval set %s.%s: %w", appName, evalSetID, err)
	}
	cloned, err := clone.Clone(evalSet)
	if err != nil {
		return nil, fmt.Errorf("clone eval set %s.%s: %w", appName, evalSetID, err)
	}
	return cloned, nil
}

// Create creates an EvalSet.
// Returns an error if the EvalSet already exists.
func (m *manager) Create(_ context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureAppExist(appName)
	if _, ok := m.evalSets[appName][evalSetID]; ok {
		return nil, fmt.Errorf("eval set %s.%s already exists", appName, evalSetID)
	}
	evalSet := &evalset.EvalSet{
		EvalSetID:         evalSetID,
		Name:              evalSetID,
		EvalCases:         []*evalset.EvalCase{},
		CreationTimestamp: &epochtime.EpochTime{Time: time.Now()},
	}
	m.evalSets[appName][evalSetID] = evalSet
	m.evalCases[appName][evalSetID] = make(map[string]*evalset.EvalCase)
	cloned, err := clone.Clone(evalSet)
	if err != nil {
		return nil, fmt.Errorf("clone eval set %s.%s: %w", appName, evalSetID, err)
	}
	return cloned, nil
}

// List lists all EvalSet IDs for the given appName.
// Returns an error if the appName does not exist.
func (m *manager) List(_ context.Context, appName string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.evalSets[appName]; !ok {
		return []string{}, nil
	}
	evalSetIDs := make([]string, 0, len(m.evalSets[appName]))
	for evalSetID := range m.evalSets[appName] {
		evalSetIDs = append(evalSetIDs, evalSetID)
	}
	return evalSetIDs, nil
}

// Delete deletes EvalSet identified by evalSetID.
// Returns an error if the EvalSet does not exist.
func (m *manager) Delete(_ context.Context, appName, evalSetID string) error {
	if appName == "" {
		return errors.New("app name is empty")
	}
	if evalSetID == "" {
		return errors.New("eval set id is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.evalSets[appName], evalSetID)
	delete(m.evalCases[appName], evalSetID)
	return nil
}

// GetCase gets an EvalCase.
// Returns an error if the EvalCase does not exist.
func (m *manager) GetCase(_ context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	evalCase, err := m.loadEvalCase(appName, evalSetID, evalCaseID)
	if err != nil {
		return nil, fmt.Errorf("load eval case %s.%s.%s: %w", appName, evalSetID, evalCaseID, err)
	}
	cloned, err := clone.Clone(evalCase)
	if err != nil {
		return nil, fmt.Errorf("clone eval case %s.%s.%s: %w", appName, evalSetID, evalCaseID, err)
	}
	return cloned, nil
}

// AddCase adds the given EvalCase to an existing EvalSet identified by evalSetID.
// Returns an error if the EvalSet does not exist or the EvalCase already exists.
func (m *manager) AddCase(_ context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	if evalCase == nil {
		return errors.New("evalCase is nil")
	}
	if evalCase.EvalID == "" {
		return errors.New("evalCase.EvalID is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureAppExist(appName)
	evalSet, err := m.loadEvalSet(appName, evalSetID)
	if err != nil {
		return fmt.Errorf("load eval set %s.%s: %w", appName, evalSetID, err)
	}
	if _, exists := m.evalCases[appName][evalSetID][evalCase.EvalID]; exists {
		return fmt.Errorf("eval case %s.%s.%s already exists", appName, evalSetID, evalCase.EvalID)
	}
	cloned, err := clone.Clone(evalCase)
	if err != nil {
		return fmt.Errorf("clone eval case %s.%s.%s: %w", appName, evalSetID, evalCase.EvalID, err)
	}
	if cloned.CreationTimestamp == nil {
		cloned.CreationTimestamp = &epochtime.EpochTime{Time: time.Now()}
	}
	for _, invocation := range cloned.Conversation {
		if invocation.CreationTimestamp == nil {
			invocation.CreationTimestamp = &epochtime.EpochTime{Time: time.Now()}
		}
	}
	m.evalCases[appName][evalSetID][evalCase.EvalID] = cloned
	evalSet.EvalCases = append(evalSet.EvalCases, cloned)
	return nil
}

// UpdateCase updates an existing EvalCase.
// Returns an error if the EvalSet does not exist or the EvalCase does not exist.
func (m *manager) UpdateCase(_ context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	if evalCase == nil {
		return errors.New("evalCase is nil")
	}
	if evalCase.EvalID == "" {
		return errors.New("evalCase.EvalID is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureAppExist(appName)
	evalSet, err := m.loadEvalSet(appName, evalSetID)
	if err != nil {
		return fmt.Errorf("load eval set %s.%s: %w", appName, evalSetID, err)
	}
	if _, exists := m.evalCases[appName][evalSetID][evalCase.EvalID]; !exists {
		return fmt.Errorf("eval case %s.%s.%s not found: %w", appName, evalSetID, evalCase.EvalID, os.ErrNotExist)
	}
	cloned, err := clone.Clone(evalCase)
	if err != nil {
		return fmt.Errorf("clone eval case %s.%s.%s: %w", appName, evalSetID, evalCase.EvalID, err)
	}
	m.evalCases[appName][evalSetID][evalCase.EvalID] = cloned
	for i, c := range evalSet.EvalCases {
		if c.EvalID == evalCase.EvalID {
			evalSet.EvalCases[i] = cloned
			return nil
		}
	}
	return fmt.Errorf("eval case %s.%s.%s not found: %w", appName, evalSetID, evalCase.EvalID, os.ErrNotExist)
}

// DeleteCase deletes the given EvalCase.
// Returns an error if the EvalSet does not exist or the EvalCase does not exist.
func (m *manager) DeleteCase(_ context.Context, appName, evalSetID, evalCaseID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureAppExist(appName)
	evalSet, err := m.loadEvalSet(appName, evalSetID)
	if err != nil {
		return fmt.Errorf("load eval set %s.%s: %w", appName, evalSetID, err)
	}
	if _, exists := m.evalCases[appName][evalSetID][evalCaseID]; !exists {
		return fmt.Errorf("eval case %s.%s.%s not found: %w", appName, evalSetID, evalCaseID, os.ErrNotExist)
	}
	delete(m.evalCases[appName][evalSetID], evalCaseID)
	filtered := evalSet.EvalCases[:0]
	for _, c := range evalSet.EvalCases {
		if c.EvalID != evalCaseID {
			filtered = append(filtered, c)
		}
	}
	evalSet.EvalCases = filtered
	return nil
}

// ensureAppExist ensures the app exists.
func (m *manager) ensureAppExist(appName string) {
	if _, ok := m.evalSets[appName]; !ok {
		m.evalSets[appName] = make(map[string]*evalset.EvalSet)
		m.evalCases[appName] = make(map[string]map[string]*evalset.EvalCase)
	}
}

// loadEvalSet loads the EvalSet.
func (m *manager) loadEvalSet(appName, evalSetID string) (*evalset.EvalSet, error) {
	evalSets, ok := m.evalSets[appName]
	if !ok {
		return nil, fmt.Errorf("app %s not found: %w", appName, os.ErrNotExist)
	}
	evalSet, ok := evalSets[evalSetID]
	if !ok {
		return nil, fmt.Errorf("eval set %s.%s not found: %w", appName, evalSetID, os.ErrNotExist)
	}
	return evalSet, nil
}

// loadEvalCase loads the EvalCase.
func (m *manager) loadEvalCase(appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	evalCasesBySet, ok := m.evalCases[appName]
	if !ok {
		return nil, fmt.Errorf("app %s not found: %w", appName, os.ErrNotExist)
	}
	cases, ok := evalCasesBySet[evalSetID]
	if !ok {
		return nil, fmt.Errorf("eval set %s.%s not found: %w", appName, evalSetID, os.ErrNotExist)
	}
	evalCase, ok := cases[evalCaseID]
	if !ok {
		return nil, fmt.Errorf("eval case %s.%s.%s not found: %w", appName, evalSetID, evalCaseID, os.ErrNotExist)
	}
	return evalCase, nil
}
