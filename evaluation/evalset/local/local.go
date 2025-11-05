//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package local provides a local file storage manager implementation for evaluation sets.
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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/clone"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/epochtime"
)

const (
	defaultTempFileSuffix = ".tmp"
	defaultDirPermission  = 0o755
	defaultFilePermission = 0o644
)

// manager implements evalset.Manager backed by the local filesystem.
type manager struct {
	mu      sync.RWMutex
	baseDir string
	locator evalset.Locator
}

// New creates a local file evaluation set manager.
func New(opt ...evalset.Option) evalset.Manager {
	opts := evalset.NewOptions(opt...)
	return &manager{
		baseDir: opts.BaseDir,
		locator: opts.Locator,
	}
}

// Get gets an EvalSet identified by evalSetID.
// Returns an error if the EvalSet does not exist.
func (m *manager) Get(_ context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	if evalSetID == "" {
		return nil, errors.New("eval set id is empty")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	evalSet, err := m.load(appName, evalSetID)
	if err != nil {
		return nil, fmt.Errorf("load eval set %s.%s: %w", appName, evalSetID, err)
	}
	return evalSet, nil
}

// Create creates an EvalSet.
// Returns an error if the EvalSet already exists.
func (m *manager) Create(_ context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	if evalSetID == "" {
		return nil, errors.New("eval set id is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.load(appName, evalSetID); err == nil {
		return nil, fmt.Errorf("eval set %s.%s already exists", appName, evalSetID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load eval set %s.%s: %w", appName, evalSetID, err)
	}
	evalSet := &evalset.EvalSet{
		EvalSetID:         evalSetID,
		Name:              evalSetID,
		EvalCases:         []*evalset.EvalCase{},
		CreationTimestamp: &epochtime.EpochTime{Time: time.Now()},
	}
	if err := m.store(appName, evalSet); err != nil {
		return nil, fmt.Errorf("store eval set %s.%s: %w", appName, evalSetID, err)
	}
	return evalSet, nil
}

// List lists all EvalSet ID for the given appName.
// Returns an error if the appName does not exist.
func (m *manager) List(_ context.Context, appName string) ([]string, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	evalSetIDs, err := m.locator.List(m.baseDir, appName)
	if err != nil {
		return nil, fmt.Errorf("list eval sets for app %s: %w", appName, err)
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
	if _, err := m.load(appName, evalSetID); err != nil {
		return fmt.Errorf("load eval set %s.%s: %w", appName, evalSetID, err)
	}
	if err := m.remove(appName, evalSetID); err != nil {
		return fmt.Errorf("remove eval set %s.%s: %w", appName, evalSetID, err)
	}
	return nil
}

// GetCase gets an EvalCase.
// Returns an error if the EvalCase does not exist.
func (m *manager) GetCase(_ context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	if evalSetID == "" {
		return nil, errors.New("eval set id is empty")
	}
	if evalCaseID == "" {
		return nil, errors.New("eval case id is empty")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	evalSet, err := m.load(appName, evalSetID)
	if err != nil {
		return nil, fmt.Errorf("load eval set %s for app %s: %w", evalSetID, appName, err)
	}
	for _, c := range evalSet.EvalCases {
		if c.EvalID == evalCaseID {
			return c, nil
		}
	}
	return nil, fmt.Errorf("eval case %s.%s.%s not found: %w", appName, evalSetID, evalCaseID, os.ErrNotExist)
}

// AddCase adds the given EvalCase to an existing EvalSet identified by evalSetID.
// If the EvalSet does not exist or the EvalCase already exists, returns an error.
func (m *manager) AddCase(_ context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	if appName == "" {
		return errors.New("app name is empty")
	}
	if evalSetID == "" {
		return errors.New("eval set id is empty")
	}
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
		return fmt.Errorf("load eval set %s.%s: %w", appName, evalSetID, err)
	}
	for _, c := range evalSet.EvalCases {
		if c.EvalID == evalCase.EvalID {
			return fmt.Errorf("eval case %s.%s.%s already exists", appName, evalSetID, evalCase.EvalID)
		}
	}
	cloned, err := clone.Clone(evalCase)
	if err != nil {
		return fmt.Errorf("clone evalcase: %w", err)
	}
	if cloned.CreationTimestamp == nil {
		cloned.CreationTimestamp = &epochtime.EpochTime{Time: time.Now()}
	}
	for _, invocation := range cloned.Conversation {
		if invocation.CreationTimestamp == nil {
			invocation.CreationTimestamp = &epochtime.EpochTime{Time: time.Now()}
		}
	}
	evalSet.EvalCases = append(evalSet.EvalCases, cloned)
	if err := m.store(appName, evalSet); err != nil {
		return fmt.Errorf("store eval set %s.%s: %w", appName, evalSetID, err)
	}
	return nil
}

// UpdateCase updates an existing EvalCase.
// If the EvalSet does not exist or the EvalCase does not exist, returns an error.
func (m *manager) UpdateCase(_ context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	if appName == "" {
		return errors.New("app name is empty")
	}
	if evalSetID == "" {
		return errors.New("eval set id is empty")
	}
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
		return fmt.Errorf("load eval set %s.%s: %w", appName, evalSetID, err)
	}
	for i, c := range evalSet.EvalCases {
		if c.EvalID == evalCase.EvalID {
			evalSet.EvalCases[i] = evalCase
			if err := m.store(appName, evalSet); err != nil {
				return fmt.Errorf("store eval set %s.%s: %w", appName, evalSetID, err)
			}
			return nil
		}
	}
	return fmt.Errorf("eval case %s.%s.%s not found: %w", appName, evalSetID, evalCase.EvalID, os.ErrNotExist)
}

// DeleteCase deletes the given EvalCase.
// If the EvalSet does not exist or the EvalCase does not exist, returns an error.
func (m *manager) DeleteCase(_ context.Context, appName, evalSetID, evalCaseID string) error {
	if appName == "" {
		return errors.New("app name is empty")
	}
	if evalSetID == "" {
		return errors.New("eval set id is empty")
	}
	if evalCaseID == "" {
		return errors.New("eval case id is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	evalSet, err := m.load(appName, evalSetID)
	if err != nil {
		return fmt.Errorf("load eval set %s.%s: %w", appName, evalSetID, err)
	}
	for i, c := range evalSet.EvalCases {
		if c.EvalID == evalCaseID {
			evalSet.EvalCases = append(evalSet.EvalCases[:i], evalSet.EvalCases[i+1:]...)
			if err := m.store(appName, evalSet); err != nil {
				return fmt.Errorf("store eval set %s.%s: %w", appName, evalSetID, err)
			}
			return nil
		}
	}
	return fmt.Errorf("eval case %s.%s.%s not found: %w", appName, evalSetID, evalCaseID, os.ErrNotExist)
}

// evalSetPath builds the path to the EvalSet file.
func (m *manager) evalSetPath(appName, evalSetID string) string {
	return m.locator.Build(m.baseDir, appName, evalSetID)
}

// load loads the EvalSet from the file system.
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

// store stores the EvalSet to the file system.
func (m *manager) store(appName string, evalSet *evalset.EvalSet) error {
	if evalSet == nil {
		return errors.New("evalSet is nil")
	}
	path := m.evalSetPath(appName, evalSet.EvalSetID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, defaultDirPermission); err != nil {
		return fmt.Errorf("mkdir all %s: %w", dir, err)
	}
	tmp := path + defaultTempFileSuffix
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, defaultFilePermission)
	if err != nil {
		return fmt.Errorf("open file %s: %w", tmp, err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(evalSet); err != nil {
		file.Close()
		os.Remove(tmp)
		return fmt.Errorf("encode file %s: %w", tmp, err)
	}
	if err := file.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename file %s to %s: %w", tmp, path, err)
	}
	return nil
}

// remove removes the EvalSet from the file system.
func (m *manager) remove(appName string, evalSetID string) error {
	path := m.evalSetPath(appName, evalSetID)
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove file %s: %w", path, err)
	}
	return nil
}
