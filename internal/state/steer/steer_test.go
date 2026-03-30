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
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

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

func TestNilSafety(t *testing.T) {
	var (
		invocation *agent.Invocation
		queue      *Queue
	)

	Attach(invocation, NewQueue())
	require.False(t, IsAttached(invocation))
	require.False(t, queue.Enqueue(model.NewUserMessage("x")))
	require.Nil(t, queue.Drain())
	queue.Close()
	Clear(invocation)
	require.Nil(t, Drain(invocation))
}
