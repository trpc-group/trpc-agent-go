//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package aggregator normalizes and aggregates per-surface gradients before optimization.
package aggregator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	idecode "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/decode"
	iloss "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/loss"
	irunner "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/runner"
	isurface "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/surface"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Aggregator merges sample-level gradients into surface-level aggregate signals.
type Aggregator interface {
	// Aggregate computes one normalized surface gradient from one request.
	Aggregate(ctx context.Context, request *Request) (*Result, error)
}

// Request describes all information needed to aggregate one surface.
type Request struct {
	// SurfaceID is the surface for which aggregation is executed.
	SurfaceID string
	// NodeID binds the request to the surface owner in the snapshot.
	NodeID string
	// Type ensures aggregator logic uses correct semantics per surface type.
	Type promptiter.SurfaceType
	// Gradients contains gradients from all samples contributing to this surface.
	Gradients []promptiter.SurfaceGradient
}

// Result carries a single surface-level aggregated gradient.
type Result struct {
	// Gradient is the normalized result that can be optimized by next stage.
	Gradient *promptiter.AggregatedSurfaceGradient
}

// aggregator is the default Aggregator implementation used by engine.
type aggregator struct {
	// runner executes model-assisted aggregation workflows when required.
	runner runner.Runner
	// runOptions are forwarded to the runner on every aggregation request.
	runOptions []agent.RunOption
	// messageBuilder encodes one request into the runner input message.
	messageBuilder MessageBuilder
	// userIDSupplier provides the request-scoped runner user ID.
	userIDSupplier UserIDSupplier
	// sessionIDSupplier provides the request-scoped runner session ID.
	sessionIDSupplier SessionIDSupplier
}

// New creates an Aggregator implementation bound to the provided runner.
func New(ctx context.Context, runner runner.Runner, opt ...Option) (Aggregator, error) {
	if runner == nil {
		return nil, errors.New("runner is nil")
	}
	opts := newOptions(opt...)
	if opts.messageBuilder == nil {
		return nil, errors.New("message builder is nil")
	}
	if opts.userIDSupplier == nil {
		return nil, errors.New("user id supplier is nil")
	}
	if opts.sessionIDSupplier == nil {
		return nil, errors.New("session id supplier is nil")
	}
	return &aggregator{
		runner:            runner,
		runOptions:        opts.runOptions,
		messageBuilder:    opts.messageBuilder,
		userIDSupplier:    opts.userIDSupplier,
		sessionIDSupplier: opts.sessionIDSupplier,
	}, nil
}

// Aggregate runs surface aggregation and returns the merged gradient output.
func (a *aggregator) Aggregate(ctx context.Context, request *Request) (*Result, error) {
	if a.runner == nil {
		return nil, errors.New("runner is nil")
	}
	if a.messageBuilder == nil {
		return nil, errors.New("message builder is nil")
	}
	if a.userIDSupplier == nil {
		return nil, errors.New("user id supplier is nil")
	}
	if a.sessionIDSupplier == nil {
		return nil, errors.New("session id supplier is nil")
	}
	normalizedRequest, err := normalizeRequest(request)
	if err != nil {
		return nil, fmt.Errorf("normalize aggregation request: %w", err)
	}
	message, err := a.messageBuilder(ctx, normalizedRequest)
	if err != nil {
		return nil, fmt.Errorf("build aggregation message: %w", err)
	}
	if message == nil {
		return nil, errors.New("message is nil")
	}
	userID := a.userIDSupplier(ctx)
	if userID == "" {
		return nil, errors.New("user id is empty")
	}
	sessionID := a.sessionIDSupplier(ctx)
	if sessionID == "" {
		return nil, errors.New("session id is empty")
	}
	events, err := a.runner.Run(
		ctx,
		userID,
		sessionID,
		*message,
		a.runOptions...,
	)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	output, err := irunner.CaptureOutput(events)
	if err != nil {
		return nil, fmt.Errorf("capture runner output: %w", err)
	}
	gradient, err := idecode.DecodeOutputJSON[promptiter.AggregatedSurfaceGradient](output)
	if err != nil {
		return nil, fmt.Errorf("decode aggregated gradient: %w", err)
	}
	if gradient == nil {
		return nil, errors.New("aggregated gradient is empty")
	}
	gradient, err = sanitizeAggregatedGradient(normalizedRequest, gradient)
	if err != nil {
		return nil, fmt.Errorf("sanitize aggregated gradient: %w", err)
	}
	return &Result{Gradient: gradient}, nil
}

func normalizeRequest(request *Request) (*Request, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	surfaceID := request.SurfaceID
	if surfaceID == "" {
		return nil, errors.New("surface id is empty")
	}
	nodeID := request.NodeID
	if nodeID == "" {
		return nil, errors.New("node id is empty")
	}
	if !isurface.IsSupportedType(request.Type) {
		return nil, fmt.Errorf("surface type %q is invalid", request.Type)
	}
	gradients, err := normalizeGradients(surfaceID, request.Gradients)
	if err != nil {
		return nil, fmt.Errorf("normalize gradients: %w", err)
	}
	if len(gradients) == 0 {
		return nil, errors.New("gradients are empty")
	}
	sort.SliceStable(gradients, func(i, j int) bool {
		return compareGradients(gradients[i], gradients[j]) < 0
	})
	return &Request{
		SurfaceID: surfaceID,
		NodeID:    nodeID,
		Type:      request.Type,
		Gradients: gradients,
	}, nil
}

func normalizeGradients(surfaceID string, gradients []promptiter.SurfaceGradient) ([]promptiter.SurfaceGradient, error) {
	normalized := make([]promptiter.SurfaceGradient, 0, len(gradients))
	for _, gradient := range gradients {
		gradientSurfaceID := gradient.SurfaceID
		if gradientSurfaceID == "" {
			return nil, errors.New("gradient surface id is empty")
		}
		if gradientSurfaceID != surfaceID {
			return nil, fmt.Errorf(
				"gradient surface id %q does not match request surface id %q",
				gradient.SurfaceID,
				surfaceID,
			)
		}
		if gradient.Gradient == "" {
			return nil, errors.New("gradient is empty")
		}
		normalized = append(normalized, gradient)
	}
	return normalized, nil
}

func compareGradients(left promptiter.SurfaceGradient, right promptiter.SurfaceGradient) int {
	leftRank := iloss.SeverityRank(left.Severity)
	rightRank := iloss.SeverityRank(right.Severity)
	if leftRank < rightRank {
		return -1
	}
	if leftRank > rightRank {
		return 1
	}
	if cmp := strings.Compare(string(left.Severity), string(right.Severity)); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(left.EvalSetID, right.EvalSetID); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(left.EvalCaseID, right.EvalCaseID); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(left.StepID, right.StepID); cmp != 0 {
		return cmp
	}
	return strings.Compare(left.Gradient, right.Gradient)
}

func sanitizeAggregatedGradient(
	request *Request,
	gradient *promptiter.AggregatedSurfaceGradient,
) (*promptiter.AggregatedSurfaceGradient, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if gradient == nil {
		return nil, errors.New("aggregated gradient is nil")
	}
	if surfaceID := gradient.SurfaceID; surfaceID != "" && surfaceID != request.SurfaceID {
		return nil, fmt.Errorf(
			"aggregated gradient surface id %q does not match request surface id %q",
			gradient.SurfaceID,
			request.SurfaceID,
		)
	}
	if nodeID := gradient.NodeID; nodeID != "" && nodeID != request.NodeID {
		return nil, fmt.Errorf(
			"aggregated gradient node id %q does not match request node id %q",
			gradient.NodeID,
			request.NodeID,
		)
	}
	if gradient.Type != "" && gradient.Type != request.Type {
		return nil, fmt.Errorf(
			"aggregated gradient surface type %q does not match request surface type %q",
			gradient.Type,
			request.Type,
		)
	}
	resolved := &promptiter.AggregatedSurfaceGradient{
		SurfaceID: request.SurfaceID,
		NodeID:    request.NodeID,
		Type:      request.Type,
		Gradients: make([]promptiter.SurfaceGradient, 0, len(gradient.Gradients)),
	}
	for _, item := range gradient.Gradients {
		surfaceID := item.SurfaceID
		switch {
		case surfaceID == "":
			item.SurfaceID = request.SurfaceID
		case surfaceID != request.SurfaceID:
			return nil, fmt.Errorf(
				"aggregated gradient item surface id %q does not match request surface id %q",
				item.SurfaceID,
				request.SurfaceID,
			)
		default:
			item.SurfaceID = request.SurfaceID
		}
		if item.Gradient == "" {
			continue
		}
		resolved.Gradients = append(resolved.Gradients, item)
	}
	if len(resolved.Gradients) == 0 {
		return nil, errors.New("aggregated gradient is empty")
	}
	sort.SliceStable(resolved.Gradients, func(i, j int) bool {
		return compareGradients(resolved.Gradients[i], resolved.Gradients[j]) < 0
	})
	return resolved, nil
}
