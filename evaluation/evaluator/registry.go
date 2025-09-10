//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package evaluator provides evaluator for evaluation.
package evaluator

import (
    "errors"
    "sort"
    "sync"
)

// Registry manages the registration and retrieval of evaluators
type Registry struct {
    mu         sync.RWMutex
    evaluators map[string]Evaluator
}

// NewRegistry creates a new evaluator registry
func NewRegistry() *Registry {
    return &Registry{
        evaluators: make(map[string]Evaluator),
    }
}

// Register adds an evaluator to the registry
// If an evaluator with the same name exists, returns an error.
func (r *Registry) Register(name string, e Evaluator) error {
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
func (r *Registry) Get(name string) (Evaluator, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    if e, ok := r.evaluators[name]; ok {
        return e, nil
    }
    return nil, errors.New("evaluator not found: " + name)
}

// List returns all registered evaluator names in sorted order
func (r *Registry) List() []string {
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
func (r *Registry) Unregister(name string) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    if _, ok := r.evaluators[name]; !ok {
        return errors.New("evaluator not found: " + name)
    }
    delete(r.evaluators, name)
    return nil
}

// GetEvaluatorForMetric returns the evaluator that supports a specific metric
// NOTE: This requires a convention that evaluator.Name() matches metric name
// or you maintain a separate mapping externally. For now, we assume name-match.
func (r *Registry) GetEvaluatorForMetric(metric string) (Evaluator, error) {
    return r.Get(metric)
}
