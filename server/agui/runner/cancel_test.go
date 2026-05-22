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
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
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

type updateFailSessionService struct {
	session.Service
	err error
}

func (s *updateFailSessionService) UpdateSessionState(context.Context, session.Key, session.StateMap) error {
	return s.err
}

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

type finalizingTranslator struct {
	finalizationEvents []aguievents.Event
	finalizationErr    error
	finalizationCtxErr error
	finalizationTimed  bool
	waitForContextDone bool
}

func (f *finalizingTranslator) Translate(context.Context, *agentevent.Event) ([]aguievents.Event, error) {
	return nil, nil
}

func (f *finalizingTranslator) PostRunFinalizationEvents(ctx context.Context) ([]aguievents.Event, error) {
	if f.waitForContextDone {
		<-ctx.Done()
	}
	f.finalizationCtxErr = ctx.Err()
	_, f.finalizationTimed = ctx.Deadline()
	if f.waitForContextDone {
		return nil, ctx.Err()
	}
	return f.finalizationEvents, f.finalizationErr
}

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

func TestDistributedCancelIgnoresRunID(t *testing.T) {
	sessionService := inmemory.NewSessionService()
	ownerCtxCh := make(chan context.Context, 1)
	owner := New(
		&waitCancelRunner{ctxCh: ownerCtxCh},
		WithAppName("app"),
		WithSessionService(sessionService),
		WithDistributedCancelEnabled(true),
		WithDistributedCancelPollInterval(10*time.Millisecond),
	).(*runner)
	requester := New(
		&waitCancelRunner{ctxCh: make(chan context.Context, 1)},
		WithAppName("app"),
		WithSessionService(sessionService),
		WithDistributedCancelEnabled(true),
		WithDistributedCancelPollInterval(10*time.Millisecond),
	).(*runner)
	events, err := owner.Run(context.Background(), distributedCancelInput("thread", "run"))
	assert.NoError(t, err)
	waitForDistributedCancelRunStarted(t, events)
	runCtx := <-ownerCtxCh
	err = requester.Cancel(context.Background(), distributedCancelInput("thread", "different-run"))
	assert.NoError(t, err)
	assert.Eventually(t, func() bool {
		return runCtx.Err() != nil
	}, time.Second, 10*time.Millisecond)
	collectEvents(t, events)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	active, err := activeDistributedRun(context.Background(), sessionService, key)
	assert.NoError(t, err)
	assert.False(t, active)
	requested, err := readDistributedCancelMarker(context.Background(), sessionService, key)
	assert.NoError(t, err)
	assert.False(t, requested)
}

func TestDistributedCancelWithoutRunMarkerReturnsNotFound(t *testing.T) {
	sessionService := inmemory.NewSessionService()
	requester := newDistributedCancelTestRunner(sessionService, make(chan context.Context, 1))
	err := requester.Cancel(context.Background(), distributedCancelInput("thread", "run"))
	assert.ErrorIs(t, err, ErrRunNotFound)
}

func TestDistributedCancelRunWithoutSessionServiceIsFailOpen(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	r := New(
		&waitCancelRunner{ctxCh: ctxCh},
		WithAppName("app"),
		WithDistributedCancelEnabled(true),
	).(*runner)
	events, err := r.Run(context.Background(), distributedCancelInput("thread", "run"))
	assert.NoError(t, err)
	waitForDistributedCancelRunStarted(t, events)
	runCtx := <-ctxCh
	assert.NoError(t, r.Cancel(context.Background(), distributedCancelInput("thread", "run")))
	assert.Eventually(t, func() bool {
		return runCtx.Err() != nil
	}, time.Second, 10*time.Millisecond)
	collectEvents(t, events)
}

func TestDistributedCancelRunStartClearsStaleCancelMarker(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	sessionService := inmemory.NewSessionService()
	_, err := sessionService.CreateSession(ctx, key, nil)
	assert.NoError(t, err)
	err = sessionService.UpdateSessionState(ctx, key, session.StateMap{
		distributedCancelCancelMarkerKey: []byte("stale"),
	})
	assert.NoError(t, err)
	ctxCh := make(chan context.Context, 1)
	r := newDistributedCancelTestRunner(sessionService, ctxCh)
	events, err := r.Run(ctx, distributedCancelInput("thread", "run"))
	assert.NoError(t, err)
	waitForDistributedCancelRunStarted(t, events)
	runCtx := <-ctxCh
	requested, err := readDistributedCancelMarker(ctx, sessionService, key)
	assert.NoError(t, err)
	assert.False(t, requested)
	assert.Never(t, func() bool {
		return runCtx.Err() != nil
	}, 100*time.Millisecond, 10*time.Millisecond)
	assert.NoError(t, r.Cancel(ctx, distributedCancelInput("thread", "run")))
	assert.Eventually(t, func() bool {
		return runCtx.Err() != nil
	}, time.Second, 10*time.Millisecond)
	collectEvents(t, events)
}

func TestDistributedCancelRunMarkerFailureIsFailOpen(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	updateErr := errors.New("update failed")
	baseSessionService := inmemory.NewSessionService()
	_, err := baseSessionService.CreateSession(ctx, key, nil)
	assert.NoError(t, err)
	sessionService := &updateFailSessionService{
		Service: baseSessionService,
		err:     updateErr,
	}
	ctxCh := make(chan context.Context, 1)
	r := New(
		&waitCancelRunner{ctxCh: ctxCh},
		WithAppName("app"),
		WithSessionService(sessionService),
		WithDistributedCancelEnabled(true),
		WithDistributedCancelPollInterval(10*time.Millisecond),
	).(*runner)
	events, err := r.Run(ctx, distributedCancelInput("thread", "run"))
	assert.NoError(t, err)
	waitForDistributedCancelRunStarted(t, events)
	runCtx := <-ctxCh
	assert.NoError(t, r.Cancel(ctx, distributedCancelInput("thread", "run")))
	assert.Eventually(t, func() bool {
		return runCtx.Err() != nil
	}, time.Second, 10*time.Millisecond)
	collectEvents(t, events)
}

func TestDistributedCancelMarkerKeysAreSessionScoped(t *testing.T) {
	assert.Equal(t, session.StateTempPrefix+"agui:distributed_cancel:run", distributedCancelRunMarkerKey)
	assert.Equal(t, session.StateTempPrefix+"agui:distributed_cancel:cancel", distributedCancelCancelMarkerKey)
}

func TestDistributedCancelMarkerValuesAreJSON(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	sessionService := inmemory.NewSessionService()
	_, err := sessionService.CreateSession(ctx, key, nil)
	assert.NoError(t, err)
	assert.NoError(t, writeDistributedRunMarker(ctx, sessionService, key))
	assertDistributedCancelMarkerValue(t, sessionService, key, distributedCancelRunMarkerKey)
	assert.NoError(t, writeDistributedCancelMarker(ctx, sessionService, key))
	assertDistributedCancelMarkerValue(t, sessionService, key, distributedCancelCancelMarkerKey)
}

func assertDistributedCancelMarkerValue(
	t *testing.T,
	sessionService session.Service,
	key session.Key,
	stateKey string,
) {
	t.Helper()
	sess, err := sessionService.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sess)
	raw, ok := sess.GetState(stateKey)
	assert.True(t, ok)
	var marker distributedCancelMarker
	assert.NoError(t, json.Unmarshal(raw, &marker))
	_, err = time.Parse(time.RFC3339Nano, marker.UpdatedAt)
	assert.NoError(t, err)
}

func newDistributedCancelTestRunner(
	sessionService session.Service,
	ctxCh chan context.Context,
) *runner {
	return New(
		&waitCancelRunner{ctxCh: ctxCh},
		WithAppName("app"),
		WithSessionService(sessionService),
		WithDistributedCancelEnabled(true),
		WithDistributedCancelPollInterval(10*time.Millisecond),
	).(*runner)
}

func distributedCancelInput(threadID string, runID string) *adapter.RunAgentInput {
	return &adapter.RunAgentInput{
		ThreadID: threadID,
		RunID:    runID,
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
}

func waitForDistributedCancelRunStarted(t *testing.T, events <-chan aguievents.Event) {
	t.Helper()
	select {
	case evt := <-events:
		assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evt)
	case <-time.After(time.Second):
		assert.FailNow(t, "timeout waiting for RUN_STARTED")
	}
}

func TestCancelUsesResolvedAppName(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	underlying := &waitCancelRunner{ctxCh: ctxCh}
	r := New(
		underlying,
		WithAppName("static-app"),
		WithAppNameResolver(forwardedPropsAppNameResolver),
	).(*runner)
	input := &adapter.RunAgentInput{
		ThreadID:       "thread",
		RunID:          "run",
		ForwardedProps: map[string]any{"appName": "dynamic-app"},
		Messages:       []types.Message{{Role: types.RoleUser, Content: "hi"}},
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
	err = r.Cancel(context.Background(), &adapter.RunAgentInput{
		ThreadID:       "thread",
		RunID:          "other-run",
		ForwardedProps: map[string]any{"appName": "dynamic-app"},
	})
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

func TestCancelClosesReasoningStream(t *testing.T) {
	r := New(
		&reasoningWaitRunner{},
		WithReasoningContentEnabled(true),
	).(*runner)

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}

	events, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	waitForAGUIEventType(t, events, (*aguievents.ReasoningMessageContentEvent)(nil))

	err = r.Cancel(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	assert.NoError(t, err)

	var remaining []aguievents.Event
	for evt := range events {
		remaining = append(remaining, evt)
	}

	assert.Len(t, remaining, 3)
	assert.IsType(t, (*aguievents.ReasoningMessageEndEvent)(nil), remaining[0])
	assert.IsType(t, (*aguievents.ReasoningEndEvent)(nil), remaining[1])
	assert.IsType(t, (*aguievents.RunFinishedEvent)(nil), remaining[2])
}

func TestCancelEmitsRunErrorWhenPostRunFinalizationFails(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	underlying := &waitCancelRunner{ctxCh: ctxCh}
	finalizer := &finalizingTranslator{
		finalizationEvents: []aguievents.Event{aguievents.NewTextMessageEndEvent("msg-1")},
		finalizationErr:    errors.New("boom"),
	}
	r := New(
		underlying,
		WithPostRunFinalizationTimeout(2*time.Second),
		WithTranslatorFactory(func(context.Context, *adapter.RunAgentInput, ...translator.Option) (translator.Translator, error) {
			return finalizer, nil
		}),
	).(*runner)

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

	err = r.Cancel(context.Background(), input)
	assert.NoError(t, err)

	assert.Eventually(t, func() bool {
		select {
		case <-runCtx.Done():
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)

	var remaining []aguievents.Event
	for evt := range events {
		remaining = append(remaining, evt)
	}

	assert.Len(t, remaining, 2)
	assert.IsType(t, (*aguievents.TextMessageEndEvent)(nil), remaining[0])
	runErr, ok := remaining[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Equal(t, "post-run finalization: boom", runErr.Message)
	assert.NoError(t, finalizer.finalizationCtxErr)
	assert.True(t, finalizer.finalizationTimed)
}

func TestCancelEmitsTerminalRunErrorWhenPostRunFinalizationTimesOut(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	underlying := &waitCancelRunner{ctxCh: ctxCh}
	finalizer := &finalizingTranslator{waitForContextDone: true}
	r := New(
		underlying,
		WithPostRunFinalizationTimeout(20*time.Millisecond),
		WithTranslatorFactory(func(context.Context, *adapter.RunAgentInput, ...translator.Option) (translator.Translator, error) {
			return finalizer, nil
		}),
	).(*runner)

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

	err = r.Cancel(context.Background(), input)
	assert.NoError(t, err)

	assert.Eventually(t, func() bool {
		select {
		case <-runCtx.Done():
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)

	var remaining []aguievents.Event
	for evt := range events {
		remaining = append(remaining, evt)
	}

	assert.Len(t, remaining, 1)
	runErr, ok := remaining[0].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Equal(t, "post-run finalization: context deadline exceeded", runErr.Message)
	assert.ErrorIs(t, finalizer.finalizationCtxErr, context.DeadlineExceeded)
	assert.True(t, finalizer.finalizationTimed)
}

func TestCancelEmitsSingleTerminalEventAfterTranslateCallbackError(t *testing.T) {
	callbacks := translator.NewCallbacks().
		RegisterAfterTranslate(func(ctx context.Context, evt aguievents.Event) (aguievents.Event, error) {
			if _, ok := evt.(*aguievents.ReasoningMessageEndEvent); ok {
				return nil, errors.New("after translate fail")
			}
			return evt, nil
		})
	r := New(
		&reasoningWaitRunner{},
		WithReasoningContentEnabled(true),
		WithTranslateCallbacks(callbacks),
	).(*runner)
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	events, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	waitForAGUIEventType(t, events, (*aguievents.ReasoningMessageContentEvent)(nil))
	err = r.Cancel(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	assert.NoError(t, err)
	remaining := collectEvents(t, events)
	terminalCount := 0
	for _, evt := range remaining {
		terminal, _ := terminalRunSignal(evt)
		if terminal {
			terminalCount++
		}
	}
	assert.Equal(t, 1, terminalCount)
	assert.True(t, hasRunErrorEvent(remaining))
	assert.False(t, hasRunFinishedEvent(remaining))
}

func TestCancelEmitsSingleTerminalEventWhenFinalizerReturnsMultipleTerminalEvents(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	underlying := &waitCancelRunner{ctxCh: ctxCh}
	finalizer := &finalizingTranslator{
		finalizationEvents: []aguievents.Event{
			aguievents.NewRunErrorEvent("boom", aguievents.WithRunID("run")),
			aguievents.NewRunFinishedEvent("thread", "run"),
		},
	}
	r := New(
		underlying,
		WithTranslatorFactory(func(context.Context, *adapter.RunAgentInput, ...translator.Option) (translator.Translator, error) {
			return finalizer, nil
		}),
	).(*runner)
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
	err = r.Cancel(context.Background(), input)
	assert.NoError(t, err)
	remaining := collectEvents(t, events)
	terminalCount := 0
	for _, evt := range remaining {
		terminal, _ := terminalRunSignal(evt)
		if terminal {
			terminalCount++
		}
	}
	assert.Equal(t, 1, terminalCount)
	assert.True(t, hasRunErrorEvent(remaining))
	assert.False(t, hasRunFinishedEvent(remaining))
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

func TestCancelAppNameResolverError(t *testing.T) {
	r := &runner{
		runner: &fakeRunner{},
		appNameResolver: func(context.Context, *adapter.RunAgentInput) (string, error) {
			return "", errors.New("boom")
		},
		userIDResolver: NewOptions().UserIDResolver,
	}
	err := r.Cancel(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	assert.ErrorContains(t, err, "resolve app name")
}
