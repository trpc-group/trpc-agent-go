//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package flush

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestInvokeFindsParentFlusher(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parent := agent.NewInvocation()
	ch := make(chan *FlushRequest, 1)
	Attach(ctx, parent, ch)
	child := parent.Clone()

	done := make(chan error, 1)
	go func() {
		select {
		case req := <-ch:
			if req == nil || req.ACK == nil {
				done <- context.Canceled
				return
			}
			close(req.ACK)
			done <- nil
		case <-ctx.Done():
			done <- ctx.Err()
		}
	}()

	require.NoError(t, Invoke(ctx, child))
	require.NoError(t, <-done)
}

func TestInvokeNoFlusher(t *testing.T) {
	require.NoError(t, Invoke(context.Background(), agent.NewInvocation()))
}

func TestAttachReusesHolderAndUpdatesChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	inv := agent.NewInvocation()
	ch1 := make(chan *FlushRequest, 1)
	Attach(ctx, inv, ch1)
	holder1, ok := agent.GetStateValue[*flusherHolder](inv, StateKeyFlushSession)
	require.True(t, ok)
	require.NotNil(t, holder1)

	ch2 := make(chan *FlushRequest, 1)
	Attach(ctx, inv, ch2)
	holder2, ok := agent.GetStateValue[*flusherHolder](inv, StateKeyFlushSession)
	require.True(t, ok)
	require.Equal(t, holder1, holder2)

	done := make(chan error, 1)
	go func() {
		done <- Invoke(ctx, inv)
	}()

	var req *FlushRequest
	select {
	case req = <-ch2:
	case <-ctx.Done():
		t.Fatalf("invoke did not send flush request: %v", ctx.Err())
	}
	require.NotNil(t, req)
	require.NotNil(t, req.ACK)
	close(req.ACK)
	require.NoError(t, <-done)

	select {
	case unexpected := <-ch1:
		t.Fatalf("received unexpected request on old channel: %+v", unexpected)
	default:
	}
}

func TestInvokeContextCancelBeforeAck(t *testing.T) {
	inv := agent.NewInvocation()
	ch := make(chan *FlushRequest, 1)
	Attach(context.Background(), inv, ch)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Invoke(ctx, inv)
	}()

	req := <-ch
	require.NotNil(t, req)
	cancel()

	require.ErrorIs(t, <-errCh, context.Canceled)
	close(req.ACK)
}

func TestClearRemovesFlusher(t *testing.T) {
	inv := agent.NewInvocation()
	ch := make(chan *FlushRequest, 1)
	Attach(context.Background(), inv, ch)

	Clear(inv)
	_, ok := agent.GetStateValue[*flusherHolder](inv, StateKeyFlushSession)
	require.False(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.NoError(t, Invoke(ctx, inv))

	select {
	case req := <-ch:
		t.Fatalf("unexpected flush request after clear: %+v", req)
	default:
	}

	Clear(nil)
}

func TestInvokeNilInvocation(t *testing.T) {
	require.NoError(t, Invoke(context.Background(), nil))
}

func TestInvokeNilFlusherInHolder(t *testing.T) {
	inv := agent.NewInvocation()
	// Inject holder without flusher to cover fn == nil early return.
	inv.SetState(StateKeyFlushSession, &flusherHolder{})

	require.NoError(t, Invoke(context.Background(), inv))
}

func TestInvokeConcurrentClear(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	inv := agent.NewInvocation()
	ch := make(chan *FlushRequest, 1)
	Attach(ctx, inv, ch)

	errCh := make(chan error, 1)
	go func() {
		errCh <- Invoke(ctx, inv)
	}()

	req := <-ch
	require.NotNil(t, req)
	Clear(inv)
	close(req.ACK)

	require.NoError(t, <-errCh)
	// After Clear, Invoke should no-op.
	require.NoError(t, Invoke(ctx, inv))
}

func TestInvokeContextCancelBeforeEnqueue(t *testing.T) {
	inv := agent.NewInvocation()
	ch := make(chan *FlushRequest)
	Attach(context.Background(), inv, ch)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Invoke(ctx, inv)
	require.ErrorIs(t, err, context.Canceled)

	select {
	case req := <-ch:
		t.Fatalf("unexpected flush request: %+v", req)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestIsAttached(t *testing.T) {
	inv := agent.NewInvocation()
	require.False(t, IsAttached(inv))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch := make(chan *FlushRequest, 1)
	Attach(ctx, inv, ch)
	require.True(t, IsAttached(inv))

	Clear(inv)
	require.False(t, IsAttached(inv))
}
