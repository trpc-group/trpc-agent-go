//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package local provides a local file storage implementation for evaluation sets.
package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/internal/clone"
)

// manager implements evalset.Manager backed by the local filesystem.
type manager struct {
	mu       sync.RWMutex
	baseDir  string
	pathFunc evalset.PathFunc
}

// NewManager creates a new local file evaluation set manager.
// Use functional options defined in option.go; defaults mirror the Python implementation.
func NewManager(opt ...evalset.Option) evalset.Manager {
	opts := evalset.NewOptions(opt...)
	return &manager{
		baseDir:  opts.BaseDir,
		pathFunc: opts.PathFunc,
	}
}

// Get returns an EvalSet identified by evalSetID.
func (m *manager) Get(_ context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	evalSet, err := m.load(appName, evalSetID)
	if err != nil {
		return nil, fmt.Errorf("load eval set %s for app %s: %w", evalSetID, appName, err)
	}
	clonedEvalSet, err := clone.CloneEvalSet(evalSet)
	if err != nil {
		return nil, fmt.Errorf("clone eval set %s: %w", evalSetID, err)
	}
	return clonedEvalSet, nil
}

// Create creates and returns an empty EvalSet given the evalSetID.
func (m *manager) Create(_ context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.load(appName, evalSetID); err == nil {
		return nil, fmt.Errorf("eval set %s already exists for app %s", evalSetID, appName)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load eval set %s for app %s: %w", evalSetID, appName, err)
	}
	evalSet := &evalset.EvalSet{
		EvalSetID:         evalSetID,
		Name:              evalSetID,
		EvalCases:         []*evalset.EvalCase{},
		CreationTimestamp: time.Now().UTC(),
	}
	if err := m.store(appName, evalSet); err != nil {
		return nil, fmt.Errorf("store eval set %s for app %s: %w", evalSetID, appName, err)
	}
	clonedEvalSet, err := clone.CloneEvalSet(evalSet)
	if err != nil {
		return nil, fmt.Errorf("clone eval set %s: %w", evalSetID, err)
	}
	return clonedEvalSet, nil
}

// GetCase returns an EvalCase if found, otherwise nil.
func (m *manager) GetCase(_ context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	evalSet, err := m.load(appName, evalSetID)
	if err != nil {
		return nil, fmt.Errorf("load eval set %s for app %s: %w", evalSetID, appName, err)
	}
	for _, c := range evalSet.EvalCases {
		if c != nil && c.EvalID == evalCaseID {
			clonedEvalCase, err := clone.CloneEvalCase(c)
			if err != nil {
				return nil, fmt.Errorf("clone eval case %s: %w", evalCaseID, err)
			}
			return clonedEvalCase, nil
		}
	}
	return nil, fmt.Errorf("eval case %s.%s.%s not found", appName, evalSetID, evalCaseID)
}

// AddCase adds the given EvalCase to an existing EvalSet identified by evalSetID.
func (m *manager) AddCase(_ context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	if evalCase == nil {
		return errors.New("evalCase is nil")
	}
	if evalCase.EvalID == "" {
		return errors.New("evalCase.EvalID is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	evalSet, err := m.load(appName, evalSetID)
	if err != nil {
		return fmt.Errorf("load eval set %s for app %s: %w", evalSetID, appName, err)
	}
	for _, c := range evalSet.EvalCases {
		if c != nil && c.EvalID == evalCase.EvalID {
			return fmt.Errorf("eval case %s.%s.%s already exists", appName, evalSetID, evalCase.EvalID)
		}
	}
	clonedEvalCase, err := clone.CloneEvalCase(evalCase)
	if err != nil {
		return fmt.Errorf("clone eval case %s: %w", evalCase.EvalID, err)
	}
	evalSet.EvalCases = append(evalSet.EvalCases, clonedEvalCase)
	return m.store(appName, evalSet)
}

// UpdateCase updates an existing EvalCase given the evalSetID.
func (m *manager) UpdateCase(_ context.Context, appName, evalSetID string, updatedEvalCase *evalset.EvalCase) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	evalSet, err := m.load(appName, evalSetID)
	if err != nil {
		return fmt.Errorf("load eval set %s for app %s: %w", evalSetID, appName, err)
	}
	for i, c := range evalSet.EvalCases {
		if c != nil && c.EvalID == updatedEvalCase.EvalID {
			clonedEvalCase, err := clone.CloneEvalCase(updatedEvalCase)
			if err != nil {
				return fmt.Errorf("clone eval case %s: %w", updatedEvalCase.EvalID, err)
			}
			evalSet.EvalCases[i] = clonedEvalCase
			return m.store(appName, evalSet)
		}
	}
	return fmt.Errorf("eval case %s.%s.%s not found", appName, evalSetID, updatedEvalCase.EvalID)
}

// DeleteCase deletes the given EvalCase identified by evalSetID and evalCaseID.
func (m *manager) DeleteCase(_ context.Context, appName, evalSetID, evalCaseID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	evalSet, err := m.load(appName, evalSetID)
	if err != nil {
		return fmt.Errorf("load eval set %s for app %s: %w", evalSetID, appName, err)
	}
	for i, c := range evalSet.EvalCases {
		if c != nil && c.EvalID == evalCaseID {
			evalSet.EvalCases = append(evalSet.EvalCases[:i], evalSet.EvalCases[i+1:]...)
			return m.store(appName, evalSet)
		}
	}
	return fmt.Errorf("eval case %s.%s.%s not found", appName, evalSetID, evalCaseID)
}

func (m *manager) evalSetPath(appName, evalSetID string) string {
	return m.pathFunc(m.baseDir, appName, evalSetID)
}

func (m *manager) load(appName, evalSetID string) (*evalset.EvalSet, error) {
	path := m.evalSetPath(appName, evalSetID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}
	var evalSet evalset.EvalSet
	if err := json.Unmarshal(data, &evalSet); err != nil {
		return nil, fmt.Errorf("unmarshal file %s: %w", path, err)
	}
	if evalSet.EvalCases == nil {
		evalSet.EvalCases = []*evalset.EvalCase{}
	}
	return &evalSet, nil
}

func (m *manager) store(appName string, evalSet *evalset.EvalSet) error {
	if evalSet == nil {
		return errors.New("evalset is nil")
	}
	path := m.evalSetPath(appName, evalSet.EvalSetID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir all %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open file %s: %w", tmp, err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(evalSet); err != nil {
		file.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("encode file %s: %w", tmp, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename file %s to %s: %w", tmp, path, err)
	}
	return nil
}
