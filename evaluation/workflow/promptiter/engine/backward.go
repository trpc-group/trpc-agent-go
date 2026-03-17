//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package engine implements PromptIter orchestration and runtime flow for a generation round.
package engine

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
)

// CaseBackwardResult stores all step gradients produced for one eval case.
type CaseBackwardResult struct {
	// EvalSetID identifies the source evaluation set.
	EvalSetID string
	// EvalCaseID identifies the source evaluation case.
	EvalCaseID string
	// StepGradients stores gradients per step in topological order.
	StepGradients []promptiter.StepGradient
}

// BackwardResult stores aggregated backward outputs for every case in this round.
type BackwardResult struct {
	// Cases stores per-case backward outputs for downstream aggregation.
	Cases []CaseBackwardResult
}

// backward executes backward propagation for all required cases and traces.
func (e *engine) backward(ctx context.Context) error {
	req := &backwarder.Request{}
	rsp, err := e.backwarder.Backward(ctx, req)
	if err != nil {
		return err
	}
	_ = rsp
	return nil
}
