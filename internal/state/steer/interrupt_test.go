//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package steer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func interruptQueue() (*agent.Invocation, *Queue) {
	inv := agent.NewInvocation()
	q := NewQueue()
	Attach(inv, q)
	EnableInterrupt(inv)
	return inv, q
}
func TestInterruptAdmissionRaces(t *testing.T) {
	for i := 0; i < 300; i++ {
		inv, q := interruptQueue()
		ctx, cancel := context.WithCancel(context.Background())
		c, ready := BeginModel(inv, cancel)
		require.True(t, ready)
		var accepted bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); q.Enqueue(model.NewUserMessage("Q2")) }()
		go func() { defer wg.Done(); accepted = c.Accept() }()
		wg.Wait()
		require.NotEqual(t, accepted, c.Interrupted())
		if accepted {
			require.NoError(t, ctx.Err())
		} else {
			require.ErrorIs(t, ctx.Err(), context.Canceled)
		}
		c.End()
		cancel()
	}
}
func TestInterruptFinishRace(t *testing.T) {
	for i := 0; i < 300; i++ {
		inv, q := interruptQueue()
		var enqueued, finished bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); enqueued = q.Enqueue(model.NewUserMessage("Q2")) }()
		go func() { defer wg.Done(); finished = TryFinish(inv) }()
		wg.Wait()
		require.NotEqual(t, enqueued, finished)
		if enqueued {
			require.Len(t, q.Drain(), 1)
			require.True(t, TryFinish(inv))
		}
		require.False(t, q.Enqueue(model.NewUserMessage("late")))
	}
}
func TestInterruptDiscardAndCycleIsolation(t *testing.T) {
	inv, q := interruptQueue()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old, ready := BeginModel(inv, cancel)
	require.True(t, ready)
	require.True(t, q.Enqueue(model.NewUserMessage("discard")))
	require.Len(t, q.Discard(), 1)
	require.True(t, old.Interrupted())
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	old.End()
	nextCtx, nextCancel := context.WithCancel(context.Background())
	defer nextCancel()
	next, ready := BeginModel(inv, nextCancel)
	require.True(t, ready)
	old.End()
	require.NoError(t, nextCtx.Err())
	require.False(t, next.Interrupted())
	require.True(t, q.Enqueue(model.NewUserMessage("new")))
	require.True(t, next.Interrupted())
}
func TestInterruptBorrowedFinish(t *testing.T) {
	owner, q := interruptQueue()
	borrower := agent.NewInvocation()
	AttachBorrowed(borrower, q)
	require.True(t, TryFinish(borrower))
	Close(borrower)
	require.True(t, q.Enqueue(model.NewUserMessage("next iteration")))
	require.Len(t, Drain(owner), 1)
	require.True(t, TryFinish(owner))
	require.False(t, q.Enqueue(model.NewUserMessage("late")))
}
func TestInterruptReceipts(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "timeout", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			inv, q := interruptQueue()
			out := make(chan *event.Event, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			evt := event.New(inv.InvocationID, "agent")
			done := make(chan error, 1)
			go func() { done <- EmitAndWait(ctx, inv, out, evt, 20*time.Millisecond) }()
			<-out
			cause := errors.New("storage unavailable")
			switch outcome {
			case "success":
				Acknowledge(inv, evt.ID, nil)
			case "failure":
				Acknowledge(inv, evt.ID, cause)
			case "cancel":
				cancel()
			}
			err := <-done
			switch outcome {
			case "success":
				require.NoError(t, err)
			case "failure":
				require.ErrorIs(t, err, cause)
			case "cancel":
				require.ErrorIs(t, err, context.Canceled)
			case "timeout":
				require.ErrorContains(t, err, "timed out")
			}
			q.mu.Lock()
			require.Empty(t, q.receipts)
			q.mu.Unlock()
			if err != nil {
				require.ErrorIs(t, Err(inv), err)
			}
			Acknowledge(inv, evt.ID, nil) // Late receipts must not block or leak.
		})
	}
}

func TestInterruptQueueInvocationIsolation(t *testing.T) {
	first, firstQueue := interruptQueue()
	second, _ := interruptQueue()
	firstCtx, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	secondCtx, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()
	a, _ := BeginModel(first, firstCancel)
	b, _ := BeginModel(second, secondCancel)
	defer a.End()
	defer b.End()
	require.False(t, Enabled(first.Clone()))
	require.True(t, Enabled(first.View()))
	firstQueue.Enqueue(model.NewUserMessage("only first"))
	require.ErrorIs(t, firstCtx.Err(), context.Canceled)
	require.NoError(t, secondCtx.Err())
	require.True(t, a.Interrupted())
	require.False(t, b.Interrupted())
}

func TestInterruptAppendOnceRetryAndCancellation(t *testing.T) {
	inv, _ := interruptQueue()
	ctx, cancel := context.WithCancel(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	failure := errors.New("append failed")
	go func() {
		done <- AppendOnce(context.Background(), inv, "event", func() error {
			close(entered)
			<-release
			return failure
		})
	}()
	<-entered
	cancel()
	require.ErrorIs(t, AppendOnce(ctx, inv, "event", func() error {
		t.Error("cancelled waiter must not append")
		return nil
	}), context.Canceled)
	close(release)
	require.ErrorIs(t, <-done, failure)
	calls := 0
	for i := 0; i < 2; i++ {
		require.NoError(t, AppendOnce(context.Background(), inv, "event", func() error { calls++; return nil }))
	}
	require.Equal(t, 1, calls, "failed persistence is retryable; success is not repeated")
}

func TestInterruptFailureCancelsModelWithoutSteering(t *testing.T) {
	inv, _ := interruptQueue()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("plugin failed")
	cycle, ready := BeginModel(inv, func() {
		// Cancellation must run outside the queue mutex.
		require.ErrorIs(t, Err(inv), failure)
		cancel()
	})
	require.True(t, ready)
	defer cycle.End()
	Fail(inv, failure)
	Fail(inv, errors.New("later failure"))
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.ErrorIs(t, Err(inv), failure)
	require.False(t, cycle.Interrupted())
	require.False(t, cycle.Accept())
}
