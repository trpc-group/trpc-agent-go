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
	runs map[string]map[string]*engine.RunResult
}

// New creates an in-memory PromptIter store.
func New() store.Store {
	return &inMemoryStore{
		runs: make(map[string]map[string]*engine.RunResult),
	}
}

func (s *inMemoryStore) Create(_ context.Context, appName string, run *engine.RunResult) error {
	if err := validateRun(appName, run); err != nil {
		return err
	}
	persisted := *run
	persisted.AppName = appName
	cloned, err := cloneRun(&persisted)
	if err != nil {
		return fmt.Errorf("clone promptiter run: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	appRuns := s.runs[appName]
	if appRuns == nil {
		appRuns = make(map[string]*engine.RunResult)
		s.runs[appName] = appRuns
	}
	if _, ok := appRuns[run.ID]; ok {
		return fmt.Errorf("run %q for app %q already exists", run.ID, appName)
	}
	appRuns[run.ID] = cloned
	return nil
}

func (s *inMemoryStore) Get(_ context.Context, appName, runID string) (*engine.RunResult, error) {
	if err := validateRunKey(appName, runID); err != nil {
		return nil, err
	}
	s.mu.RLock()
	appRuns := s.runs[appName]
	run, ok := appRuns[runID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("run %q for app %q not found: %w", runID, appName, os.ErrNotExist)
	}
	cloned, err := cloneRun(run)
	if err != nil {
		return nil, fmt.Errorf("clone promptiter run %q: %w", runID, err)
	}
	cloned.AppName = appName
	cloned.ID = runID
	return cloned, nil
}

func (s *inMemoryStore) Update(_ context.Context, appName string, run *engine.RunResult) error {
	if err := validateRun(appName, run); err != nil {
		return err
	}
	persisted := *run
	persisted.AppName = appName
	cloned, err := cloneRun(&persisted)
	if err != nil {
		return fmt.Errorf("clone promptiter run: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	appRuns := s.runs[appName]
	if _, ok := appRuns[run.ID]; !ok {
		return fmt.Errorf("run %q for app %q not found: %w", run.ID, appName, os.ErrNotExist)
	}
	appRuns[run.ID] = cloned
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

func validateRun(appName string, run *engine.RunResult) error {
	if run == nil {
		return errors.New("promptiter run is nil")
	}
	if err := validateRunKey(appName, run.ID); err != nil {
		return err
	}
	if run.AppName != "" && run.AppName != appName {
		return fmt.Errorf("promptiter run app name %q does not match %q", run.AppName, appName)
	}
	return nil
}

func validateRunKey(appName, runID string) error {
	if appName == "" {
		return errors.New("promptiter run app name is empty")
	}
	if runID == "" {
		return errors.New("promptiter run id is empty")
	}
	return nil
}
