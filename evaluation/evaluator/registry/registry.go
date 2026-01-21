//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package registry manages the registration and retrieval of evaluators.
package registry

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	finalresponse "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/finalresponse"
	llmfinalresponse "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/finalresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/rubricknowledgerecall"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/rubricresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/tooltrajectory"
)

// Registry defines the interface for evaluators registry.
type Registry interface {
	// Register registers an evaluator to the registry.
	Register(name string, e evaluator.Evaluator) error
	// Get retrieves an evaluator by name.
	Get(name string) (evaluator.Evaluator, error)
	// List returns the names of all registered evaluators.
	List() []string
}

// registry is the default implementation of Registry.
type registry struct {
	mu         sync.RWMutex
	evaluators map[string]evaluator.Evaluator
}

// New creates a evaluator registry
func New() Registry {
	r := &registry{
		evaluators: make(map[string]evaluator.Evaluator),
	}
	toolTrajectory := tooltrajectory.New()
	r.Register(toolTrajectory.Name(), toolTrajectory)
	finalResponse := finalresponse.New()
	r.Register(finalResponse.Name(), finalResponse)
	llmfinalResponse := llmfinalresponse.New()
	r.Register(llmfinalResponse.Name(), llmfinalResponse)
	rubricResponse := rubricresponse.New()
	r.Register(rubricResponse.Name(), rubricResponse)
	rubricKnowledgeRecall := rubricknowledgerecall.New()
	r.Register(rubricKnowledgeRecall.Name(), rubricKnowledgeRecall)
	return r
}

// Register registers an evaluator to the registry.
// Same name evaluator will be overwritten.
func (r *registry) Register(name string, e evaluator.Evaluator) error {
	if e == nil {
		return errors.New("evaluator is nil")
	}
	if name == "" {
		name = e.Name()
	}
	if name == "" {
		return errors.New("evaluator name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evaluators[name] = e
	return nil
}

// Get gets an evaluator by name.
// Returns os.ErrNotExist if the evaluator is not found.
func (r *registry) Get(name string) (evaluator.Evaluator, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.evaluators[name]; ok {
		return e, nil
	}
	return nil, fmt.Errorf("get evaluator %s: %w", name, os.ErrNotExist)
}

// List returns the names of all registered evaluators sorted lexicographically.
func (r *registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.evaluators))
	for name := range r.evaluators {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
