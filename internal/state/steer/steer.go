//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package steer provides invocation-scoped queued user-message storage.
// Runner uses it to accept steer messages for an active run, while llmflow
// drains and persists them only at safe loop boundaries.
package steer

import (
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// StateKeyQueuedUserMessages is the invocation state key used by Attach.
const StateKeyQueuedUserMessages = "__queued_user_messages__"

// stateKeyBorrowed marks an attachment as non-owning: the invocation may drain
// the queue but must not close it (see AttachBorrowed and Close).
const stateKeyBorrowed = "__queued_user_messages_borrowed__"

const (
	// ExtensionKeyQueuedUserMessage marks events emitted when queued user
	// messages are consumed at a safe boundary.
	ExtensionKeyQueuedUserMessage = "trpc_agent.steer.queued_user_message"

	// QueuedUserMessageStatusConsumed is the status for consumed queued
	// user-message events.
	QueuedUserMessageStatusConsumed = "consumed"
)

// QueuedUserMessageMetadata describes the queued user-message event state.
type QueuedUserMessageMetadata struct {
	Status string `json:"status"`
}

// Queue stores queued user messages in FIFO order.
type Queue struct {
	mu       sync.Mutex
	messages []model.Message
	closed   bool
}

// NewQueue creates an empty queue.
func NewQueue() *Queue {
	return &Queue{}
}

// Enqueue appends one message unless the queue has been closed.
func (q *Queue) Enqueue(message model.Message) bool {
	if q == nil {
		return false
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	q.messages = append(q.messages, message)
	return true
}

// Drain returns all queued messages in FIFO order.
func (q *Queue) Drain() []model.Message {
	if q == nil {
		return nil
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) == 0 {
		return nil
	}
	drained := append([]model.Message(nil), q.messages...)
	q.messages = nil
	return drained
}

// Discard removes all queued messages without closing the queue.
func (q *Queue) Discard() []model.Message {
	if q == nil {
		return nil
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) == 0 {
		return nil
	}
	discarded := append([]model.Message(nil), q.messages...)
	q.messages = nil
	return discarded
}

// Close rejects future enqueues.
func (q *Queue) Close() {
	if q == nil {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
}

// Attach binds a queue to the invocation. The invocation owns the queue: a
// subsequent Close (or Clear) closes it.
func Attach(inv *agent.Invocation, queue *Queue) {
	if inv == nil || queue == nil {
		return
	}
	inv.SetState(StateKeyQueuedUserMessages, queue)
	// Establish owning semantics unconditionally: clear any stale borrowed
	// marker so Close/Clear close the queue regardless of a prior
	// AttachBorrowed on this invocation.
	inv.DeleteState(stateKeyBorrowed)
}

// AttachBorrowed binds a queue to the invocation without ownership: the
// invocation may drain the queue, but Close (and Clear) leave it open so the
// true owner can keep accepting enqueues. The ralph loop uses this to share the
// run's live queue with each iteration's inner agent — the inner llmflow closes
// its invocation queue when it finishes, but that must not close the run-level
// queue the runner enqueues onto across iterations.
func AttachBorrowed(inv *agent.Invocation, queue *Queue) {
	if inv == nil || queue == nil {
		return
	}
	inv.SetState(StateKeyQueuedUserMessages, queue)
	inv.SetState(stateKeyBorrowed, true)
}

// isBorrowed reports whether the invocation holds a non-owning attachment.
func isBorrowed(inv *agent.Invocation) bool {
	borrowed, ok := agent.GetStateValue[bool](inv, stateKeyBorrowed)
	return ok && borrowed
}

// IsAttached reports whether a queue is attached to the invocation.
func IsAttached(inv *agent.Invocation) bool {
	queue, ok := agent.GetStateValue[*Queue](
		inv,
		StateKeyQueuedUserMessages,
	)
	return ok && queue != nil
}

// Drain removes and returns queued messages from the invocation.
func Drain(inv *agent.Invocation) []model.Message {
	queue, ok := agent.GetStateValue[*Queue](
		inv,
		StateKeyQueuedUserMessages,
	)
	if !ok || queue == nil {
		return nil
	}
	return queue.Drain()
}

// Close rejects future enqueues for the invocation queue. A borrowed
// attachment (see AttachBorrowed) is left open: only its owner may close it.
func Close(inv *agent.Invocation) {
	if inv == nil || isBorrowed(inv) {
		return
	}
	queue, ok := agent.GetStateValue[*Queue](
		inv,
		StateKeyQueuedUserMessages,
	)
	if ok && queue != nil {
		queue.Close()
	}
}

// Clear closes the queue (unless borrowed) and removes it from the invocation.
func Clear(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	Close(inv)
	inv.DeleteState(StateKeyQueuedUserMessages)
	inv.DeleteState(stateKeyBorrowed)
}
