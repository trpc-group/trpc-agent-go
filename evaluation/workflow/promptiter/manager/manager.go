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
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
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
	appName              string
	engine               engine.Engine
	store                store.Store
	storedResultSlimming engine.RunResultSlimming
	mu                   sync.Mutex
	cancelFuncs          map[string]context.CancelFunc
	closed               bool
}

// New creates a PromptIter run manager for one app.
func New(appName string, engine engine.Engine, opts ...Option) (Manager, error) {
	options := newOptions(opts...)
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return nil, errors.New("promptiter manager: app name must not be empty")
	}
	if engine == nil {
		return nil, errors.New("promptiter manager: engine must not be nil")
	}
	return &manager{
		appName:              appName,
		engine:               engine,
		store:                options.store,
		storedResultSlimming: options.storedResultSlimming,
		cancelFuncs:          make(map[string]context.CancelFunc),
	}, nil
}

// Start creates and starts one asynchronous PromptIter run.
func (m *manager) Start(ctx context.Context, request *engine.RunRequest) (*engine.RunResult, error) {
	if err := validateRunRequest(request); err != nil {
		return nil, err
	}
	runID := uuid.NewString()
	run := &engine.RunResult{
		AppName: m.appName,
		ID:      runID,
		Status:  engine.RunStatusQueued,
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("promptiter manager is closed")
	}
	if err := m.store.Create(ctx, m.appName, m.slimStoredRun(run)); err != nil {
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
	return m.store.Get(ctx, m.appName, runID)
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
	run, err := m.store.Get(ctx, m.appName, runID)
	if err != nil {
		return err
	}
	if run.Status == engine.RunStatusQueued || run.Status == engine.RunStatusRunning {
		run.Status = engine.RunStatusCanceled
		run.ErrorMessage = "run canceled"
		if err := m.store.Update(ctx, m.appName, m.slimStoredRun(run)); err != nil {
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
	run, err := m.store.Get(context.Background(), m.appName, runID)
	if err != nil {
		m.clearCancel(runID)
		return
	}
	run.Status = engine.RunStatusRunning
	if err := m.store.Update(context.Background(), m.appName, m.slimStoredRun(run)); err != nil {
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
		_ = m.store.Update(context.Background(), m.appName, m.slimStoredRun(observer.run))
		m.clearCancel(runID)
		return
	}
	if result == nil {
		observer.run.Status = engine.RunStatusFailed
		observer.run.ErrorMessage = "engine returned nil run"
		_ = m.store.Update(context.Background(), m.appName, m.slimStoredRun(observer.run))
		m.clearCancel(runID)
		return
	}
	result.ID = runID
	result.AppName = m.appName
	result.Status = engine.RunStatusSucceeded
	if err := m.store.Update(context.Background(), m.appName, m.slimStoredRun(result)); err != nil {
		observer.run.Status = engine.RunStatusFailed
		observer.run.ErrorMessage = err.Error()
		_ = m.store.Update(context.Background(), m.appName, m.slimStoredRun(observer.run))
	}
	m.clearCancel(runID)
}

func (m *manager) slimStoredRun(run *engine.RunResult) *engine.RunResult {
	if m == nil {
		return run
	}
	return slimRunResult(run, m.storedResultSlimming)
}

func (m *manager) clearCancel(runID string) {
	m.mu.Lock()
	delete(m.cancelFuncs, runID)
	m.mu.Unlock()
}

func validateRunRequest(request *engine.RunRequest) error {
	if request == nil {
		return errors.New("run request is nil")
	}
	if err := validateEvalSetInputs("train", request.Train); err != nil {
		return err
	}
	if err := validateEvalSetInputs("validation", request.Validation); err != nil {
		return err
	}
	switch {
	case request.MaxRounds <= 0:
		return errors.New("max rounds must be greater than 0")
	case request.TargetSurfaceIDs != nil && len(request.TargetSurfaceIDs) == 0:
		return errors.New("target surface ids must not be empty")
	case request.BackwardOptions.CaseParallelism < 0:
		return errors.New("backward case parallelism must be non-negative")
	case request.AggregationOptions.SurfaceParallelism < 0:
		return errors.New("aggregation surface parallelism must be non-negative")
	case request.OptimizerOptions.SurfaceParallelism < 0:
		return errors.New("optimizer surface parallelism must be non-negative")
	default:
		return nil
	}
}

func cloneRunRequest(request *engine.RunRequest) *engine.RunRequest {
	if request == nil {
		return nil
	}
	cloned := *request
	cloned.Train = cloneEvalSetInputs(request.Train)
	cloned.Validation = cloneEvalSetInputs(request.Validation)
	cloned.InitialProfile = iprofile.Clone(request.InitialProfile)
	cloned.TargetSurfaceIDs = append([]string(nil), request.TargetSurfaceIDs...)
	if request.StopPolicy.TargetScore != nil {
		targetScore := *request.StopPolicy.TargetScore
		cloned.StopPolicy.TargetScore = &targetScore
	}
	return &cloned
}

func validateEvalSetInputs(role string, inputs []engine.EvalSetInput) error {
	prefix := role + " "
	if len(inputs) == 0 {
		return fmt.Errorf("%sevaluation sets are empty", prefix)
	}
	for _, input := range inputs {
		if input.EvalSetID == "" {
			return fmt.Errorf("%sevaluation set id is empty", prefix)
		}
		if slices.Contains(input.EvalCaseIDs, "") {
			return fmt.Errorf("%seval case id for eval set %q is empty", prefix, input.EvalSetID)
		}
		selectedCaseIDs := make(map[string]struct{}, len(input.EvalCaseIDs))
		for _, evalCaseID := range input.EvalCaseIDs {
			selectedCaseIDs[evalCaseID] = struct{}{}
		}
		for _, hint := range input.LossHints {
			hintEvalCaseID := strings.TrimSpace(hint.EvalCaseID)
			switch {
			case hintEvalCaseID == "":
				return fmt.Errorf("%sloss hint eval case id for eval set %q is empty", prefix, input.EvalSetID)
			case strings.TrimSpace(hint.MetricName) == "":
				return fmt.Errorf(
					"%sloss hint metric name for eval set %q case %q is empty",
					prefix,
					input.EvalSetID,
					hint.EvalCaseID,
				)
			case strings.TrimSpace(hint.Reason) == "":
				return fmt.Errorf(
					"%sloss hint reason for eval set %q case %q metric %q is empty",
					prefix,
					input.EvalSetID,
					hint.EvalCaseID,
					hint.MetricName,
				)
			case !isValidLossHintSeverity(hint.Severity):
				return fmt.Errorf(
					"%sloss hint severity %q for eval set %q case %q metric %q is invalid",
					prefix,
					hint.Severity,
					input.EvalSetID,
					hint.EvalCaseID,
					hint.MetricName,
				)
			}
			if len(selectedCaseIDs) > 0 {
				if _, ok := selectedCaseIDs[hintEvalCaseID]; !ok {
					return fmt.Errorf(
						"%sloss hint eval case %q is not selected for eval set %q",
						prefix,
						hint.EvalCaseID,
						input.EvalSetID,
					)
				}
			}
		}
	}
	return nil
}

func isValidLossHintSeverity(severity promptiter.LossSeverity) bool {
	switch severity {
	case "",
		promptiter.LossSeverityP0,
		promptiter.LossSeverityP1,
		promptiter.LossSeverityP2,
		promptiter.LossSeverityP3:
		return true
	default:
		return false
	}
}

func cloneEvalSetInputs(inputs []engine.EvalSetInput) []engine.EvalSetInput {
	if inputs == nil {
		return nil
	}
	cloned := make([]engine.EvalSetInput, 0, len(inputs))
	for _, input := range inputs {
		cloned = append(cloned, engine.EvalSetInput{
			EvalSetID:   input.EvalSetID,
			EvalCaseIDs: append([]string(nil), input.EvalCaseIDs...),
			LossHints:   append([]engine.LossHint(nil), input.LossHints...),
		})
	}
	return cloned
}
