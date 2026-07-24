//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
)

const (
	fakeGradient      = "make the instruction satisfy failed examples without regressing validation"
	fakeEngineVersion = "deterministic-gradient-audit-v1"
)

type deterministicBackwarder struct{}

func (b *deterministicBackwarder) Backward(
	ctx context.Context,
	request *backwarder.Request,
) (*backwarder.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, errors.New("backward request is nil")
	}
	gradients := make([]promptiter.SurfaceGradient, 0, len(request.AllowedGradientSurfaceIDs))
	for _, surfaceID := range request.AllowedGradientSurfaceIDs {
		if surfaceID == "" {
			return nil, errors.New("allowed gradient surface id is empty")
		}
		gradients = append(gradients, promptiter.SurfaceGradient{
			EvalSetID:  request.EvalSetID,
			EvalCaseID: request.EvalCaseID,
			StepID:     request.StepID,
			SurfaceID:  surfaceID,
			Severity:   promptiter.LossSeverityP1,
			Gradient:   fakeGradient,
		})
	}
	return &backwarder.Result{Gradients: gradients}, nil
}

type deterministicAggregator struct{}

func (a *deterministicAggregator) Aggregate(
	ctx context.Context,
	request *aggregator.Request,
) (*aggregator.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, errors.New("aggregation request is nil")
	}
	if request.SurfaceID == "" || request.NodeID == "" || len(request.Gradients) == 0 {
		return nil, errors.New("aggregation request is incomplete")
	}
	return &aggregator.Result{Gradient: &promptiter.AggregatedSurfaceGradient{
		SurfaceID: request.SurfaceID,
		NodeID:    request.NodeID,
		Type:      request.Type,
		Gradients: request.Gradients,
	}}, nil
}

type deterministicOptimizer struct {
	prompt  string
	attempt int
}

func (o *deterministicOptimizer) Optimize(
	ctx context.Context,
	request *optimizer.Request,
) (*optimizer.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil || request.Surface == nil || request.Gradient == nil {
		return nil, errors.New("optimizer request is incomplete")
	}
	if request.Surface.Type != astructure.SurfaceTypeInstruction {
		return nil, fmt.Errorf("unsupported target surface type %q", request.Surface.Type)
	}
	failures, err := gradientFailureCases(request.Gradient)
	if err != nil {
		return nil, err
	}
	prompt := o.prompt
	return &optimizer.Result{Patch: &promptiter.SurfacePatch{
		SurfaceID: request.Surface.SurfaceID,
		Value:     astructure.SurfaceValue{Text: &prompt},
		Reason: fmt.Sprintf("deterministic candidate for attempt %d from failed cases: %s",
			o.attempt, strings.Join(failures, ", ")),
	}}, nil
}

func gradientFailureCases(gradient *promptiter.AggregatedSurfaceGradient) ([]string, error) {
	if gradient == nil || len(gradient.Gradients) == 0 {
		return nil, errors.New("optimizer gradient has no failed cases")
	}
	unique := make(map[string]struct{}, len(gradient.Gradients))
	for index, item := range gradient.Gradients {
		caseID := strings.TrimSpace(item.EvalCaseID)
		if caseID == "" || strings.TrimSpace(item.Gradient) == "" {
			return nil, fmt.Errorf("optimizer gradient %d is incomplete", index)
		}
		unique[caseID] = struct{}{}
	}
	failures := make([]string, 0, len(unique))
	for caseID := range unique {
		failures = append(failures, caseID)
	}
	sort.Strings(failures)
	return failures, nil
}
