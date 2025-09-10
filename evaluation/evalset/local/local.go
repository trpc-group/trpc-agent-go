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
)

// Manager implements the evalset.Manager interface using local file storage.
type Manager struct {
	baseDir string
	mu      sync.Mutex
}

// NewManager creates a new local file evaluation set manager.
// Use functional options defined in option.go; defaults to ./evalsets
func NewManager(opts ...Option) *Manager {
	m := &Manager{baseDir: "evalsets"}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

// Get returns an EvalSet identified by evalSetID.
func (m *Manager) Get(ctx context.Context, evalSetID string) (*evalset.EvalSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.load(evalSetID)
}

// Create creates and returns an empty EvalSet given the evalSetID.
func (m *Manager) Create(ctx context.Context, evalSetID string) (*evalset.EvalSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(m.baseDir, 0o755); err != nil {
		return nil, err
	}
	// If exists, load and return
	if es, err := m.load(evalSetID); err == nil && es != nil {
		return es, nil
	}
	es := &evalset.EvalSet{
		EvalSetID:         evalSetID,
		EvalCases:         []evalset.EvalCase{},
		CreationTimestamp: time.Now().UTC(),
	}
	if err := m.save(es); err != nil {
		return nil, err
	}
	return es, nil
}

// GetCase returns an EvalCase if found, otherwise nil.
func (m *Manager) GetCase(ctx context.Context, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	es, err := m.load(evalSetID)
	if err != nil {
		return nil, err
	}
	for i := range es.EvalCases {
		if es.EvalCases[i].EvalID == evalCaseID {
			c := es.EvalCases[i]
			return &c, nil
		}
	}
	return nil, os.ErrNotExist
}

// AddCase adds the given EvalCase to an existing EvalSet identified by evalSetID.
func (m *Manager) AddCase(ctx context.Context, evalSetID string, evalCase *evalset.EvalCase) error {
	_ = ctx
	if evalCase == nil {
		return errors.New("evalCase is nil")
	}
	if evalCase.EvalID == "" {
		return errors.New("evalCase.EvalID is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	es, err := m.load(evalSetID)
	if err != nil {
		return err
	}
	for _, c := range es.EvalCases {
		if c.EvalID == evalCase.EvalID {
			return fmt.Errorf("eval case %s already exists", evalCase.EvalID)
		}
	}
	es.EvalCases = append(es.EvalCases, *evalCase)
	return m.save(es)
}

// UpdateCase updates an existing EvalCase given the evalSetID.
func (m *Manager) UpdateCase(ctx context.Context, evalSetID string, updatedEvalCase *evalset.EvalCase) error {
	_ = ctx
	if updatedEvalCase == nil {
		return errors.New("updatedEvalCase is nil")
	}
	if updatedEvalCase.EvalID == "" {
		return errors.New("updatedEvalCase.EvalID is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	es, err := m.load(evalSetID)
	if err != nil {
		return err
	}
	for i := range es.EvalCases {
		if es.EvalCases[i].EvalID == updatedEvalCase.EvalID {
			es.EvalCases[i] = *updatedEvalCase
			return m.save(es)
		}
	}
	return os.ErrNotExist
}

// DeleteCase deletes the given EvalCase identified by evalSetID and evalCaseID.
func (m *Manager) DeleteCase(ctx context.Context, evalSetID, evalCaseID string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	es, err := m.load(evalSetID)
	if err != nil {
		return err
	}
	idx := -1
	for i := range es.EvalCases {
		if es.EvalCases[i].EvalID == evalCaseID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return os.ErrNotExist
	}
	es.EvalCases = append(es.EvalCases[:idx], es.EvalCases[idx+1:]...)
	return m.save(es)
}

// --- helpers ---

func (m *Manager) evalSetPath(evalSetID string) string {
	filename := fmt.Sprintf("%s.evalset.json", evalSetID)
	return filepath.Join(m.baseDir, filename)
}

func (m *Manager) load(evalSetID string) (*evalset.EvalSet, error) {
	path := m.evalSetPath(evalSetID)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var es evalset.EvalSet
	dec := json.NewDecoder(f)
	if err := dec.Decode(&es); err != nil {
		return nil, err
	}
	return &es, nil
}

func (m *Manager) save(es *evalset.EvalSet) error {
	if es == nil {
		return errors.New("evalset is nil")
	}
	if err := os.MkdirAll(m.baseDir, 0o755); err != nil {
		return err
	}
	tmp := m.evalSetPath(es.EvalSetID) + ".tmp"
	path := m.evalSetPath(es.EvalSetID)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(es); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
