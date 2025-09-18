//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides a in-memory storage implementation for evaluation results.
package inmemory

import (
	"context"
	"fmt"
	"os"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
)

// Manager implements the evalresult.Manager interface using in-memory storage.
type Manager struct {
	mu      sync.RWMutex
	results map[string]*evalresult.EvalSetResult
}

// NewManager creates a new in-memory evaluation result manager.
func NewManager() *Manager {
	return &Manager{
		results: make(map[string]*evalresult.EvalSetResult),
	}
}

// Save stores an evaluation result in memory.
func (m *Manager) Save(ctx context.Context, result *evalresult.EvalSetResult) error {
	_ = ctx
	if result == nil {
		return fmt.Errorf("result is nil")
	}
	if result.EvalSetResultID == "" {
		return fmt.Errorf("result id is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[result.EvalSetResultID] = cloneEvalSetResult(result)
	return nil
}

// Get retrieves an evaluation result by evalSetResultID from memory.
func (m *Manager) Get(ctx context.Context, evalSetResultID string) (*evalresult.EvalSetResult, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()
	res, ok := m.results[evalSetResultID]
	if !ok {
		return nil, fmt.Errorf("%w: eval set result %s", os.ErrNotExist, evalSetResultID)
	}
	return cloneEvalSetResult(res), nil
}

// List returns all available evaluation results from memory.
func (m *Manager) List(ctx context.Context) ([]*evalresult.EvalSetResult, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()
	results := make([]*evalresult.EvalSetResult, 0, len(m.results))
	for _, res := range m.results {
		results = append(results, cloneEvalSetResult(res))
	}
	return results, nil
}

func cloneEvalSetResult(res *evalresult.EvalSetResult) *evalresult.EvalSetResult {
	if res == nil {
		return nil
	}
	clone := *res
	if res.EvalCaseResults != nil {
		clone.EvalCaseResults = make([]evalresult.EvalCaseResult, len(res.EvalCaseResults))
		for i := range res.EvalCaseResults {
			clone.EvalCaseResults[i] = cloneEvalCaseResult(&res.EvalCaseResults[i])
		}
	}
	return &clone
}

func cloneEvalCaseResult(res *evalresult.EvalCaseResult) evalresult.EvalCaseResult {
	if res == nil {
		return evalresult.EvalCaseResult{}
	}
	clone := *res
	if res.OverallEvalMetricResults != nil {
		clone.OverallEvalMetricResults = make([]evalresult.EvalMetricResult, len(res.OverallEvalMetricResults))
		for i := range res.OverallEvalMetricResults {
			clone.OverallEvalMetricResults[i] = cloneEvalMetricResult(&res.OverallEvalMetricResults[i])
		}
	}
	if res.EvalMetricResultPerInvocation != nil {
		clone.EvalMetricResultPerInvocation = make([]evalresult.EvalMetricResultPerInvocation, len(res.EvalMetricResultPerInvocation))
		for i := range res.EvalMetricResultPerInvocation {
			clone.EvalMetricResultPerInvocation[i] = cloneMetricPerInvocation(&res.EvalMetricResultPerInvocation[i])
		}
	}
	return clone
}

func cloneEvalMetricResult(res *evalresult.EvalMetricResult) evalresult.EvalMetricResult {
	if res == nil {
		return evalresult.EvalMetricResult{}
	}
	clone := *res
	if res.Score != nil {
		score := *res.Score
		clone.Score = &score
	}
	if res.Details != nil {
		clone.Details = make(map[string]interface{}, len(res.Details))
		for k, v := range res.Details {
			clone.Details[k] = v
		}
	}
	return clone
}

func cloneMetricPerInvocation(res *evalresult.EvalMetricResultPerInvocation) evalresult.EvalMetricResultPerInvocation {
	if res == nil {
		return evalresult.EvalMetricResultPerInvocation{}
	}
	clone := *res
	if res.MetricResults != nil {
		clone.MetricResults = make([]evalresult.EvalMetricResult, len(res.MetricResults))
		for i := range res.MetricResults {
			clone.MetricResults[i] = cloneEvalMetricResult(&res.MetricResults[i])
		}
	}
	return clone
}
