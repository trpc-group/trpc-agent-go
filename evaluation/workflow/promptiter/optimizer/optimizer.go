//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package optimizer transforms aggregated gradients into patch suggestions for the target prompt.
package optimizer

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Optimizer converts aggregated gradients into patch updates for one surface.
type Optimizer interface {
	// Optimize generates one patch proposal for one surface request.
	Optimize(ctx context.Context, request *Request) (*Result, error)
}

// Request carries gradient and baseline surface context for optimization.
type Request struct {
	// Surface is the source surface baseline that may be changed.
	Surface *promptiter.Surface
	// Gradient is the merged signal that drives optimization decisions.
	Gradient *promptiter.AggregatedSurfaceGradient
}

// Result carries the patch suggestion for one optimized surface.
type Result struct {
	// Patch is the proposed change for the requested surface.
	Patch *promptiter.SurfacePatch
}

// optimizer is the default Optimizer implementation used by the engine.
type optimizer struct {
	// runner executes external inference needed to draft patch proposals.
	runner runner.Runner
}

// New creates an Optimizer instance bound to the provided runner.
func New(ctx context.Context, runner runner.Runner, opt ...option) (Optimizer, error) {
	return &optimizer{
		runner: runner,
	}, nil
}

// Optimize runs optimization logic and returns one patch proposal.
func (o *optimizer) Optimize(ctx context.Context, request *Request) (*Result, error) {
	return nil, nil
}
