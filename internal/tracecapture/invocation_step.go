//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tracecapture

import (
	"context"
	"sync"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// StepBinding identifies the execution trace step for one structural node
// visit. It is kept separate from Capture so cloned invocations can share the
// capture without inheriting the current visit.
type StepBinding struct {
	mu     sync.Mutex
	stepID string
}

// NewStepBinding creates an empty step binding.
func NewStepBinding() *StepBinding {
	return &StepBinding{}
}

// StepLease records whether a caller created and owns a step. Borrowed leases
// must not finish or release their step.
type StepLease struct {
	StepID string
	Owns   bool

	binding *StepBinding
}

type invocationRuntime struct {
	binding *StepBinding
	capture *Capture
}

type invocationRuntimeKey struct{}

// AttachInvocationRuntime attaches one invocation's private trace runtime to
// ctx. An empty runtime masks a parent's runtime when necessary.
func AttachInvocationRuntime(
	ctx context.Context,
	binding *StepBinding,
	capture *Capture,
) context.Context {
	if ctx != nil && binding == nil && capture == nil {
		if _, ok := invocationRuntimeFromContext(ctx); !ok {
			return ctx
		}
	}
	return context.WithValue(ctx, invocationRuntimeKey{}, invocationRuntime{
		binding: binding,
		capture: capture,
	})
}

// BindInvocationStep binds an invocation runtime to an existing step.
func BindInvocationStep(ctx context.Context, stepID string) {
	if stepID == "" {
		return
	}
	runtime, ok := invocationRuntimeFromContext(ctx)
	if !ok || runtime.binding == nil {
		return
	}
	runtime.binding.mu.Lock()
	runtime.binding.stepID = stepID
	runtime.binding.mu.Unlock()
}

// EnsureInvocationStep borrows the bound step or starts and owns a new step.
func EnsureInvocationStep(
	ctx context.Context,
	start func() string,
) StepLease {
	if start == nil {
		return StepLease{}
	}
	runtime, ok := invocationRuntimeFromContext(ctx)
	if !ok || runtime.binding == nil {
		return StepLease{}
	}
	binding := runtime.binding
	binding.mu.Lock()
	defer binding.mu.Unlock()
	if binding.stepID != "" {
		return StepLease{
			StepID:  binding.stepID,
			binding: binding,
		}
	}
	stepID := start()
	if stepID == "" {
		return StepLease{}
	}
	binding.stepID = stepID
	return StepLease{
		StepID:  stepID,
		Owns:    true,
		binding: binding,
	}
}

// ReleaseInvocationStep clears an owned binding when it still refers to the
// leased step. Borrowed and stale leases are ignored.
func ReleaseInvocationStep(lease StepLease) {
	if !lease.Owns || lease.StepID == "" || lease.binding == nil {
		return
	}
	lease.binding.mu.Lock()
	if lease.binding.stepID == lease.StepID {
		lease.binding.stepID = ""
	}
	lease.binding.mu.Unlock()
}

// SetInvocationStepInput replaces the current step's input snapshot.
func SetInvocationStepInput(ctx context.Context, input *atrace.Snapshot) {
	capture, stepID := currentInvocationStep(ctx)
	if capture == nil || stepID == "" {
		return
	}
	capture.setStepInput(stepID, input)
}

// MergeInvocationStepAppliedSurfaceIDs merges applied surface IDs into the
// current step in stable first-seen order.
func MergeInvocationStepAppliedSurfaceIDs(ctx context.Context, surfaceIDs []string) {
	capture, stepID := currentInvocationStep(ctx)
	if capture == nil || stepID == "" {
		return
	}
	capture.mergeStepAppliedSurfaceIDs(stepID, surfaceIDs)
}

// AddInvocationStepUsage accumulates model usage into the current step.
func AddInvocationStepUsage(ctx context.Context, usage *model.Usage) {
	capture, stepID := currentInvocationStep(ctx)
	if capture == nil || stepID == "" {
		return
	}
	capture.addStepUsage(stepID, usage)
}

func invocationRuntimeFromContext(ctx context.Context) (invocationRuntime, bool) {
	if ctx == nil {
		return invocationRuntime{}, false
	}
	runtime, ok := ctx.Value(invocationRuntimeKey{}).(invocationRuntime)
	return runtime, ok
}

func currentInvocationStep(ctx context.Context) (*Capture, string) {
	runtime, ok := invocationRuntimeFromContext(ctx)
	if !ok || runtime.capture == nil || runtime.binding == nil {
		return nil, ""
	}
	runtime.binding.mu.Lock()
	stepID := runtime.binding.stepID
	runtime.binding.mu.Unlock()
	return runtime.capture, stepID
}
