//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package fakemodel

import (
	"context"
	"errors"
	"strings"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
)

// DeterministicBackwarder emits one fixed gradient for the target surface on each failing step,
// implementing backwarder.Backwarder with no model.
type DeterministicBackwarder struct {
	// TargetSurfaceID is the surface (candidate instruction) gradients attach to.
	TargetSurfaceID string
}

// Backward returns a single P1 gradient for the target surface when the step touched it.
func (b DeterministicBackwarder) Backward(_ context.Context, request *backwarder.Request) (*backwarder.Result, error) {
	if request == nil {
		return nil, errors.New("backward request is nil")
	}
	gradients := make([]promptiter.SurfaceGradient, 0, 1)
	for _, surface := range request.Surfaces {
		if surface.SurfaceID != b.TargetSurfaceID {
			continue
		}
		gradients = append(gradients, promptiter.SurfaceGradient{
			EvalSetID:  request.EvalSetID,
			EvalCaseID: request.EvalCaseID,
			StepID:     request.StepID,
			SurfaceID:  surface.SurfaceID,
			Severity:   promptiter.LossSeverityP1,
			Gradient:   "The current instruction is too vague; make the required output format explicit.",
		})
	}
	return &backwarder.Result{
		Gradients: gradients,
		Upstream:  []backwarder.Propagation{},
	}, nil
}

// DeterministicAggregator forwards the collected gradients unchanged, implementing
// aggregator.Aggregator with no model.
type DeterministicAggregator struct{}

// Aggregate wraps the request gradients into a single aggregated surface gradient.
func (DeterministicAggregator) Aggregate(_ context.Context, request *aggregator.Request) (*aggregator.Result, error) {
	if request == nil {
		return nil, errors.New("aggregate request is nil")
	}
	return &aggregator.Result{
		Gradient: &promptiter.AggregatedSurfaceGradient{
			SurfaceID: request.SurfaceID,
			NodeID:    request.NodeID,
			Type:      request.Type,
			Gradients: request.Gradients,
		},
	}, nil
}

// DeterministicOptimizer proposes the next instruction from a fixture transition table,
// implementing optimizer.Optimizer with no model.
type DeterministicOptimizer struct {
	// Transitions map the current instruction to a proposed replacement.
	Transitions []Transition
}

// Optimize returns a patch that rewrites the instruction surface per the transition table.
// When no transition matches, it proposes the current text unchanged (yielding no score gain,
// which the engine rejects) so the loop converges deterministically.
func (o DeterministicOptimizer) Optimize(_ context.Context, request *optimizer.Request) (*optimizer.Result, error) {
	if request == nil || request.Surface == nil {
		return nil, errors.New("optimize request or surface is nil")
	}
	current := ""
	if request.Surface.Value.Text != nil {
		current = *request.Surface.Value.Text
	}
	next, reason := current, "no beneficial change identified for this surface"
	for _, transition := range o.Transitions {
		if transition.FromContains != "" && !strings.Contains(current, transition.FromContains) {
			continue
		}
		next, reason = transition.ToInstruction, transition.Reason
		break
	}
	text := next
	return &optimizer.Result{
		Patch: &promptiter.SurfacePatch{
			SurfaceID: request.Surface.SurfaceID,
			Value:     astructure.SurfaceValue{Text: &text},
			Reason:    reason,
		},
	}, nil
}
