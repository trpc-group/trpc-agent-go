//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package steer

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

// Cycle coordinates admission of a complete model response with new input.
// Its fields are protected by the owning queue's mutex.
type Cycle struct {
	queue       *Queue
	cancel      context.CancelFunc
	interrupted bool
	accepted    bool
}

func queueFor(inv *agent.Invocation) *Queue {
	q, _ := agent.GetStateValue[*Queue](inv, StateKeyQueuedUserMessages)
	return q
}

// EnableInterrupt enables model interruption for an attached queue.
func EnableInterrupt(inv *agent.Invocation) {
	if q := queueFor(inv); q != nil {
		q.mu.Lock()
		q.enabled = true
		q.mu.Unlock()
	}
}

// Enabled reports whether this invocation participates in model interruption.
func Enabled(inv *agent.Invocation) bool {
	q := queueFor(inv)
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.enabled
}

// BeginModel registers cancellation unless queued input must be consumed first.
func BeginModel(inv *agent.Invocation, cancel context.CancelFunc) (*Cycle, bool) {
	q := queueFor(inv)
	if q == nil {
		return nil, true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.enabled {
		return nil, true
	}
	if q.closed || len(q.messages) != 0 {
		return nil, false
	}
	c := &Cycle{queue: q, cancel: cancel}
	q.cycle = c
	return c, true
}

// Accept commits a complete response, protecting its entire tool batch.
func (c *Cycle) Accept() bool {
	if c == nil {
		return true
	}
	c.queue.mu.Lock()
	defer c.queue.mu.Unlock()
	if c.interrupted {
		return false
	}
	c.accepted = true
	return true
}

// Interrupted reports whether new input won response admission.
func (c *Cycle) Interrupted() bool {
	if c == nil {
		return false
	}
	c.queue.mu.Lock()
	defer c.queue.mu.Unlock()
	return c.interrupted
}

// End unregisters only this cycle, so stale cleanup cannot affect a new cycle.
func (c *Cycle) End() {
	if c == nil {
		return
	}
	c.queue.mu.Lock()
	defer c.queue.mu.Unlock()
	if c.queue.cycle == c {
		c.queue.cycle = nil
	}
}

// TryFinish atomically closes admission if no input remains. Borrowers leave
// admission open for their owner, as they do in Close.
func TryFinish(inv *agent.Invocation) bool {
	q := queueFor(inv)
	if q == nil {
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.enabled {
		return true
	}
	if len(q.messages) != 0 {
		return false
	}
	if !isBorrowed(inv) {
		q.closed = true
	}
	return true
}

// Fail records the first persistence/protocol failure for the active run.
func Fail(inv *agent.Invocation, err error) {
	q := queueFor(inv)
	if q == nil || err == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.enabled && q.err == nil {
		q.err = err
	}
}

// Err returns a failure that prevents further inference.
func Err(inv *agent.Invocation) error {
	q := queueFor(inv)
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.err
}

// Acknowledge completes a registered event receipt after runner processing.
func Acknowledge(inv *agent.Invocation, id string, err error) {
	q := queueFor(inv)
	if q == nil {
		return
	}
	q.mu.Lock()
	ch := q.receipts[id]
	delete(q.receipts, id)
	q.mu.Unlock()
	if ch != nil {
		ch <- err
	}
}

// EmitAndWait emits an event and requires an error-bearing runner receipt.
// Callers use this only when Enabled is true. Receipts stay inside the run;
// they are not serialized into events or shared with sub-agents.
func EmitAndWait(ctx context.Context, inv *agent.Invocation, out chan<- *event.Event, evt *event.Event, timeout time.Duration) (err error) {
	q := queueFor(inv)
	if q == nil {
		return fmt.Errorf("steer queue is not attached")
	}
	ch := make(chan error, 1)
	q.mu.Lock()
	if q.receipts == nil {
		q.receipts = make(map[string]chan error)
	}
	q.receipts[evt.ID] = ch
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		delete(q.receipts, evt.ID)
		q.mu.Unlock()
		Fail(inv, err)
	}()
	evt.RequiresCompletion = true
	if err = agent.EmitEvent(ctx, inv, out, evt); err != nil {
		return err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err = <-ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("timed out waiting for event persistence: %s", evt.ID)
	}
}
