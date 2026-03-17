//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package backwarder computes backward propagation outputs from trace and gradient data.
package backwarder

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Backwarder computes gradient attribution for one trace step.
type Backwarder interface {
	// Backward derives surface gradients and upstream propagation packets.
	Backward(ctx context.Context, request *Request) (*Result, error)
}

// Request carries a single step trace context for local backwarding.
type Request struct {
	// EvalSetID identifies the evaluation set for trace correlation.
	EvalSetID string
	// EvalCaseID identifies the evaluation case for trace correlation.
	EvalCaseID string
	// Node is the static node that produced this step.
	Node *promptiter.StructureNode
	// StepID is the local step identifier in the trace.
	StepID string
	// Input is the concrete input captured for this step.
	Input *promptiter.TraceInput
	// Output is the concrete output captured for this step.
	Output *promptiter.TraceOutput
	// Error records runtime failure details when the step did not complete.
	Error string
	// Surfaces are all surfaces whose values affected this step.
	Surfaces []promptiter.Surface
	// Predecessors stores direct predecessor execution steps.
	Predecessors []Predecessor
	// Incoming carries raw gradients that need to be processed at this step.
	Incoming []GradientPacket
}

// Predecessor captures one direct upstream step for gradient propagation.
type Predecessor struct {
	// StepID identifies the predecessor step id.
	StepID string
	// NodeID identifies the predecessor node id.
	NodeID string
	// Output stores the predecessor output used by this step.
	Output *promptiter.TraceOutput
	// Error records predecessor execution error for debugging.
	Error string
}

// GradientPacket carries one scalar gradient unit passed from downstream.
type GradientPacket struct {
	// FromStepID identifies the direct downstream step origin.
	FromStepID string
	// Severity carries failure importance for weighted propagation.
	Severity promptiter.LossSeverity
	// Gradient is the serialized propagated gradient string.
	Gradient string
}

// Result contains gradients attributed to current step and upstream propagation data.
type Result struct {
	// Gradients stores gradients mapped to surfaces affected by this step.
	Gradients []promptiter.SurfaceGradient
	// Upstream carries gradients that still need to propagate to predecessors.
	Upstream []Propagation
}

// Propagation groups packets to be sent to one predecessor step.
type Propagation struct {
	// PredecessorStepID identifies the target upstream step.
	PredecessorStepID string
	// Gradients stores packets forwarded to that predecessor.
	Gradients []GradientPacket
}

// backwarder is the default Backwarder implementation used by the engine.
type backwarder struct {
	// runner executes any external inference used during backward computation.
	runner runner.Runner
}

// New creates a Backwarder instance with injected runner and options.
func New(ctx context.Context, runner runner.Runner, opt ...option) (Backwarder, error) {
	return &backwarder{
		runner: runner,
	}, nil
}

// Backward computes local gradients and upstream propagation paths for one step.
func (b *backwarder) Backward(ctx context.Context, request *Request) (*Result, error) {
	return nil, nil
}
