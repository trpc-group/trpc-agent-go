//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/finalization"
	"trpc.group/trpc-go/trpc-agent-go/internal/tooljournal"
)

// ExecutionObserver is an optional capability for runners configured with
// WithToolExecutionTracking. ManagedRunner's method set remains unchanged.
type ExecutionObserver interface {
	// ExecutionStatus includes retained completed runs; false means tracking is
	// disabled or the ID is unknown/expired. It is not a durable cross-process lookup.
	ExecutionStatus(requestID string) (ExecutionStatus, bool)
	// WaitExecution waits for upstream execution and output finalization to exit.
	// A cancelled wait does not cancel execution. PersistenceError may be nonempty
	// even when waiting succeeds. Unknown/expired IDs return ErrExecutionNotFound.
	WaitExecution(context.Context, string) (ExecutionStatus, error)
}

// ErrExecutionNotFound indicates an unknown, expired or untracked execution.
var ErrExecutionNotFound = errors.New("execution is not tracked or has expired")

// ExecutionStatus separates cancellation, output closure, execution settlement
// and acknowledged persistence. Settlement describes framework-owned work;
// custom agents must keep their event channel open until their workers exit.
// It does not prove remote side effects completed or were rolled back.
type ExecutionStatus struct {
	RequestID             string
	CancellationRequested bool
	OutputClosed          bool
	ExecutionSettled      bool
	ActiveTools           int
	// PersistenceError records a failed or uncertain append acknowledgement.
	// Empty means no observed append error, not a remote durability guarantee.
	PersistenceError string
}

const executionRetention = 10 * time.Minute
const executionCapacity = 128

type executionObservation struct {
	mu          sync.Mutex
	status      ExecutionStatus
	inputClosed bool
	completedAt time.Time
	done        chan struct{}
	invocation  *agent.Invocation
}

func (o *executionObservation) complete(input, output, cancelled bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.inputClosed = o.inputClosed || input
	o.status.OutputClosed = o.status.OutputClosed || output
	o.status.CancellationRequested = o.status.CancellationRequested || cancelled
	if o.inputClosed && o.status.OutputClosed && !o.status.ExecutionSettled && tooljournal.Active(o.invocation) == 0 && tooljournal.Flows(o.invocation) == 0 {
		o.status.ExecutionSettled = true
		o.completedAt = time.Now()
		close(o.done)
	}
}

func (o *executionObservation) snapshot() ExecutionStatus {
	o.mu.Lock()
	status, inv := o.status, o.invocation
	o.mu.Unlock()
	if inv != nil {
		status.ActiveTools = tooljournal.Active(inv)
		if err := finalization.FromInvocation(inv).Err(); err != nil {
			status.PersistenceError = err.Error()
		}
	}
	return status
}

// reserveExecution never evicts unsettled work to make capacity look available.
func (r *runner) reserveExecution(requestID string) (*executionObservation, error) {
	if !r.toolExecutionTracking {
		return nil, nil
	}
	r.observationsMu.Lock()
	defer r.observationsMu.Unlock()
	if r.observations == nil {
		r.observations = make(map[string]*executionObservation)
	}
	r.pruneExecutions(time.Now())
	if _, exists := r.observations[requestID]; exists {
		return nil, fmt.Errorf("request id %q still has a retained execution", requestID)
	}
	if len(r.observations) >= executionCapacity {
		oldestID := ""
		var oldest time.Time
		for id, o := range r.observations {
			o.mu.Lock()
			at := o.completedAt
			o.mu.Unlock()
			if !at.IsZero() && (oldest.IsZero() || at.Before(oldest)) {
				oldestID = id
				oldest = at
			}
		}
		if oldestID == "" {
			return nil, errors.New("execution tracking capacity exhausted by unsettled runs")
		}
		delete(r.observations, oldestID)
	}
	o := &executionObservation{status: ExecutionStatus{RequestID: requestID}, done: make(chan struct{})}
	r.observations[requestID] = o
	return o, nil
}

func (r *runner) pruneExecutions(now time.Time) {
	for id, o := range r.observations {
		o.mu.Lock()
		at := o.completedAt
		o.mu.Unlock()
		if !at.IsZero() && now.Sub(at) >= executionRetention {
			delete(r.observations, id)
		}
	}
}

func (r *runner) execution(requestID string) *executionObservation {
	r.observationsMu.Lock()
	defer r.observationsMu.Unlock()
	r.pruneExecutions(time.Now())
	return r.observations[requestID]
}

func (r *runner) ExecutionStatus(requestID string) (ExecutionStatus, bool) {
	o := r.execution(requestID)
	if o == nil {
		return ExecutionStatus{}, false
	}
	return o.snapshot(), true
}

func (r *runner) WaitExecution(ctx context.Context, requestID string) (ExecutionStatus, error) {
	o := r.execution(requestID)
	if o == nil {
		return ExecutionStatus{}, ErrExecutionNotFound
	}
	select {
	case <-o.done:
		return o.snapshot(), nil
	case <-ctx.Done():
		return o.snapshot(), ctx.Err()
	}
}

// observeExecution keeps draining upstream after public output closes. LLMAgent
// also holds a tracked flow lease until its managed work has returned.
func observeExecution(ctx context.Context, inv *agent.Invocation, in <-chan *event.Event, o *executionObservation) <-chan *event.Event {
	if o == nil {
		return in
	}
	o.mu.Lock()
	o.invocation = inv
	o.mu.Unlock()
	tooljournal.OnChange(inv, func() { o.complete(false, false, false) })
	out := make(chan *event.Event, cap(in))
	go func() {
		defer close(out)
		defer o.complete(true, false, false)
		for e := range in {
			select {
			case out <- e:
			case <-ctx.Done():
				o.complete(false, false, true)
				for range in {
				}
				return
			}
		}
	}()
	return out
}
