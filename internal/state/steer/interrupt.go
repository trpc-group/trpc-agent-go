//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
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
	if c.interrupted || c.queue.err != nil {
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

// Fail records the first persistence/protocol failure and cancels the active
// model request outside the queue lock. It does not signal a user interruption.
func Fail(inv *agent.Invocation, err error) {
	q := queueFor(inv)
	if q == nil || err == nil {
		return
	}
	q.mu.Lock()
	var cancel context.CancelFunc
	if q.enabled && q.err == nil {
		q.err = err
		if q.cycle != nil {
			cancel = q.cycle.cancel
		}
	}
	q.mu.Unlock()
	if cancel != nil {
		cancel()
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

type appendRecord struct {
	done     chan struct{}
	err      error
	panicked bool
}

type persistenceIdentityKey struct{}
type persistenceIdentity struct {
	eventID    string
	originalID string
}

type persistenceQueueKey struct{}
type persistenceQueue struct {
	invocationID string
	queue        *Queue
}

// WithPersistence retains the invocation's append coordination for an operation
// that may finish after Clear. It must be called before event delivery. The
// returned context preserves cancellation and does not keep the Run open; only
// AppendOnce for the same invocation can use the retained queue. Nil or
// unattached invocations leave ctx unchanged.
func WithPersistence(ctx context.Context, inv *agent.Invocation) context.Context {
	q := queueFor(inv)
	if q == nil {
		return ctx
	}
	return context.WithValue(ctx, persistenceQueueKey{}, persistenceQueue{invocationID: inv.InvocationID, queue: q})
}

// queueForPersistence allows views of the same invocation to finish an append
// after cleanup without granting that retained state to a child invocation.
func queueForPersistence(ctx context.Context, inv *agent.Invocation) *Queue {
	if retained, ok := ctx.Value(persistenceQueueKey{}).(persistenceQueue); ok && inv != nil && retained.invocationID == inv.InvocationID {
		return retained.queue
	}
	return queueFor(inv)
}

// WithPersistenceIdentity associates a plugin's replacement event with the
// original event for AppendOnce. It does not alter the public event ID or apply
// to appends of other events using the same context.
func WithPersistenceIdentity(ctx context.Context, eventID, originalID string) context.Context {
	return context.WithValue(ctx, persistenceIdentityKey{}, persistenceIdentity{eventID: eventID, originalID: originalID})
}

// AppendOnce coordinates normal persistence and deadline fallback writes by
// event ID. Concurrent attempts wait for the in-flight append; successful IDs
// stay recorded for the queue lifetime, while failed attempts can be retried.
// A panicking callback releases waiters with an error and is not retried; the
// original panic continues to propagate to the caller.
// Disabled queues, and unattached invocations without retained coordination,
// preserve the original append behavior.
func AppendOnce(ctx context.Context, inv *agent.Invocation, id string, appendEvent func() error) error {
	q := queueForPersistence(ctx, inv)
	if q == nil || id == "" {
		return appendEvent()
	}
	if identity, ok := ctx.Value(persistenceIdentityKey{}).(persistenceIdentity); ok && identity.eventID == id && identity.originalID != "" {
		id = identity.originalID
	}
	for {
		q.mu.Lock()
		if !q.enabled {
			q.mu.Unlock()
			return appendEvent()
		}
		if q.appends == nil {
			q.appends = make(map[string]*appendRecord)
		}
		record, exists := q.appends[id]
		if !exists {
			record = &appendRecord{done: make(chan struct{})}
			q.appends[id] = record
			q.mu.Unlock()
			return q.appendEvent(id, record, appendEvent)
		}
		q.mu.Unlock()
		select {
		case <-record.done:
			if record.err == nil || record.panicked {
				return record.err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// appendEvent settles every exit path without recovering the caller's panic.
// Only returned errors release the identity for a later retry.
func (q *Queue) appendEvent(id string, record *appendRecord, appendEvent func() error) (err error) {
	returned := false
	defer func() {
		q.mu.Lock()
		record.err = err
		if !returned {
			record.panicked = true
			record.err = fmt.Errorf("event persistence callback panicked: %s", id)
		} else if err != nil {
			delete(q.appends, id)
		}
		close(record.done)
		q.mu.Unlock()
	}()
	err = appendEvent()
	returned = true
	return err
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
