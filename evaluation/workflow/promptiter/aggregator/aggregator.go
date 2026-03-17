//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package aggregator consolidates gradients and scores produced by sampler traces before optimization.
package aggregator

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
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
}

// New creates an Aggregator implementation bound to the provided runner.
func New(ctx context.Context, runner runner.Runner, opt ...option) (Aggregator, error) {
	return &aggregator{
		runner: runner,
	}, nil
}

// Aggregate runs surface aggregation and returns the merged gradient output.
func (a *aggregator) Aggregate(ctx context.Context, request *Request) (*Result, error) {
	return nil, nil
}
