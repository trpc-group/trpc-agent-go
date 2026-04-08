//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package inmemory provides an in-memory PromptIter store implementation.
package inmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/store"
)

type inMemoryStore struct {
	mu   sync.RWMutex
	runs map[string]*engine.RunResult
}

// New creates an in-memory PromptIter store.
func New() store.Store {
	return &inMemoryStore{
		runs: make(map[string]*engine.RunResult),
	}
}

func (s *inMemoryStore) Create(_ context.Context, run *engine.RunResult) error {
	if run == nil {
		return errors.New("promptiter run is nil")
	}
	if run.ID == "" {
		return errors.New("promptiter run id is empty")
	}
	cloned, err := cloneRun(run)
	if err != nil {
		return fmt.Errorf("clone promptiter run: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[run.ID]; ok {
		return fmt.Errorf("run %q already exists", run.ID)
	}
	s.runs[run.ID] = cloned
	return nil
}

func (s *inMemoryStore) Get(_ context.Context, runID string) (*engine.RunResult, error) {
	s.mu.RLock()
	run, ok := s.runs[runID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("run %q not found: %w", runID, os.ErrNotExist)
	}
	cloned, err := cloneRun(run)
	if err != nil {
		return nil, fmt.Errorf("clone promptiter run %q: %w", runID, err)
	}
	return cloned, nil
}

func (s *inMemoryStore) Update(_ context.Context, run *engine.RunResult) error {
	if run == nil {
		return errors.New("promptiter run is nil")
	}
	if run.ID == "" {
		return errors.New("promptiter run id is empty")
	}
	cloned, err := cloneRun(run)
	if err != nil {
		return fmt.Errorf("clone promptiter run: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[run.ID]; !ok {
		return fmt.Errorf("run %q not found: %w", run.ID, os.ErrNotExist)
	}
	s.runs[run.ID] = cloned
	return nil
}

func (s *inMemoryStore) Close() error {
	return nil
}

func cloneRun(run *engine.RunResult) (*engine.RunResult, error) {
	if run == nil {
		return nil, nil
	}
	bytes, err := json.Marshal(run)
	if err != nil {
		return nil, err
	}
	var cloned engine.RunResult
	if err := json.Unmarshal(bytes, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}
