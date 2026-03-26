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

// Close rejects future enqueues.
func (q *Queue) Close() {
	if q == nil {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
}

// Attach binds a queue to the invocation.
func Attach(inv *agent.Invocation, queue *Queue) {
	if inv == nil || queue == nil {
		return
	}
	inv.SetState(StateKeyQueuedUserMessages, queue)
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

// Close rejects future enqueues for the invocation queue.
func Close(inv *agent.Invocation) {
	if inv == nil {
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

// Clear closes the queue and removes it from the invocation.
func Clear(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	Close(inv)
	inv.DeleteState(StateKeyQueuedUserMessages)
}
