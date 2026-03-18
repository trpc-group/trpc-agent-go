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
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	idecode "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/decode"
	irunner "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/runner"
	isurface "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/surface"
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
	// runOptions are forwarded to the runner on every optimization request.
	runOptions []agent.RunOption
	// messageBuilder encodes one request into the runner input message.
	messageBuilder MessageBuilder
	// userIDSupplier provides the request-scoped runner user ID.
	userIDSupplier UserIDSupplier
	// sessionIDSupplier provides the request-scoped runner session ID.
	sessionIDSupplier SessionIDSupplier
}

// New creates an Optimizer instance bound to the provided runner.
func New(ctx context.Context, runner runner.Runner, opt ...Option) (Optimizer, error) {
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
	return &optimizer{
		runner:            runner,
		runOptions:        opts.runOptions,
		messageBuilder:    opts.messageBuilder,
		userIDSupplier:    opts.userIDSupplier,
		sessionIDSupplier: opts.sessionIDSupplier,
	}, nil
}

// Optimize runs optimization logic and returns one patch proposal.
func (o *optimizer) Optimize(ctx context.Context, request *Request) (*Result, error) {
	if o.runner == nil {
		return nil, errors.New("runner is nil")
	}
	if o.messageBuilder == nil {
		return nil, errors.New("message builder is nil")
	}
	if o.userIDSupplier == nil {
		return nil, errors.New("user id supplier is nil")
	}
	if o.sessionIDSupplier == nil {
		return nil, errors.New("session id supplier is nil")
	}
	normalizedRequest, err := normalizeRequest(request)
	if err != nil {
		return nil, fmt.Errorf("normalize optimization request: %w", err)
	}
	message, err := o.messageBuilder(ctx, normalizedRequest)
	if err != nil {
		return nil, fmt.Errorf("build optimization message: %w", err)
	}
	if message == nil {
		return nil, errors.New("message is nil")
	}
	userID := o.userIDSupplier(ctx)
	if userID == "" {
		return nil, errors.New("user id is empty")
	}
	sessionID := o.sessionIDSupplier(ctx)
	if sessionID == "" {
		return nil, errors.New("session id is empty")
	}
	events, err := o.runner.Run(
		ctx,
		userID,
		sessionID,
		*message,
		o.runOptions...,
	)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	output, err := irunner.CaptureOutput(events)
	if err != nil {
		return nil, fmt.Errorf("capture runner output: %w", err)
	}
	patch, err := idecode.DecodeOutputJSON[promptiter.SurfacePatch](output)
	if err != nil {
		return nil, fmt.Errorf("decode surface patch: %w", err)
	}
	if patch == nil {
		return nil, errors.New("surface patch is empty")
	}
	patch, err = sanitizeSurfacePatch(normalizedRequest, patch)
	if err != nil {
		return nil, fmt.Errorf("sanitize surface patch: %w", err)
	}
	return &Result{Patch: patch}, nil
}

func normalizeRequest(request *Request) (*Request, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if request.Surface == nil {
		return nil, errors.New("surface is nil")
	}
	if request.Gradient == nil {
		return nil, errors.New("aggregated gradient is nil")
	}
	surface := request.Surface
	gradient := request.Gradient
	if surface.SurfaceID == "" {
		return nil, errors.New("surface id is empty")
	}
	if surface.NodeID == "" {
		return nil, errors.New("node id is empty")
	}
	if !isurface.IsSupportedType(surface.Type) {
		return nil, fmt.Errorf("surface type %q is invalid", surface.Type)
	}
	if gradient.SurfaceID == "" {
		return nil, errors.New("aggregated gradient surface id is empty")
	}
	if gradient.SurfaceID != surface.SurfaceID {
		return nil, fmt.Errorf(
			"aggregated gradient surface id %q does not match surface id %q",
			gradient.SurfaceID,
			surface.SurfaceID,
		)
	}
	if gradient.NodeID == "" {
		return nil, errors.New("aggregated gradient node id is empty")
	}
	if gradient.NodeID != surface.NodeID {
		return nil, fmt.Errorf(
			"aggregated gradient node id %q does not match surface node id %q",
			gradient.NodeID,
			surface.NodeID,
		)
	}
	if gradient.Type != surface.Type {
		return nil, fmt.Errorf(
			"aggregated gradient surface type %q does not match surface type %q",
			gradient.Type,
			surface.Type,
		)
	}
	if len(gradient.Gradients) == 0 {
		return nil, errors.New("aggregated gradients are empty")
	}
	return request, nil
}

func sanitizeSurfacePatch(request *Request, patch *promptiter.SurfacePatch) (*promptiter.SurfacePatch, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if request.Surface == nil {
		return nil, errors.New("surface is nil")
	}
	if patch == nil {
		return nil, errors.New("surface patch is nil")
	}
	if surfaceID := patch.SurfaceID; surfaceID != "" && surfaceID != request.Surface.SurfaceID {
		return nil, fmt.Errorf(
			"patch surface id %q does not match surface id %q",
			patch.SurfaceID,
			request.Surface.SurfaceID,
		)
	}
	if patch.Reason == "" {
		return nil, errors.New("patch reason is empty")
	}
	value, err := isurface.SanitizeValue(request.Surface.Type, patch.Value)
	if err != nil {
		return nil, fmt.Errorf("sanitize patch value: %w", err)
	}
	return &promptiter.SurfacePatch{
		SurfaceID: request.Surface.SurfaceID,
		Value:     value,
		Reason:    patch.Reason,
	}, nil
}
