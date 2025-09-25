//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package registry

import (
	"errors"
	"sort"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/tooltrajectory"
)

// Registry manages the registration and retrieval of evaluators
type Registry interface {
	Register(name string, e evaluator.Evaluator) error
	Unregister(name string) error
	Get(name string) (evaluator.Evaluator, error)
	List() []string
}

// Registry manages the registration and retrieval of evaluators
type registry struct {
	mu         sync.RWMutex
	evaluators map[string]evaluator.Evaluator
}

// NewRegistry creates a new evaluator registry
func NewRegistry() Registry {
	r := &registry{
		evaluators: make(map[string]evaluator.Evaluator),
	}
	toolTrajectory := tooltrajectory.New()
	r.Register(toolTrajectory.Name(), toolTrajectory)
	return r
}

// Register adds an evaluator to the registry
// If an evaluator with the same name exists, returns an error.
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
	if _, ok := r.evaluators[name]; ok {
		return errors.New("evaluator already registered: " + name)
	}
	r.evaluators[name] = e
	return nil
}

// Get retrieves an evaluator by name
func (r *registry) Get(name string) (evaluator.Evaluator, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.evaluators[name]; ok {
		return e, nil
	}
	return nil, errors.New("evaluator not found: " + name)
}

// List returns all registered evaluator names in sorted order
func (r *registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.evaluators))
	for n := range r.evaluators {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Unregister removes an evaluator from the registry
func (r *registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.evaluators[name]; !ok {
		return errors.New("evaluator not found: " + name)
	}
	delete(r.evaluators, name)
	return nil
}
