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
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	idecode "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/decode"
	irunner "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/runner"
	isurface "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/surface"
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
	Node *astructure.Node
	// StepID is the local step identifier in the trace.
	StepID string
	// Input is the concrete input snapshot captured for this step.
	Input *atrace.Snapshot
	// Output is the concrete output snapshot captured for this step.
	Output *atrace.Snapshot
	// Error records runtime failure details when the step did not complete.
	Error string
	// Surfaces are all surfaces whose values affected this step.
	Surfaces []astructure.Surface
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
	// Output stores the predecessor output snapshot used by this step.
	Output *atrace.Snapshot
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
	// runOptions are forwarded to the runner on every backward request.
	runOptions []agent.RunOption
	// messageBuilder encodes one request into the runner input message.
	messageBuilder MessageBuilder
	// userIDSupplier provides the request-scoped runner user ID.
	userIDSupplier UserIDSupplier
	// sessionIDSupplier provides the request-scoped runner session ID.
	sessionIDSupplier SessionIDSupplier
}

// New creates a Backwarder instance with injected runner and options.
func New(ctx context.Context, runner runner.Runner, opt ...Option) (Backwarder, error) {
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
	return &backwarder{
		runner:            runner,
		runOptions:        opts.runOptions,
		messageBuilder:    opts.messageBuilder,
		userIDSupplier:    opts.userIDSupplier,
		sessionIDSupplier: opts.sessionIDSupplier,
	}, nil
}

// Backward computes local gradients and upstream propagation paths for one step.
func (b *backwarder) Backward(ctx context.Context, request *Request) (*Result, error) {
	if b.runner == nil {
		return nil, errors.New("runner is nil")
	}
	if b.messageBuilder == nil {
		return nil, errors.New("message builder is nil")
	}
	if b.userIDSupplier == nil {
		return nil, errors.New("user id supplier is nil")
	}
	if b.sessionIDSupplier == nil {
		return nil, errors.New("session id supplier is nil")
	}
	normalizedRequest, err := normalizeRequest(request)
	if err != nil {
		return nil, fmt.Errorf("normalize backward request: %w", err)
	}
	message, err := b.messageBuilder(ctx, normalizedRequest)
	if err != nil {
		return nil, fmt.Errorf("build backward message: %w", err)
	}
	if message == nil {
		return nil, errors.New("message is nil")
	}
	userID := b.userIDSupplier(ctx)
	if userID == "" {
		return nil, errors.New("user id is empty")
	}
	sessionID := b.sessionIDSupplier(ctx)
	if sessionID == "" {
		return nil, errors.New("session id is empty")
	}
	events, err := b.runner.Run(
		ctx,
		userID,
		sessionID,
		*message,
		b.runOptions...,
	)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	output, err := irunner.CaptureOutput(events)
	if err != nil {
		return nil, fmt.Errorf("capture runner output: %w", err)
	}
	result, err := idecode.DecodeOutputJSON[Result](output)
	if err != nil {
		return nil, fmt.Errorf("decode backward result: %w", err)
	}
	if result == nil {
		return nil, errors.New("backward result is empty")
	}
	result, err = sanitizeBackwardResult(normalizedRequest, result)
	if err != nil {
		return nil, fmt.Errorf("sanitize backward result: %w", err)
	}
	return result, nil
}

func normalizeRequest(request *Request) (*Request, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if request.EvalSetID == "" {
		return nil, errors.New("eval set id is empty")
	}
	if request.EvalCaseID == "" {
		return nil, errors.New("eval case id is empty")
	}
	if request.Node == nil {
		return nil, errors.New("node is nil")
	}
	if request.Node.NodeID == "" {
		return nil, errors.New("node id is empty")
	}
	if request.StepID == "" {
		return nil, errors.New("step id is empty")
	}
	if request.Input == nil {
		return nil, errors.New("input is nil")
	}
	if _, err := isurface.BuildIndex(request.Surfaces); err != nil {
		return nil, fmt.Errorf("build surface index: %w", err)
	}
	if _, err := buildPredecessorIndex(request.Predecessors); err != nil {
		return nil, fmt.Errorf("build predecessor index: %w", err)
	}
	if len(request.Incoming) == 0 {
		return nil, errors.New("incoming gradients are empty")
	}
	for _, packet := range request.Incoming {
		if packet.FromStepID == "" {
			return nil, errors.New("incoming gradient from step id is empty")
		}
		if packet.Gradient == "" {
			return nil, errors.New("incoming gradient is empty")
		}
	}
	return request, nil
}

func sanitizeBackwardResult(request *Request, result *Result) (*Result, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if result == nil {
		return nil, errors.New("backward result is nil")
	}
	surfaceIndex, err := isurface.BuildIndex(request.Surfaces)
	if err != nil {
		return nil, fmt.Errorf("build surface index: %w", err)
	}
	predecessorIndex, err := buildPredecessorIndex(request.Predecessors)
	if err != nil {
		return nil, fmt.Errorf("build predecessor index: %w", err)
	}
	sanitized := &Result{
		Gradients: make([]promptiter.SurfaceGradient, 0, len(result.Gradients)),
		Upstream:  make([]Propagation, 0, len(result.Upstream)),
	}
	for _, gradient := range result.Gradients {
		sanitizedGradient, keep, err := sanitizeSurfaceGradient(request, surfaceIndex, gradient)
		if err != nil {
			return nil, fmt.Errorf("sanitize surface gradient: %w", err)
		}
		if !keep {
			continue
		}
		sanitized.Gradients = append(sanitized.Gradients, sanitizedGradient)
	}
	if len(predecessorIndex) == 0 && len(result.Upstream) > 0 {
		return nil, errors.New("upstream propagations are not allowed without predecessors")
	}
	seenPredecessors := make(map[string]struct{}, len(result.Upstream))
	for _, propagation := range result.Upstream {
		sanitizedPropagation, keep, err := sanitizePropagation(request, predecessorIndex, propagation)
		if err != nil {
			return nil, fmt.Errorf("sanitize propagation: %w", err)
		}
		if !keep {
			continue
		}
		if _, ok := seenPredecessors[sanitizedPropagation.PredecessorStepID]; ok {
			return nil, fmt.Errorf(
				"duplicate propagation for predecessor step id %q",
				sanitizedPropagation.PredecessorStepID,
			)
		}
		seenPredecessors[sanitizedPropagation.PredecessorStepID] = struct{}{}
		sanitized.Upstream = append(sanitized.Upstream, sanitizedPropagation)
	}
	if len(sanitized.Gradients) == 0 && len(sanitized.Upstream) == 0 {
		return nil, errors.New("backward result is empty")
	}
	return sanitized, nil
}

func sanitizeSurfaceGradient(
	request *Request,
	surfaceIndex map[string]astructure.Surface,
	gradient promptiter.SurfaceGradient,
) (promptiter.SurfaceGradient, bool, error) {
	if gradient.Gradient == "" {
		return promptiter.SurfaceGradient{}, false, nil
	}
	if evalSetID := gradient.EvalSetID; evalSetID != "" && evalSetID != request.EvalSetID {
		return promptiter.SurfaceGradient{}, false, fmt.Errorf(
			"gradient eval set id %q does not match request eval set id %q",
			gradient.EvalSetID,
			request.EvalSetID,
		)
	}
	if evalCaseID := gradient.EvalCaseID; evalCaseID != "" && evalCaseID != request.EvalCaseID {
		return promptiter.SurfaceGradient{}, false, fmt.Errorf(
			"gradient eval case id %q does not match request eval case id %q",
			gradient.EvalCaseID,
			request.EvalCaseID,
		)
	}
	if stepID := gradient.StepID; stepID != "" && stepID != request.StepID {
		return promptiter.SurfaceGradient{}, false, fmt.Errorf(
			"gradient step id %q does not match request step id %q",
			gradient.StepID,
			request.StepID,
		)
	}
	surfaceID, err := sanitizeGradientSurfaceID(surfaceIndex, gradient.SurfaceID)
	if err != nil {
		return promptiter.SurfaceGradient{}, false, fmt.Errorf("sanitize gradient surface id: %w", err)
	}
	return promptiter.SurfaceGradient{
		EvalSetID:  request.EvalSetID,
		EvalCaseID: request.EvalCaseID,
		StepID:     request.StepID,
		SurfaceID:  surfaceID,
		Severity:   gradient.Severity,
		Gradient:   gradient.Gradient,
	}, true, nil
}

func sanitizePropagation(
	request *Request,
	predecessorIndex map[string]Predecessor,
	propagation Propagation,
) (Propagation, bool, error) {
	predecessorStepID, err := sanitizePropagationPredecessorStepID(predecessorIndex, propagation.PredecessorStepID)
	if err != nil {
		return Propagation{}, false, fmt.Errorf("sanitize propagation predecessor step id: %w", err)
	}
	sanitized := Propagation{
		PredecessorStepID: predecessorStepID,
		Gradients:         make([]GradientPacket, 0, len(propagation.Gradients)),
	}
	for _, packet := range propagation.Gradients {
		if packet.Gradient == "" {
			continue
		}
		if fromStepID := packet.FromStepID; fromStepID != "" && fromStepID != request.StepID {
			return Propagation{}, false, fmt.Errorf(
				"propagation packet from step id %q does not match request step id %q",
				packet.FromStepID,
				request.StepID,
			)
		}
		sanitized.Gradients = append(sanitized.Gradients, GradientPacket{
			FromStepID: request.StepID,
			Severity:   packet.Severity,
			Gradient:   packet.Gradient,
		})
	}
	if len(sanitized.Gradients) == 0 {
		return Propagation{}, false, nil
	}
	return sanitized, true, nil
}

func buildPredecessorIndex(predecessors []Predecessor) (map[string]Predecessor, error) {
	index := make(map[string]Predecessor, len(predecessors))
	for _, predecessor := range predecessors {
		if predecessor.StepID == "" {
			return nil, errors.New("predecessor step id is empty")
		}
		if predecessor.NodeID == "" {
			return nil, errors.New("predecessor node id is empty")
		}
		if _, ok := index[predecessor.StepID]; ok {
			return nil, fmt.Errorf("duplicate predecessor step id %q", predecessor.StepID)
		}
		index[predecessor.StepID] = predecessor
	}
	return index, nil
}

func sanitizeGradientSurfaceID(
	surfaceIndex map[string]astructure.Surface,
	surfaceID string,
) (string, error) {
	switch {
	case surfaceID == "" && len(surfaceIndex) == 1:
		for id := range surfaceIndex {
			return id, nil
		}
	case surfaceID == "":
		return "", errors.New("gradient surface id is empty")
	default:
		if _, ok := surfaceIndex[surfaceID]; !ok {
			return "", fmt.Errorf("gradient surface id %q is not part of request surfaces", surfaceID)
		}
		return surfaceID, nil
	}
	return "", errors.New("gradient surface id is empty")
}

func sanitizePropagationPredecessorStepID(
	predecessorIndex map[string]Predecessor,
	predecessorStepID string,
) (string, error) {
	switch {
	case predecessorStepID == "" && len(predecessorIndex) == 1:
		for stepID := range predecessorIndex {
			return stepID, nil
		}
	case predecessorStepID == "":
		return "", errors.New("propagation predecessor step id is empty")
	default:
		if _, ok := predecessorIndex[predecessorStepID]; !ok {
			return "", fmt.Errorf(
				"propagation predecessor step id %q is not part of request predecessors",
				predecessorStepID,
			)
		}
		return predecessorStepID, nil
	}
	return "", errors.New("propagation predecessor step id is empty")
}
