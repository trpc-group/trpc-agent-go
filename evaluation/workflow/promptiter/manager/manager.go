//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package manager provides asynchronous PromptIter run lifecycle management on top of the synchronous engine.
package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	iprofile "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/profile"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/store"
)

// Manager manages asynchronous PromptIter runs.
type Manager interface {
	// Start creates and starts one asynchronous PromptIter run.
	Start(ctx context.Context, request *engine.RunRequest) (*engine.RunResult, error)
	// Get loads one persisted PromptIter run.
	Get(ctx context.Context, runID string) (*engine.RunResult, error)
	// Cancel cancels one running PromptIter run.
	Cancel(ctx context.Context, runID string) error
	// Close stops active runs and releases manager resources.
	Close() error
}

type manager struct {
	engine      engine.Engine
	store       store.Store
	mu          sync.Mutex
	cancelFuncs map[string]context.CancelFunc
	closed      bool
}

// New creates a PromptIter run manager.
func New(engine engine.Engine, opts ...Option) (Manager, error) {
	options := newOptions(opts...)
	if engine == nil {
		return nil, errors.New("promptiter manager: engine must not be nil")
	}
	return &manager{
		engine:      engine,
		store:       options.store,
		cancelFuncs: make(map[string]context.CancelFunc),
	}, nil
}

// Start creates and starts one asynchronous PromptIter run.
func (m *manager) Start(ctx context.Context, request *engine.RunRequest) (*engine.RunResult, error) {
	if err := validateRunRequest(request); err != nil {
		return nil, err
	}
	runID := uuid.NewString()
	run := &engine.RunResult{
		ID:     runID,
		Status: engine.RunStatusQueued,
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("promptiter manager is closed")
	}
	if err := m.store.Create(ctx, run); err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("create run %q: %w", runID, err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	m.cancelFuncs[runID] = cancel
	m.mu.Unlock()
	go m.run(runCtx, runID, cloneRunRequest(request))
	return run, nil
}

// Get loads one persisted PromptIter run.
func (m *manager) Get(ctx context.Context, runID string) (*engine.RunResult, error) {
	return m.store.Get(ctx, runID)
}

// Cancel cancels one running PromptIter run.
func (m *manager) Cancel(ctx context.Context, runID string) error {
	m.mu.Lock()
	cancel, ok := m.cancelFuncs[runID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("run %q is not running: %w", runID, os.ErrNotExist)
	}
	cancel()
	run, err := m.store.Get(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == engine.RunStatusQueued || run.Status == engine.RunStatusRunning {
		run.Status = engine.RunStatusCanceled
		run.ErrorMessage = "run canceled"
		if err := m.store.Update(ctx, run); err != nil {
			return err
		}
	}
	return nil
}

// Close stops active runs and releases manager resources.
func (m *manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	cancelFuncs := make([]context.CancelFunc, 0, len(m.cancelFuncs))
	for _, cancel := range m.cancelFuncs {
		cancelFuncs = append(cancelFuncs, cancel)
	}
	m.cancelFuncs = make(map[string]context.CancelFunc)
	m.mu.Unlock()
	for _, cancel := range cancelFuncs {
		cancel()
	}
	return m.store.Close()
}

func (m *manager) run(ctx context.Context, runID string, request *engine.RunRequest) {
	run, err := m.store.Get(context.Background(), runID)
	if err != nil {
		m.clearCancel(runID)
		return
	}
	run.Status = engine.RunStatusRunning
	if err := m.store.Update(context.Background(), run); err != nil {
		m.clearCancel(runID)
		return
	}
	observer := &observer{
		manager: m,
		run:     run,
	}
	result, err := m.engine.Run(ctx, request, engine.WithObserver(observer.append))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			observer.run.Status = engine.RunStatusCanceled
			if observer.run.ErrorMessage == "" {
				observer.run.ErrorMessage = "run canceled"
			}
		} else {
			observer.run.Status = engine.RunStatusFailed
			observer.run.ErrorMessage = err.Error()
		}
		_ = m.store.Update(context.Background(), observer.run)
		m.clearCancel(runID)
		return
	}
	if result == nil {
		observer.run.Status = engine.RunStatusFailed
		observer.run.ErrorMessage = "engine returned nil run"
		_ = m.store.Update(context.Background(), observer.run)
		m.clearCancel(runID)
		return
	}
	result.ID = runID
	result.Status = engine.RunStatusSucceeded
	if err := m.store.Update(context.Background(), result); err != nil {
		observer.run.Status = engine.RunStatusFailed
		observer.run.ErrorMessage = err.Error()
		_ = m.store.Update(context.Background(), observer.run)
	}
	m.clearCancel(runID)
}

func (m *manager) clearCancel(runID string) {
	m.mu.Lock()
	delete(m.cancelFuncs, runID)
	m.mu.Unlock()
}

func validateRunRequest(request *engine.RunRequest) error {
	switch {
	case request == nil:
		return errors.New("run request is nil")
	case len(request.TrainEvalSetIDs) == 0:
		return errors.New("train evaluation set ids are empty")
	case len(request.ValidationEvalSetIDs) == 0:
		return errors.New("validation evaluation set ids are empty")
	case request.MaxRounds <= 0:
		return errors.New("max rounds must be greater than 0")
	case request.TargetSurfaceIDs != nil && len(request.TargetSurfaceIDs) == 0:
		return errors.New("target surface ids must not be empty")
	default:
		return nil
	}
}

func cloneRunRequest(request *engine.RunRequest) *engine.RunRequest {
	if request == nil {
		return nil
	}
	cloned := *request
	cloned.TrainEvalSetIDs = append([]string(nil), request.TrainEvalSetIDs...)
	cloned.ValidationEvalSetIDs = append([]string(nil), request.ValidationEvalSetIDs...)
	cloned.InitialProfile = iprofile.Clone(request.InitialProfile)
	cloned.TargetSurfaceIDs = append([]string(nil), request.TargetSurfaceIDs...)
	if request.StopPolicy.TargetScore != nil {
		targetScore := *request.StopPolicy.TargetScore
		cloned.StopPolicy.TargetScore = &targetScore
	}
	return &cloned
}
