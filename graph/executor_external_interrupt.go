//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	// ExternalInterruptKey identifies interrupts requested via
	// WithGraphInterrupt.
	ExternalInterruptKey = "external_interrupt"

	// CheckpointMetaKeyGraphInterruptInputs stores per-node input snapshots that
	// should be restored when resuming after a forced external interrupt.
	CheckpointMetaKeyGraphInterruptInputs = "graph_interrupt_inputs"
)

var errGraphInterruptTimeout = errors.New("graph interrupt timeout")

type externalInterruptWatcher struct {
	state *graphInterruptState

	stopOnce sync.Once
	stopCh   chan struct{}

	cancel context.CancelCauseFunc
}

func newExternalInterruptWatcher(
	parent context.Context,
	state *graphInterruptState,
) (context.Context, *externalInterruptWatcher) {
	if state == nil {
		return parent, nil
	}

	runCtx, cancel := context.WithCancelCause(parent)
	w := &externalInterruptWatcher{
		state:  state,
		stopCh: make(chan struct{}),
		cancel: cancel,
	}
	go w.listen()
	return runCtx, w
}

func (w *externalInterruptWatcher) listen() {
	select {
	case <-w.stopCh:
		return
	case <-w.state.doneCh():
	}

	timeout := w.state.timeoutOrNil()
	if timeout == nil {
		return
	}
	if *timeout <= 0 {
		w.cancel(errGraphInterruptTimeout)
		return
	}

	timer := time.NewTimer(*timeout)
	defer timer.Stop()

	select {
	case <-w.stopCh:
		return
	case <-timer.C:
		w.cancel(errGraphInterruptTimeout)
	}
}

func (w *externalInterruptWatcher) stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}

func (w *externalInterruptWatcher) requested() bool {
	if w == nil || w.state == nil {
		return false
	}
	return w.state.requested()
}

func (w *externalInterruptWatcher) forced(ctx context.Context) bool {
	if w == nil || ctx == nil {
		return false
	}
	return errors.Is(context.Cause(ctx), errGraphInterruptTimeout)
}

type stepExecutionReport struct {
	mu sync.Mutex

	completed map[*Task]bool
	inputs    map[*Task]State
	fields    map[string]StateField
}

func newStepExecutionReport(
	fields map[string]StateField,
) *stepExecutionReport {
	return &stepExecutionReport{
		completed: make(map[*Task]bool),
		inputs:    make(map[*Task]State),
		fields:    fields,
	}
}

func (r *stepExecutionReport) recordInput(task *Task, input State) {
	if r == nil || task == nil || task.NodeID == "" || input == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.inputs[task]; exists {
		return
	}
	r.inputs[task] = input.deepCopy(false, r.fields)
}

func (r *stepExecutionReport) markCompleted(task *Task) {
	if r == nil || task == nil {
		return
	}
	r.mu.Lock()
	r.completed[task] = true
	r.mu.Unlock()
}

func (r *stepExecutionReport) isCompleted(task *Task) bool {
	if r == nil || task == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.completed[task]
}

func (r *stepExecutionReport) inputFor(task *Task) (State, bool) {
	if r == nil || task == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	in, ok := r.inputs[task]
	return in, ok
}

// ExternalInterruptPayload is stored in InterruptError.Value for external
// interrupts requested via WithGraphInterrupt.
type ExternalInterruptPayload struct {
	Key    string `json:"key"`
	Forced bool   `json:"forced"`
}

func newExternalInterruptError(forced bool) *InterruptError {
	intr := NewInterruptError(ExternalInterruptPayload{
		Key:    ExternalInterruptKey,
		Forced: forced,
	})
	intr.Key = ExternalInterruptKey
	intr.TaskID = ExternalInterruptKey
	intr.SkipRerun = true
	return intr
}
