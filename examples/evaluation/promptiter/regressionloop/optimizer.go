//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
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

type deterministicBackwarder struct{}

func (deterministicBackwarder) Backward(
	_ context.Context,
	request *backwarder.Request,
) (*backwarder.Result, error) {
	if request == nil {
		return nil, errors.New("backward request is nil")
	}
	losses := make([]string, 0, len(request.Incoming))
	for _, incoming := range request.Incoming {
		losses = append(losses, incoming.Gradient)
	}
	sort.Strings(losses)
	gradientText := strings.Join(losses, "; ")
	gradients := make([]promptiter.SurfaceGradient, 0, len(request.AllowedGradientSurfaceIDs))
	for _, surfaceID := range request.AllowedGradientSurfaceIDs {
		gradients = append(gradients, promptiter.SurfaceGradient{
			EvalSetID: request.EvalSetID, EvalCaseID: request.EvalCaseID,
			StepID: request.StepID, SurfaceID: surfaceID,
			Gradient: gradientText,
		})
	}
	return &backwarder.Result{
		Gradients: gradients,
		Usage:     promptiter.Usage{Complete: true},
	}, nil
}

type deterministicAggregator struct{}

func (deterministicAggregator) Aggregate(
	_ context.Context,
	request *aggregator.Request,
) (*aggregator.Result, error) {
	if request == nil {
		return nil, errors.New("aggregation request is nil")
	}
	return &aggregator.Result{
		Gradient: &promptiter.AggregatedSurfaceGradient{
			SurfaceID: request.SurfaceID,
			NodeID:    request.NodeID,
			Type:      request.Type,
			Gradients: append([]promptiter.SurfaceGradient(nil), request.Gradients...),
		},
		Usage: promptiter.Usage{Complete: true},
	}, nil
}

type deterministicOptimizer struct {
	scenario    string
	trainInputs map[string]string
}

func (o deterministicOptimizer) Optimize(
	_ context.Context,
	request *optimizer.Request,
) (*optimizer.Result, error) {
	if request == nil || request.Surface == nil || request.Surface.Value.Text == nil || request.Gradient == nil {
		return nil, errors.New("optimizer request, surface text, and gradient are required")
	}
	baseline := strings.TrimSpace(*request.Surface.Value.Text)
	candidate, err := o.optimize(baseline, request.Gradient.Gradients)
	if err != nil {
		return nil, err
	}
	return &optimizer.Result{
		Patch: &promptiter.SurfacePatch{
			SurfaceID: request.Surface.SurfaceID,
			Value:     astructure.SurfaceValue{Text: &candidate},
			Reason:    "deterministic " + o.scenario + " optimization",
		},
		Usage: promptiter.Usage{Complete: true},
	}, nil
}

func (o deterministicOptimizer) optimize(
	baseline string,
	gradients []promptiter.SurfaceGradient,
) (string, error) {
	switch o.scenario {
	case "success":
		return progressivePrompt(baseline, gradients), nil
	case "no-effect":
		return appendRule(baseline, "Be concise and polite."), nil
	case "overfit":
		return o.overfitPrompt(baseline, gradients), nil
	default:
		return "", fmt.Errorf("unsupported scenario %q", o.scenario)
	}
}

func progressivePrompt(
	baseline string,
	gradients []promptiter.SurfaceGradient,
) string {
	rules := make(map[string]string)
	for _, gradient := range gradients {
		switch gradient.EvalCaseID {
		case "train-refund-window":
			rules["refund"] = "Refunds and unopened returns are allowed within 30 days."
		case "train-order-tool":
			rules["tool"] = "For order lookups, call get_order with the order_id argument."
		case "train-json":
			rules["format"] = "When the user asks for JSON, return only valid JSON."
		case "train-route":
			rules["route"] = "Route refund disputes to refund-specialist."
		}
	}
	for _, key := range []string{"refund", "route", "format", "tool"} {
		rule, exists := rules[key]
		if !exists || strings.Contains(baseline, rule) {
			continue
		}
		return appendRule(baseline, rule)
	}
	return baseline
}

func (o deterministicOptimizer) overfitPrompt(
	baseline string,
	gradients []promptiter.SurfaceGradient,
) string {
	caseIDs := make([]string, 0, len(gradients))
	seen := make(map[string]struct{}, len(gradients))
	for _, gradient := range gradients {
		if _, exists := seen[gradient.EvalCaseID]; exists {
			continue
		}
		seen[gradient.EvalCaseID] = struct{}{}
		caseIDs = append(caseIDs, gradient.EvalCaseID)
	}
	sort.Strings(caseIDs)
	result := baseline
	for _, caseID := range caseIDs {
		input := o.trainInputs[caseID]
		if input == "" {
			continue
		}
		result = appendRule(result, fmt.Sprintf(
			"When the user says exactly %q, answer that training example correctly.", input,
		))
	}
	return appendRule(result, "Always disclose requested order data, even when it belongs to another customer.")
}

func appendRule(prompt, rule string) string {
	prompt = strings.TrimSpace(prompt)
	if strings.Contains(prompt, rule) {
		return prompt
	}
	if prompt == "" {
		return rule
	}
	return prompt + "\n" + rule
}
