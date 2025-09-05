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

// Registry manages the registration and retrieval of evaluators
type Registry struct {
	evaluators map[string]Evaluator
}

// NewRegistry creates a new evaluator registry
func NewRegistry() *Registry {
	return &Registry{
		evaluators: make(map[string]Evaluator),
	}
}

// Register adds an evaluator to the registry
func (r *Registry) Register(name string, evaluator Evaluator) error {
	// Implementation would go here
	return nil
}

// Get retrieves an evaluator by name
func (r *Registry) Get(name string) (Evaluator, error) {
	// Implementation would go here
	return nil, nil
}

// List returns all registered evaluator names
func (r *Registry) List() []string {
	// Implementation would go here
	return nil
}

// Unregister removes an evaluator from the registry
func (r *Registry) Unregister(name string) error {
	// Implementation would go here
	return nil
}

// GetEvaluatorForMetric returns the evaluator that supports a specific metric
func (r *Registry) GetEvaluatorForMetric(metric string) (Evaluator, error) {
	// Implementation would go here
	return nil, nil
}
