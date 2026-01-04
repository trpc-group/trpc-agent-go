//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
)

type waitCancelRunner struct {
	ctxCh chan context.Context
}

func (w *waitCancelRunner) Run(ctx context.Context, userID, sessionID string, _ model.Message,
	_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
	w.ctxCh <- ctx
	ch := make(chan *agentevent.Event)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func (w *waitCancelRunner) Close() error { return nil }

type blockingRunRunner struct {
	entered chan struct{}
	unblock chan struct{}
	calls   int32
}

func (b *blockingRunRunner) Run(ctx context.Context, userID, sessionID string, _ model.Message,
	_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
	atomic.AddInt32(&b.calls, 1)
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.unblock
	ch := make(chan *agentevent.Event)
	close(ch)
	return ch, nil
}

func (b *blockingRunRunner) Close() error { return nil }

func TestCancelCancelsRunningRun(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	underlying := &waitCancelRunner{ctxCh: ctxCh}
	r := New(underlying).(*runner)

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}

	events, err := r.Run(context.Background(), input)
	assert.NoError(t, err)

	select {
	case evt := <-events:
		assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evt)
	case <-time.After(3 * time.Second):
		assert.FailNow(t, "timeout waiting for RUN_STARTED")
	}

	runCtx := <-ctxCh
	assert.NotNil(t, runCtx)

	err = r.Cancel(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	assert.NoError(t, err)

	assert.Eventually(t, func() bool {
		select {
		case <-runCtx.Done():
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)

	collectEvents(t, events)

	err = r.Cancel(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	assert.ErrorIs(t, err, ErrRunNotFound)
}

func TestCancelIgnoresRunID(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	underlying := &waitCancelRunner{ctxCh: ctxCh}
	r := New(underlying).(*runner)

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}

	events, err := r.Run(context.Background(), input)
	assert.NoError(t, err)

	select {
	case evt := <-events:
		assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evt)
	case <-time.After(3 * time.Second):
		assert.FailNow(t, "timeout waiting for RUN_STARTED")
	}

	runCtx := <-ctxCh
	assert.NotNil(t, runCtx)

	err = r.Cancel(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "wrong"})
	assert.NoError(t, err)

	assert.Eventually(t, func() bool {
		select {
		case <-runCtx.Done():
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)

	collectEvents(t, events)
}

func TestCancelDoesNotReleaseSessionUntilRunExits(t *testing.T) {
	underlying := &blockingRunRunner{
		entered: make(chan struct{}, 1),
		unblock: make(chan struct{}),
	}
	r := New(underlying).(*runner)

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}

	events1, err := r.Run(context.Background(), input)
	assert.NoError(t, err)

	select {
	case evt := <-events1:
		assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evt)
	case <-time.After(3 * time.Second):
		assert.FailNow(t, "timeout waiting for RUN_STARTED")
	}

	select {
	case <-underlying.entered:
	case <-time.After(3 * time.Second):
		assert.FailNow(t, "timeout waiting for runner Run")
	}

	err = r.Cancel(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	assert.NoError(t, err)

	input2 := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run-2",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi again"}},
	}
	events2, err := r.Run(context.Background(), input2)
	assert.Nil(t, events2)
	assert.ErrorIs(t, err, ErrRunAlreadyExists)

	close(underlying.unblock)
	collectEvents(t, events1)

	assert.Equal(t, int32(1), atomic.LoadInt32(&underlying.calls))
}

func TestCancelValidatesRunner(t *testing.T) {
	r := &runner{}

	err := r.Cancel(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})

	assert.ErrorContains(t, err, "runner is nil")
}

func TestCancelValidatesInput(t *testing.T) {
	r := &runner{runner: &fakeRunner{}}

	err := r.Cancel(context.Background(), nil)

	assert.ErrorContains(t, err, "run input cannot be nil")
}

func TestCancelRunAgentInputHookError(t *testing.T) {
	r := &runner{
		runner: &fakeRunner{},
		runAgentInputHook: func(context.Context, *adapter.RunAgentInput) (*adapter.RunAgentInput, error) {
			return nil, errors.New("hook fail")
		},
	}

	err := r.Cancel(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})

	assert.ErrorContains(t, err, "run input hook")
}

func TestCancelUserIDResolverError(t *testing.T) {
	r := &runner{
		runner: &fakeRunner{},
		userIDResolver: func(context.Context, *adapter.RunAgentInput) (string, error) {
			return "", errors.New("boom")
		},
	}

	err := r.Cancel(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolve user ID")
}
