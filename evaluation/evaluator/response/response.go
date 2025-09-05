//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package response provides response quality evaluation.
package response

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
)

// Evaluator implements response quality evaluation
type Evaluator struct {
}

// New creates a new response evaluator
func New() *Evaluator {
	return &Evaluator{}
}

// Evaluate compares response quality between actual and expected invocations
func (e *Evaluator) Evaluate(ctx context.Context, actual,
	expected []evalset.Invocation) (*evaluator.EvaluationResult, error) {
	// Implementation would go here
	return nil, nil
}

// Name returns the name of this evaluator
func (e *Evaluator) Name() string {
	return "response"
}

// Description returns a description of what this evaluator does
func (e *Evaluator) Description() string {
	return "Evaluates the quality and accuracy of final responses using LLM-as-Judge"
}

// Verify interface compliance
var _ evaluator.Evaluator = (*Evaluator)(nil)
