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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestQueuedUserMessageWireValues(t *testing.T) {
	require.Equal(
		t,
		"trpc_agent.steer.queued_user_message",
		ExtensionKeyQueuedUserMessage,
	)
	require.Equal(t, "consumed", QueuedUserMessageStatusConsumed)

	payload, err := json.Marshal(QueuedUserMessageMetadata{
		Status: QueuedUserMessageStatusConsumed,
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"status":"consumed"}`, string(payload))
}

func TestQueue_FIFOAndClose(t *testing.T) {
	queue := NewQueue()

	require.True(t, queue.Enqueue(model.NewUserMessage("one")))
	require.True(t, queue.Enqueue(model.NewUserMessage("two")))

	drained := queue.Drain()
	require.Len(t, drained, 2)
	require.Equal(t, "one", drained[0].Content)
	require.Equal(t, "two", drained[1].Content)
	require.Nil(t, queue.Drain())

	queue.Close()
	require.False(t, queue.Enqueue(model.NewUserMessage("three")))
	require.Nil(t, queue.Drain())
}

func TestAttachDrainAndClear(t *testing.T) {
	invocation := agent.NewInvocation()
	queue := NewQueue()

	require.False(t, IsAttached(invocation))

	Attach(invocation, queue)
	require.True(t, IsAttached(invocation))

	require.True(t, queue.Enqueue(model.NewUserMessage("hello")))

	drained := Drain(invocation)
	require.Len(t, drained, 1)
	require.Equal(t, "hello", drained[0].Content)

	Clear(invocation)
	require.False(t, IsAttached(invocation))
	require.False(t, queue.Enqueue(model.NewUserMessage("later")))
	require.Nil(t, Drain(invocation))
}

func TestClose_RejectsFutureEnqueueAndPreservesQueuedMessages(t *testing.T) {
	invocation := agent.NewInvocation()
	queue := NewQueue()

	Attach(invocation, queue)
	require.True(t, queue.Enqueue(model.NewUserMessage("hello")))

	Close(invocation)

	require.True(t, IsAttached(invocation))
	require.False(t, queue.Enqueue(model.NewUserMessage("later")))

	drained := Drain(invocation)
	require.Len(t, drained, 1)
	require.Equal(t, "hello", drained[0].Content)
}

func TestQueue_DiscardClearsQueuedMessagesWithoutClosing(t *testing.T) {
	queue := NewQueue()

	require.Nil(t, queue.Discard())
	require.True(t, queue.Enqueue(model.NewUserMessage("one")))
	require.True(t, queue.Enqueue(model.NewUserMessage("two")))

	discarded := queue.Discard()
	require.Len(t, discarded, 2)
	require.Equal(t, "one", discarded[0].Content)
	require.Equal(t, "two", discarded[1].Content)
	require.Nil(t, queue.Drain())

	require.True(t, queue.Enqueue(model.NewUserMessage("three")))
	drained := queue.Drain()
	require.Len(t, drained, 1)
	require.Equal(t, "three", drained[0].Content)
}

// TestAttach_NotInheritedByPlainClone pins the member-isolation invariant: the
// steer queue is deliberately NOT in the invocation clone allowlist, so a plain
// Clone (the path delegated sub-agents take — agent_tool clones the parent
// invocation) does NOT inherit it. Otherwise a member sub-agent would drain a
// steer the user meant for the lead. The ralph loop re-attaches the queue to its
// inner (lead) invocation explicitly; see runner.newInnerInvocation.
func TestAttach_NotInheritedByPlainClone(t *testing.T) {
	root := agent.NewInvocation()
	queue := NewQueue()
	Attach(root, queue)
	require.True(t, IsAttached(root))

	clone := root.Clone()
	require.False(t, IsAttached(clone),
		"a plain clone (delegated sub-agent) must NOT inherit the steer queue")

	// A steer enqueued on the root is invisible to the clone — it cannot drain it.
	require.True(t, queue.Enqueue(model.NewUserMessage("steer")))
	require.Nil(t, Drain(clone), "sub-agent clone must not drain the lead's steer")
}

func TestNilSafety(t *testing.T) {
	var (
		invocation *agent.Invocation
		queue      *Queue
	)

	Attach(invocation, NewQueue())
	require.False(t, IsAttached(invocation))
	require.False(t, queue.Enqueue(model.NewUserMessage("x")))
	require.Nil(t, queue.Drain())
	require.Nil(t, queue.Discard())
	queue.Close()
	Clear(invocation)
	require.Nil(t, Drain(invocation))
}
