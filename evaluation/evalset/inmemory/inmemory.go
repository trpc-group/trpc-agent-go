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
)

// Manager implements the evalset.Manager interface using in-memory storage.
//
// The manager keeps an in-memory copy of all eval sets. Each API returns
// deep-cloned objects to avoid accidental mutation by callers.
type Manager struct {
	mu   sync.RWMutex
	sets map[string]*evalset.EvalSet
}

// NewManager creates a new in-memory evaluation set manager.
func NewManager() *Manager {
	return &Manager{
		sets: make(map[string]*evalset.EvalSet),
	}
}

// Get returns an EvalSet identified by evalSetID. If the set does not exist,
// os.ErrNotExist is returned.
func (m *Manager) Get(ctx context.Context, evalSetID string) (*evalset.EvalSet, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()
	es, ok := m.sets[evalSetID]
	if !ok {
		return nil, fmt.Errorf("%w: eval set %s", os.ErrNotExist, evalSetID)
	}
	return cloneEvalSet(es), nil
}

// Create creates and returns an empty EvalSet given the evalSetID. If the set
// already exists, a cloned copy is returned.
func (m *Manager) Create(ctx context.Context, evalSetID string) (*evalset.EvalSet, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if es, ok := m.sets[evalSetID]; ok {
		return cloneEvalSet(es), nil
	}
	es := &evalset.EvalSet{
		EvalSetID:         evalSetID,
		EvalCases:         []evalset.EvalCase{},
		CreationTimestamp: time.Now().UTC(),
	}
	m.sets[evalSetID] = es
	return cloneEvalSet(es), nil
}

// GetCase returns an EvalCase if found, otherwise an error.
func (m *Manager) GetCase(ctx context.Context, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()
	es, ok := m.sets[evalSetID]
	if !ok {
		return nil, fmt.Errorf("%w: eval set %s", os.ErrNotExist, evalSetID)
	}
	for i := range es.EvalCases {
		if es.EvalCases[i].EvalID == evalCaseID {
			caseCopy := cloneEvalCase(&es.EvalCases[i])
			return caseCopy, nil
		}
	}
	return nil, fmt.Errorf("%w: eval case %s", os.ErrNotExist, evalCaseID)
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
	es, ok := m.sets[evalSetID]
	if !ok {
		return fmt.Errorf("%w: eval set %s", os.ErrNotExist, evalSetID)
	}
	for _, c := range es.EvalCases {
		if c.EvalID == evalCase.EvalID {
			return errors.New("eval case already exists")
		}
	}
	es.EvalCases = append(es.EvalCases, *cloneEvalCase(evalCase))
	return nil
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
	es, ok := m.sets[evalSetID]
	if !ok {
		return fmt.Errorf("%w: eval set %s", os.ErrNotExist, evalSetID)
	}
	for i := range es.EvalCases {
		if es.EvalCases[i].EvalID == updatedEvalCase.EvalID {
			es.EvalCases[i] = *cloneEvalCase(updatedEvalCase)
			return nil
		}
	}
	return fmt.Errorf("%w: eval case %s", os.ErrNotExist, updatedEvalCase.EvalID)
}

// DeleteCase deletes the given EvalCase identified by evalSetID and evalCaseID.
func (m *Manager) DeleteCase(ctx context.Context, evalSetID, evalCaseID string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	es, ok := m.sets[evalSetID]
	if !ok {
		return fmt.Errorf("%w: eval set %s", os.ErrNotExist, evalSetID)
	}
	idx := -1
	for i := range es.EvalCases {
		if es.EvalCases[i].EvalID == evalCaseID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: eval case %s", os.ErrNotExist, evalCaseID)
	}
	es.EvalCases = append(es.EvalCases[:idx], es.EvalCases[idx+1:]...)
	return nil
}

func cloneEvalSet(es *evalset.EvalSet) *evalset.EvalSet {
	if es == nil {
		return nil
	}
	copySet := *es
	if es.EvalCases != nil {
		copySet.EvalCases = make([]evalset.EvalCase, len(es.EvalCases))
		for i := range es.EvalCases {
			copySet.EvalCases[i] = *cloneEvalCase(&es.EvalCases[i])
		}
	}
	return &copySet
}

func cloneEvalCase(c *evalset.EvalCase) *evalset.EvalCase {
	if c == nil {
		return nil
	}
	clone := *c
	if c.SessionInput != nil {
		clone.SessionInput = cloneSessionInput(c.SessionInput)
	}
	if c.Conversation != nil {
		clone.Conversation = make([]evalset.Invocation, len(c.Conversation))
		for i := range c.Conversation {
			clone.Conversation[i] = *cloneInvocation(&c.Conversation[i])
		}
	}
	return &clone
}

func cloneInvocation(in *evalset.Invocation) *evalset.Invocation {
	if in == nil {
		return nil
	}
	clone := *in
	if in.UserContent != nil {
		clone.UserContent = cloneContent(in.UserContent)
	}
	if in.FinalResponse != nil {
		clone.FinalResponse = cloneContent(in.FinalResponse)
	}
	if in.IntermediateData != nil {
		clone.IntermediateData = cloneIntermediateData(in.IntermediateData)
	}
	return &clone
}

func cloneContent(c *evalset.Content) *evalset.Content {
	if c == nil {
		return nil
	}
	clone := *c
	if c.Parts != nil {
		clone.Parts = make([]evalset.Part, len(c.Parts))
		copy(clone.Parts, c.Parts)
	}
	return &clone
}

func cloneIntermediateData(d *evalset.IntermediateData) *evalset.IntermediateData {
	if d == nil {
		return nil
	}
	clone := *d
	if d.ToolUses != nil {
		clone.ToolUses = make([]evalset.FunctionCall, len(d.ToolUses))
		for i := range d.ToolUses {
			clone.ToolUses[i] = cloneFunctionCall(&d.ToolUses[i])
		}
	}
	if d.ToolResponses != nil {
		clone.ToolResponses = make([]evalset.ToolResponse, len(d.ToolResponses))
		for i := range d.ToolResponses {
			clone.ToolResponses[i] = cloneToolResponse(&d.ToolResponses[i])
		}
	}
	if d.IntermediateResponses != nil {
		clone.IntermediateResponses = make([]evalset.IntermediateMessage, len(d.IntermediateResponses))
		for i := range d.IntermediateResponses {
			clone.IntermediateResponses[i] = cloneIntermediateMessage(&d.IntermediateResponses[i])
		}
	}
	return &clone
}

func cloneFunctionCall(c *evalset.FunctionCall) evalset.FunctionCall {
	if c == nil {
		return evalset.FunctionCall{}
	}
	clone := *c
	if c.Args != nil {
		clone.Args = make(map[string]interface{}, len(c.Args))
		for k, v := range c.Args {
			clone.Args[k] = v
		}
	}
	return clone
}

func cloneToolResponse(r *evalset.ToolResponse) evalset.ToolResponse {
	if r == nil {
		return evalset.ToolResponse{}
	}
	clone := *r
	if r.Response != nil {
		clone.Response = make(map[string]interface{}, len(r.Response))
		for k, v := range r.Response {
			clone.Response[k] = v
		}
	}
	return clone
}

func cloneIntermediateMessage(m *evalset.IntermediateMessage) evalset.IntermediateMessage {
	if m == nil {
		return evalset.IntermediateMessage{}
	}
	clone := *m
	if m.Parts != nil {
		clone.Parts = make([]evalset.Part, len(m.Parts))
		copy(clone.Parts, m.Parts)
	}
	return clone
}

func cloneSessionInput(input *evalset.SessionInput) *evalset.SessionInput {
	if input == nil {
		return nil
	}
	clone := *input
	if input.State != nil {
		clone.State = make(map[string]interface{}, len(input.State))
		for k, v := range input.State {
			clone.State[k] = v
		}
	}
	return &clone
}
