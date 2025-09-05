//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tooltrajectory provides tool trajectory-based evaluation.
package tooltrajectory

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
)

// Evaluator implements trajectory-based evaluation
type Evaluator struct {
}

// New creates a new trajectory evaluator
func New() *Evaluator {
	return &Evaluator{}
}

// Evaluate compares tool usage trajectories between actual and expected invocations
func (e *Evaluator) Evaluate(ctx context.Context, actual,
	expected []evalset.Invocation) (*evaluator.EvaluationResult, error) {
	// Implementation would go here
	return nil, nil
}

// Name returns the name of this evaluator
func (e *Evaluator) Name() string {
	return "trajectory"
}

// Description returns a description of what this evaluator does
func (e *Evaluator) Description() string {
	return "Evaluates the accuracy of tool usage trajectory including sequence and arguments"
}

// Verify interface compliance
var _ evaluator.Evaluator = (*Evaluator)(nil)
