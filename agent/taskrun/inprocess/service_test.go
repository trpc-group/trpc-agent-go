//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inprocess

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	firstPersistAttempt        = 1
	runningStatePersistAttempt = 2
	testPersistBoom            = "persist boom"
	shortRunTimeout            = 10 * time.Millisecond
	noWaitResultDelay          = 20 * time.Millisecond
	testProfileInstruction     = "profile instruction"
	testProfileRuntimeStateKey = "profile_id"
	testRunContextValue        = "ctx-a"
	testFinalizedKey           = "finalized"
	testFinalizedValue         = "yes"
	testFinalizerPanic         = "finalizer panic"
	testCompletedOutput        = "completed output"
	testChildFailed            = "child failed"
	testWorkerCanceledRequest  = "worker canceled request"
	testCancelToolLeaseFailure = "failed to cancel tool lease"
)

type testRunContextKey struct{}

type captureRunner struct {
	mu        sync.Mutex
	ctx       context.Context
	userID    string
	sessionID string
	message   model.Message
	runOpts   agent.RunOptions
	reply     string
	events    []*event.Event
	runErr    error
}

func (r *captureRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.mu.Lock()
	r.ctx = ctx
	r.userID = userID
	r.sessionID = sessionID
	r.message = message
	var runOpts agent.RunOptions
	for _, opt := range opts {
		opt(&runOpts)
	}
	r.runOpts = runOpts
	reply := r.reply
	events := append([]*event.Event(nil), r.events...)
	runErr := r.runErr
	r.mu.Unlock()

	if runErr != nil {
		return nil, runErr
	}

	if len(events) == 0 {
		events = []*event.Event{{
			Response: &model.Response{
				Object: model.ObjectTypeChatCompletion,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage(reply),
				}},
			},
		}}
	}

	ch := make(chan *event.Event, len(events))
	for _, evt := range events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (r *captureRunner) Close() error {
	return nil
}

type blockingRunner struct {
	started chan struct{}
	once    sync.Once
}

func (r *blockingRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.once.Do(func() {
		close(r.started)
	})

	ch := make(chan *event.Event)
	go func() {
		defer close(ch)
		<-ctx.Done()
	}()
	return ch, nil
}

func (r *blockingRunner) Close() error {
	return nil
}

type controlledCancelRunner struct {
	started     chan struct{}
	canceling   chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
	once        sync.Once
}

func (r *controlledCancelRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.once.Do(func() {
		close(r.started)
	})

	ch := make(chan *event.Event)
	go func() {
		defer close(ch)
		<-ctx.Done()
		close(r.canceling)
		<-r.release
	}()
	return ch, nil
}

func (r *controlledCancelRunner) Close() error {
	r.releaseOnce.Do(func() {
		close(r.release)
	})
	return nil
}

type exitGateRunner struct {
	started    chan struct{}
	unblock    chan struct{}
	afterReply chan struct{}
	once       sync.Once
}

func (r *exitGateRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.once.Do(func() {
		close(r.started)
	})
	ch := make(chan *event.Event)
	go func() {
		defer close(ch)
		<-r.unblock
		ch <- &event.Event{
			Response: &model.Response{
				Object: model.ObjectTypeChatCompletion,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage("done"),
				}},
			},
		}
		close(r.afterReply)
	}()
	return ch, nil
}

func (r *exitGateRunner) Close() error {
	return nil
}

type captureObserver struct {
	mu   sync.Mutex
	runs []Run
}

func (o *captureObserver) OnRunUpdate(ctx context.Context, run Run) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.runs = append(o.runs, run)
}

func (o *captureObserver) statuses() []Status {
	o.mu.Lock()
	defer o.mu.Unlock()

	statuses := make([]Status, 0, len(o.runs))
	for _, run := range o.runs {
		statuses = append(statuses, run.Status)
	}
	return statuses
}

type failingLoadStore struct{}

func (s *failingLoadStore) Load(ctx context.Context) ([]Run, error) {
	return nil, errors.New("load boom")
}

func (s *failingLoadStore) Save(ctx context.Context, runs []Run) error {
	return nil
}

type failOnSaveStore struct {
	mu     sync.Mutex
	failAt int
	saves  int
	runs   []Run
}

func (s *failOnSaveStore) Load(ctx context.Context) ([]Run, error) {
	return cloneRuns(s.runs), nil
}

func (s *failOnSaveStore) Save(ctx context.Context, runs []Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.saves++
	if s.saves == s.failAt {
		return errors.New(testPersistBoom)
	}
	s.runs = cloneRuns(runs)
	return nil
}

type blockingFailStore struct {
	mu          sync.Mutex
	enteredOnce sync.Once
	releaseOnce sync.Once
	failAt      int
	saves       int
	runs        []Run
	entered     chan struct{}
	release     chan struct{}
}

func newBlockingFailStore(failAt int) *blockingFailStore {
	return &blockingFailStore{
		failAt:  failAt,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *blockingFailStore) Load(ctx context.Context) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return cloneRuns(s.runs), nil
}

func (s *blockingFailStore) Save(ctx context.Context, runs []Run) error {
	s.mu.Lock()
	s.saves++
	shouldFail := s.saves == s.failAt
	if !shouldFail {
		s.runs = cloneRuns(runs)
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	s.enteredOnce.Do(func() {
		close(s.entered)
	})
	select {
	case <-s.release:
		return errors.New(testPersistBoom)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *blockingFailStore) unblock() {
	s.releaseOnce.Do(func() {
		close(s.release)
	})
}

type waitResult struct {
	run *Run
	err error
}

func requireRegisteredWaiter(t *testing.T, svc *Service, runID string) {
	t.Helper()

	require.Eventually(t, func() bool {
		svc.mu.Lock()
		defer svc.mu.Unlock()

		return len(svc.waiters[runID]) > 0
	}, time.Second, 10*time.Millisecond)
}

func requireRunnerStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("task run did not start in time")
	}
}

func requireWaitResult(
	t *testing.T,
	ch <-chan waitResult,
) waitResult {
	t.Helper()

	select {
	case result := <-ch:
		return result
	case <-time.After(time.Second):
		t.Fatal("wait did not return after terminal transition")
	}
	return waitResult{}
}

func requireNoWaitResult(
	t *testing.T,
	ch <-chan waitResult,
) {
	t.Helper()

	select {
	case result := <-ch:
		t.Fatalf("wait returned before terminal transition: %+v", result)
	case <-time.After(noWaitResultDelay):
	}
}

func TestServiceSpawnCompletesRun(t *testing.T) {
	t.Parallel()

	observer := &captureObserver{}
	runner := &captureRunner{reply: "finished delegated work"}
	svc, err := NewService(runner, WithObserver(observer))
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		AppName:         "fallback-app",
		AgentName:       "worker",
		Task:            "review the patch",
		Timeout:         time.Second,
		RuntimeState: map[string]any{
			"trace_id": "trace-1",
		},
		RunOptions: []agent.RunOption{
			agent.WithAppName("child-app"),
			agent.WithInstruction(testProfileInstruction),
			agent.MergeRuntimeState(map[string]any{
				testProfileRuntimeStateKey: "profile-a",
			}),
		},
		RunContext: func(ctx context.Context) context.Context {
			return context.WithValue(
				ctx,
				testRunContextKey{},
				testRunContextValue,
			)
		},
		InjectedContextMessages: []model.Message{
			model.NewSystemMessage("stay focused"),
		},
		Metadata: map[string]string{
			"kind": "review",
		},
	})
	require.NoError(t, err)
	require.Equal(t, StatusQueued, run.Status)
	require.Equal(t, "child-app", run.AppName)

	final, err := svc.Wait(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, final.Status)
	require.Equal(t, "child-app", final.AppName)
	require.Equal(t, "finished delegated work", final.Result)
	require.Equal(t, "finished delegated work", final.Summary)
	require.NotEmpty(t, final.ChildSessionID)
	require.NotEmpty(t, final.RequestID)

	runs, err := svc.List(context.Background(), ListFilter{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
	})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, run.ID, runs[0].ID)

	runner.mu.Lock()
	require.Equal(t, "user-a", runner.userID)
	require.Equal(t, "review the patch", runner.message.Content)
	require.True(t, strings.HasPrefix(runner.sessionID, childSessionPrefix))
	require.Equal(t, "worker", runner.runOpts.AgentByName)
	require.Equal(t, true, runner.runOpts.RuntimeState[RuntimeStateKeyRun])
	require.Equal(t, run.ID, runner.runOpts.RuntimeState[RuntimeStateKeyRunID])
	require.Equal(
		t,
		"parent-a",
		runner.runOpts.RuntimeState[RuntimeStateKeyParentSessionID],
	)
	require.Equal(t, testProfileInstruction, runner.runOpts.Instruction)
	require.Equal(t, "child-app", runner.runOpts.AppName)
	require.Equal(
		t,
		"profile-a",
		runner.runOpts.RuntimeState[testProfileRuntimeStateKey],
	)
	require.Equal(
		t,
		testRunContextValue,
		runner.ctx.Value(testRunContextKey{}),
	)
	require.Equal(t, "trace-1", runner.runOpts.RuntimeState["trace_id"])
	require.Len(t, runner.runOpts.InjectedContextMessages, 1)
	require.Equal(
		t,
		"stay focused",
		runner.runOpts.InjectedContextMessages[0].Content,
	)
	runner.mu.Unlock()

	require.Contains(t, observer.statuses(), StatusCompleted)
}

func TestServiceRecordsRunProgress(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC)
	toolCallID := "call-1"
	runner := &captureRunner{events: []*event.Event{
		{
			Timestamp: startedAt.Add(time.Second),
			Response: &model.Response{
				Object: model.ObjectTypeChatCompletion,
				Usage: &model.Usage{
					PromptTokens:     2,
					CompletionTokens: 1,
					TotalTokens:      3,
				},
				Choices: []model.Choice{{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID: toolCallID,
							Function: model.FunctionDefinitionParam{
								Name: "lookup",
							},
						}},
					},
				}},
			},
		},
		{
			Timestamp: startedAt.Add(2 * time.Second),
			Response: &model.Response{
				Object: model.ObjectTypeToolResponse,
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleTool,
						ToolID:  toolCallID,
						Content: "tool output",
					},
				}},
			},
		},
		{
			Timestamp: startedAt.Add(3 * time.Second),
			Response: &model.Response{
				Object: model.ObjectTypeChatCompletion,
				Usage: &model.Usage{
					PromptTokens:     4,
					CompletionTokens: 3,
					TotalTokens:      7,
				},
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage("done"),
				}},
			},
		},
	}}
	svc, err := NewService(runner)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "collect progress",
	})
	require.NoError(t, err)

	final, err := svc.Wait(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, final.Status)
	require.Equal(t, "done", final.Result)
	require.NotNil(t, final.Progress)
	require.Equal(t, 3, final.Progress.EventCount)
	require.Equal(t, 1, final.Progress.ToolCallCount)
	require.Equal(t, 1, final.Progress.ToolResultCount)
	require.Equal(t, 6, final.Progress.PromptTokens)
	require.Equal(t, 4, final.Progress.CompletionTokens)
	require.Equal(t, 10, final.Progress.TotalTokens)
	require.NotNil(t, final.Progress.LastEventAt)
	require.Equal(t, startedAt.Add(3*time.Second), *final.Progress.LastEventAt)
}

func TestProgressAccumulatorHandlesSparseEvents(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	acc := progressAccumulator{}

	require.False(t, acc.consume(nil, now))
	require.Nil(t, acc.snapshot())

	require.True(t, acc.consume(&event.Event{}, now))
	require.True(t, acc.consume(&event.Event{
		Response: &model.Response{
			IsPartial: true,
			Usage: &model.Usage{
				PromptTokens:     100,
				CompletionTokens: 100,
				TotalTokens:      200,
			},
			Choices: []model.Choice{{
				Delta: model.Message{
					ToolCalls: []model.ToolCall{{
						ID: "partial-call",
						Function: model.FunctionDefinitionParam{
							Name: "lookup",
						},
					}},
				},
			}},
		},
	}, now.Add(time.Second)))

	got := acc.snapshot()
	require.NotNil(t, got)
	require.Equal(t, 2, got.EventCount)
	require.Zero(t, got.ToolCallCount)
	require.Zero(t, got.ToolResultCount)
	require.Zero(t, got.PromptTokens)
	require.Zero(t, got.CompletionTokens)
	require.Zero(t, got.TotalTokens)
	require.NotNil(t, got.LastEventAt)
	require.Equal(t, now.Add(time.Second), *got.LastEventAt)
}

func TestServiceUpdateProgressIgnoresNilMissingAndTerminalRuns(t *testing.T) {
	t.Parallel()

	svc, err := NewService(&captureRunner{})
	require.NoError(t, err)

	runningRunID := "running-run"
	terminalRunID := "terminal-run"
	svc.runs[runningRunID] = &Run{ID: runningRunID, Status: StatusRunning}
	svc.runs[terminalRunID] = &Run{ID: terminalRunID, Status: StatusCompleted}

	svc.updateProgress(runningRunID, nil)
	require.Nil(t, svc.runs[runningRunID].Progress)

	lastEventAt := time.Date(2026, 5, 26, 11, 0, 0, 0, time.UTC)
	progress := &Progress{
		EventCount:  1,
		LastEventAt: &lastEventAt,
	}
	svc.updateProgress("missing-run", progress)
	svc.updateProgress(terminalRunID, progress)
	require.Nil(t, svc.runs[terminalRunID].Progress)

	svc.updateProgress(runningRunID, progress)
	progress.EventCount = 2
	require.NotNil(t, svc.runs[runningRunID].Progress)
	require.Equal(t, 1, svc.runs[runningRunID].Progress.EventCount)
	require.NotSame(t, progress, svc.runs[runningRunID].Progress)
	require.NotSame(t, progress.LastEventAt, svc.runs[runningRunID].Progress.LastEventAt)
}

func TestServicePropagatesSpawnAppName(t *testing.T) {
	t.Parallel()

	runner := &captureRunner{reply: "ok"}
	svc, err := NewService(runner)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		AppName:         "tenant-app",
		Task:            "run under tenant app",
	})
	require.NoError(t, err)
	require.Equal(t, "tenant-app", run.AppName)

	final, err := svc.Wait(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, "tenant-app", final.AppName)

	runner.mu.Lock()
	defer runner.mu.Unlock()
	require.Equal(t, "tenant-app", runner.runOpts.AppName)
}

func TestServiceCustomRuntimeStateKeys(t *testing.T) {
	t.Parallel()

	runner := &captureRunner{reply: "ok"}
	svc, err := NewService(runner)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "custom runtime keys",
		RuntimeStateKeys: RuntimeStateKeys{
			Run:             "product.run",
			RunID:           "product.run_id",
			ParentSessionID: "product.parent_session_id",
		},
	})
	require.NoError(t, err)
	_, err = svc.Wait(context.Background(), run.ID)
	require.NoError(t, err)

	runner.mu.Lock()
	defer runner.mu.Unlock()
	require.Equal(t, true, runner.runOpts.RuntimeState["product.run"])
	require.Equal(t, run.ID, runner.runOpts.RuntimeState["product.run_id"])
	require.Equal(
		t,
		"parent-a",
		runner.runOpts.RuntimeState["product.parent_session_id"],
	)
	require.NotContains(t, runner.runOpts.RuntimeState, RuntimeStateKeyRun)
	require.NotContains(t, runner.runOpts.RuntimeState, RuntimeStateKeyRunID)
	require.NotContains(
		t,
		runner.runOpts.RuntimeState,
		RuntimeStateKeyParentSessionID,
	)
}

func TestServiceOptionsNilAndContextBranches(t *testing.T) {
	t.Parallel()

	_, err := NewService(nil)
	require.ErrorContains(t, err, "nil runner")

	_, err = NewService(&captureRunner{}, WithStore(&failingLoadStore{}))
	require.ErrorContains(t, err, "load boom")

	fixedNow := time.Date(2026, 4, 27, 1, 2, 3, 0, time.UTC)
	svc, err := NewService(
		&captureRunner{reply: "ok"},
		nil,
		WithStore(nil),
		WithClock(func() time.Time { return fixedNow }),
	)
	require.NoError(t, err)
	svc.Start(nil)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "clocked task",
	})
	require.NoError(t, err)
	require.Equal(t, fixedNow, run.CreatedAt)

	final, err := svc.Wait(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, final.Status)
	listed, err := svc.List(context.Background(), ListFilter{
		Status: StatusCompleted,
	})
	require.NoError(t, err)
	require.Len(t, listed, 1)

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.List(canceledCtx, ListFilter{})
	require.ErrorIs(t, err, context.Canceled)
	_, err = svc.Get(canceledCtx, run.ID)
	require.ErrorIs(t, err, context.Canceled)
	_, _, err = svc.Cancel(canceledCtx, run.ID)
	require.ErrorIs(t, err, context.Canceled)

	var nilSvc *Service
	nilSvc.Start(context.Background())
	require.NoError(t, nilSvc.Close())
	runs, err := nilSvc.List(context.Background(), ListFilter{})
	require.NoError(t, err)
	require.Nil(t, runs)
	_, err = nilSvc.Get(context.Background(), "missing")
	require.ErrorIs(t, err, ErrRunNotFound)
	_, _, err = nilSvc.Cancel(context.Background(), "missing")
	require.ErrorIs(t, err, ErrRunNotFound)
	_, err = nilSvc.Wait(context.Background(), "missing")
	require.ErrorIs(t, err, ErrRunNotFound)

	called := false
	ObserverFunc(func(ctx context.Context, run Run) {
		called = true
	}).OnRunUpdate(context.Background(), Run{ID: "run-1"})
	require.True(t, called)
	ObserverFunc(nil).OnRunUpdate(context.Background(), Run{})
}

func TestServiceCancelAndWait(t *testing.T) {
	t.Parallel()

	runner := &blockingRunner{started: make(chan struct{})}
	svc, err := NewService(runner)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "wait for cancel",
	})
	require.NoError(t, err)

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("task run did not start in time")
	}

	canceled, changed, err := svc.Cancel(context.Background(), run.ID)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, StatusCanceling, canceled.Status)

	final, err := svc.Wait(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCanceled, final.Status)
}

func TestServiceCancelWaitsForRunningChildExit(t *testing.T) {
	t.Parallel()

	runner := &controlledCancelRunner{
		started:   make(chan struct{}),
		canceling: make(chan struct{}),
		release:   make(chan struct{}),
	}
	svc, err := NewService(runner)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "wait for controlled cancel",
	})
	require.NoError(t, err)
	requireRunnerStarted(t, runner.started)

	canceled, changed, err := svc.Cancel(context.Background(), run.ID)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, StatusCanceling, canceled.Status)
	require.Nil(t, canceled.FinishedAt)

	select {
	case <-runner.canceling:
	case <-time.After(time.Second):
		t.Fatal("runner did not observe cancellation")
	}

	got, err := svc.Get(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCanceling, got.Status)
	require.Nil(t, got.FinishedAt)

	waitCh := make(chan waitResult, 1)
	go func() {
		final, waitErr := svc.Wait(context.Background(), run.ID)
		waitCh <- waitResult{run: final, err: waitErr}
	}()
	requireRegisteredWaiter(t, svc, run.ID)
	requireNoWaitResult(t, waitCh)

	runner.releaseOnce.Do(func() {
		close(runner.release)
	})
	result := requireWaitResult(t, waitCh)
	require.NoError(t, result.err)
	require.Equal(t, StatusCanceled, result.run.Status)
	require.NotNil(t, result.run.FinishedAt)
}

func TestServiceTimeoutWithoutEventErrorDoesNotComplete(t *testing.T) {
	t.Parallel()

	runner := &blockingRunner{started: make(chan struct{})}
	svc, err := NewService(runner)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "wait for timeout",
		Timeout:         shortRunTimeout,
	})
	require.NoError(t, err)
	requireRunnerStarted(t, runner.started)

	final, err := svc.Wait(context.Background(), run.ID)
	require.NoError(t, err)
	require.NotEqual(t, StatusCompleted, final.Status)
	require.Equal(t, StatusFailed, final.Status)
	require.Contains(t, final.Error, context.DeadlineExceeded.Error())
	require.Contains(t, final.Summary, context.DeadlineExceeded.Error())
}

func TestServiceParentCancelWithoutEventErrorCancelsRun(t *testing.T) {
	t.Parallel()

	parentCtx, parentCancel := context.WithCancel(context.Background())
	runner := &blockingRunner{started: make(chan struct{})}
	svc, err := NewService(runner)
	require.NoError(t, err)
	svc.Start(parentCtx)
	t.Cleanup(func() {
		parentCancel()
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "wait for parent cancellation",
	})
	require.NoError(t, err)
	requireRunnerStarted(t, runner.started)

	parentCancel()
	final, err := svc.Wait(context.Background(), run.ID)
	require.NoError(t, err)
	require.NotEqual(t, StatusCompleted, final.Status)
	require.Equal(t, StatusCanceled, final.Status)
	require.Empty(t, final.Error)
	require.Equal(t, statusCanceledSummary, final.Summary)
}

func TestServiceCancelPersistFailureWakesWaiters(t *testing.T) {
	t.Parallel()

	store := &failOnSaveStore{failAt: firstPersistAttempt}
	svc, err := NewService(&captureRunner{reply: "ok"}, WithStore(store))
	require.NoError(t, err)
	svc.Start(context.Background())

	now := time.Now()
	run := Run{
		ID:              "cancel-persist-failure",
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "cancel queued work",
		Status:          StatusQueued,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	svc.mu.Lock()
	svc.runs[run.ID] = runPtr(run)
	svc.mu.Unlock()

	waitCh := make(chan waitResult, 1)
	go func() {
		final, waitErr := svc.Wait(context.Background(), run.ID)
		waitCh <- waitResult{run: final, err: waitErr}
	}()
	requireRegisteredWaiter(t, svc, run.ID)

	_, _, err = svc.Cancel(context.Background(), run.ID)
	require.ErrorContains(t, err, testPersistBoom)

	result := requireWaitResult(t, waitCh)
	require.NoError(t, result.err)
	require.Equal(t, StatusCanceled, result.run.Status)
}

func TestServiceScopesAndErrors(t *testing.T) {
	t.Parallel()

	svc, err := NewService(&captureRunner{reply: "ok"})
	require.NoError(t, err)
	_, err = svc.Spawn(context.Background(), SpawnRequest{})
	require.ErrorIs(t, err, ErrNotStarted)

	svc.Start(context.Background())
	_, err = svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "parent-a",
		Task:            "task",
	})
	require.ErrorContains(t, err, "empty owner")

	_, err = svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID: "user-a",
		Task:        "task",
	})
	require.ErrorContains(t, err, "empty parent session id")

	_, err = svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
	})
	require.ErrorContains(t, err, "empty task")

	first, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "first",
	})
	require.NoError(t, err)
	_, err = svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-b",
		Task:            "second",
	})
	require.NoError(t, err)
	_, err = svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-b",
		ParentSessionID: "parent-a",
		Task:            "third",
	})
	require.NoError(t, err)

	_, err = svc.Spawn(context.Background(), SpawnRequest{
		ID:              first.ID,
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "duplicate",
	})
	require.ErrorIs(t, err, ErrRunAlreadyExists)

	require.Eventually(t, func() bool {
		runs, listErr := svc.List(context.Background(), ListFilter{
			OwnerUserID: "user-a",
		})
		return listErr == nil && len(runs) == 2
	}, time.Second, 10*time.Millisecond)

	filtered, err := svc.List(context.Background(), ListFilter{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, first.ID, filtered[0].ID)

	_, err = svc.Get(context.Background(), "missing")
	require.ErrorIs(t, err, ErrRunNotFound)

	_, _, err = svc.Cancel(context.Background(), "missing")
	require.ErrorIs(t, err, ErrRunNotFound)

	canceled, changed, err := svc.Cancel(context.Background(), first.ID)
	require.NoError(t, err)
	require.False(t, changed)
	require.True(t, canceled.Status.IsTerminal())
}

func TestServiceFinalizerAttachesMetadataBeforeTerminalUpdate(t *testing.T) {
	t.Parallel()

	observer := &captureObserver{}
	svc, err := NewService(
		&captureRunner{reply: "ok"},
		WithObserver(observer),
		WithFinalizer(FinalizerFunc(
			func(ctx context.Context, run Run) map[string]string {
				require.Equal(t, StatusCompleted, run.Status)
				return map[string]string{
					"added": " value with spaces ",
				}
			},
		)),
	)
	require.NoError(t, err)
	svc.Start(context.Background())

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "attach metadata",
		Metadata: map[string]string{
			"initial": "value",
		},
	})
	require.NoError(t, err)

	final, err := svc.Wait(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, "value", final.Metadata["initial"])
	require.Equal(t, "value with spaces", final.Metadata["added"])

	require.Eventually(t, func() bool {
		seen := observer.statuses()
		for _, status := range seen {
			if status != StatusCompleted {
				continue
			}
			observer.mu.Lock()
			defer observer.mu.Unlock()
			last := observer.runs[len(observer.runs)-1]
			return last.Metadata["added"] == "value with spaces"
		}
		return false
	}, time.Second, 10*time.Millisecond)
}

func TestFinalizerHelpersHandleNilBranches(t *testing.T) {
	t.Parallel()

	const helperRunID = "finalizer-helper-run"

	var fn FinalizerFunc
	require.Nil(t, fn.FinalizeRun(context.Background(), Run{}))
	require.Nil(t, runFinalizer(context.Background(), nil, Run{}))

	metadata := runFinalizer(
		nil,
		FinalizerFunc(func(ctx context.Context, run Run) map[string]string {
			require.NotNil(t, ctx)
			require.Equal(t, helperRunID, run.ID)
			return map[string]string{testFinalizedKey: testFinalizedValue}
		}),
		Run{ID: helperRunID},
	)
	require.Equal(t, testFinalizedValue, metadata[testFinalizedKey])

	var nilService *Service
	require.Nil(t, nilService.finalizeRun(context.Background(), Run{}))
	require.NotNil(t, nilService.finalizerBaseContext())

	svc := &Service{}
	require.NotNil(t, svc.finalizerBaseContext())
	require.NoError(t, nilService.persist(context.Background()))
	require.NoError(t, svc.persist(context.Background()))
}

func TestRunFinalizerRecoversPanic(t *testing.T) {
	t.Parallel()

	panicFinalizer := FinalizerFunc(
		func(ctx context.Context, run Run) map[string]string {
			panic(testFinalizerPanic)
		},
	)
	require.Nil(t, runFinalizer(context.Background(), panicFinalizer, Run{}))

	store := NewMemoryStore()
	now := time.Now()
	require.NoError(t, store.Save(context.Background(), []Run{{
		ID:              "panic-normalization-run",
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "normalize with panic",
		Status:          StatusRunning,
		CreatedAt:       now,
		UpdatedAt:       now,
		StartedAt:       cloneTime(now),
	}}))
	normalized, err := NewService(
		&captureRunner{reply: "ok"},
		WithStore(store),
		WithFinalizer(panicFinalizer),
	)
	require.NoError(t, err)
	restarted, err := normalized.Get(
		context.Background(),
		"panic-normalization-run",
	)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, restarted.Status)

	svc, err := NewService(
		&captureRunner{reply: "ok"},
		WithFinalizer(panicFinalizer),
	)
	require.NoError(t, err)
	svc.Start(context.Background())

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "complete with panic",
	})
	require.NoError(t, err)
	final, err := svc.Wait(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, final.Status)
	require.Empty(t, final.Metadata)
}

func TestServiceStopAllRunningHandlesNilAndExitingRuns(t *testing.T) {
	t.Parallel()

	activeCtx, activeCancel := context.WithCancel(context.Background())
	defer activeCancel()

	svc := &Service{
		running: map[string]*runningRun{
			"nil":     nil,
			"exiting": {exiting: true},
			"active":  {cancel: activeCancel},
		},
	}
	svc.stopAllRunning()

	svc.mu.Lock()
	_, hasNil := svc.running["nil"]
	exiting := svc.running["exiting"]
	active := svc.running["active"]
	svc.mu.Unlock()

	require.False(t, hasNil)
	require.False(t, exiting.cancelRequested)
	require.True(t, active.cancelRequested)
	require.ErrorIs(t, activeCtx.Err(), context.Canceled)
}

func TestServiceTerminalHelpersDropMissingRuns(t *testing.T) {
	t.Parallel()

	const terminalRunID = "terminal-helper-run"

	errTerminal := errors.New("terminal helper failure")
	newService := func() *Service {
		return &Service{
			store:   NewMemoryStore(),
			clock:   time.Now,
			runs:    map[string]*Run{},
			running: map[string]*runningRun{terminalRunID: {}},
		}
	}

	svc := newService()
	svc.finishRun(terminalRunID, "terminal helper result", nil, nil)
	requireNoRunningRun(t, svc, terminalRunID)

	svc = newService()
	svc.failPersistedRun(terminalRunID, errTerminal, time.Now())
	requireNoRunningRun(t, svc, terminalRunID)

	svc = newService()
	_, err := svc.finalizeCanceledRun(context.Background(), terminalRunID)
	require.ErrorIs(t, err, ErrRunNotFound)
}

func TestServiceTerminalPersistFailureDoesNotNotifyObserver(t *testing.T) {
	t.Parallel()

	errTerminal := errors.New("terminal persist failure")
	tests := []struct {
		name   string
		finish func(*Service, string)
	}{
		{
			name: "finished run",
			finish: func(svc *Service, runID string) {
				svc.finishRun(runID, "terminal helper result", nil, nil)
			},
		},
		{
			name: "failed persisted run",
			finish: func(svc *Service, runID string) {
				svc.failPersistedRun(runID, errTerminal, time.Now())
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runID := "terminal-persist-failure"
			observer := &captureObserver{}
			svc := &Service{
				store:    &failOnSaveStore{failAt: firstPersistAttempt},
				observer: observer,
				clock:    time.Now,
				runs: map[string]*Run{
					runID: runPtr(Run{
						ID:              runID,
						OwnerUserID:     "user-a",
						ParentSessionID: "parent-a",
						Task:            "finish with persist failure",
						Status:          StatusRunning,
						CreatedAt:       time.Now(),
						UpdatedAt:       time.Now(),
					}),
				},
				running: map[string]*runningRun{runID: {}},
				waiters: map[string][]chan struct{}{},
			}

			tt.finish(svc, runID)

			require.Empty(t, observer.statuses())
			requireNoRunningRun(t, svc, runID)
			got, err := svc.Get(context.Background(), runID)
			require.NoError(t, err)
			require.True(t, got.Status.IsTerminal())
		})
	}
}

func requireNoRunningRun(t *testing.T, svc *Service, runID string) {
	t.Helper()
	svc.mu.Lock()
	_, running := svc.running[runID]
	svc.mu.Unlock()
	require.False(t, running)
}

func TestServiceFinalizerRunsForQueuedCancel(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		&captureRunner{reply: "ok"},
		WithFinalizer(FinalizerFunc(
			func(ctx context.Context, run Run) map[string]string {
				require.Equal(t, StatusCanceled, run.Status)
				return map[string]string{
					testFinalizedKey: testFinalizedValue,
				}
			},
		)),
	)
	require.NoError(t, err)
	svc.Start(context.Background())

	now := time.Now()
	run := Run{
		ID:              "queued-cancel-finalizer",
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "cancel queued work",
		Status:          StatusQueued,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	svc.mu.Lock()
	svc.runs[run.ID] = runPtr(run)
	svc.mu.Unlock()

	canceled, changed, err := svc.Cancel(context.Background(), run.ID)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, StatusCanceled, canceled.Status)
	require.Equal(
		t,
		testFinalizedValue,
		canceled.Metadata[testFinalizedKey],
	)
}

func TestServiceCancelDuringFinalizerDoesNotOverrideExitStatus(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	var releaseOnce sync.Once
	svc, err := NewService(
		&captureRunner{reply: "done"},
		WithFinalizer(FinalizerFunc(
			func(ctx context.Context, run Run) map[string]string {
				enteredOnce.Do(func() {
					close(entered)
				})
				<-release
				return map[string]string{
					testFinalizedKey: testFinalizedValue,
				}
			},
		)),
	)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		releaseOnce.Do(func() {
			close(release)
		})
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "complete then finalize",
	})
	require.NoError(t, err)

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("finalizer did not run")
	}

	got, err := svc.Get(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusFinalizing, got.Status)

	waitCh := make(chan waitResult, 1)
	go func() {
		final, waitErr := svc.Wait(context.Background(), run.ID)
		waitCh <- waitResult{run: final, err: waitErr}
	}()
	select {
	case <-waitCh:
		t.Fatal("wait returned before finalizer completed")
	case <-time.After(noWaitResultDelay):
	}

	canceled, changed, err := svc.Cancel(context.Background(), run.ID)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, StatusFinalizing, canceled.Status)

	releaseOnce.Do(func() {
		close(release)
	})
	result := requireWaitResult(t, waitCh)
	require.NoError(t, result.err)
	final := result.run
	require.Equal(t, StatusCompleted, final.Status)
	require.Equal(t, testFinalizedValue, final.Metadata[testFinalizedKey])
}

func TestServiceCloseCancelsFinalizerContext(t *testing.T) {
	entered := make(chan struct{})
	finalized := make(chan struct{})
	var enteredOnce sync.Once
	var finalizedOnce sync.Once
	svc, err := NewService(
		&captureRunner{reply: "done"},
		WithFinalizer(FinalizerFunc(
			func(ctx context.Context, run Run) map[string]string {
				enteredOnce.Do(func() {
					close(entered)
				})
				<-ctx.Done()
				finalizedOnce.Do(func() {
					close(finalized)
				})
				return map[string]string{
					testFinalizedKey: testFinalizedValue,
				}
			},
		)),
	)
	require.NoError(t, err)
	defer func() {
		_ = svc.Close()
	}()
	svc.Start(context.Background())

	_, err = svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "close during finalizer",
	})
	require.NoError(t, err)

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("finalizer did not run")
	}

	closed := make(chan error, 1)
	go func() {
		closed <- svc.Close()
	}()

	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("close did not cancel finalizer")
	}
	select {
	case <-finalized:
	default:
		t.Fatal("finalizer did not observe context cancellation")
	}
}

func TestServiceCancelAfterChildReplyDoesNotOverrideExitStatus(t *testing.T) {
	t.Parallel()

	runner := &exitGateRunner{
		started:    make(chan struct{}),
		unblock:    make(chan struct{}),
		afterReply: make(chan struct{}),
	}
	svc, err := NewService(runner)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "complete then cancel race",
	})
	require.NoError(t, err)
	requireRunnerStarted(t, runner.started)

	close(runner.unblock)
	select {
	case <-runner.afterReply:
	case <-time.After(time.Second):
		t.Fatal("runner did not emit reply")
	}

	require.Eventually(t, func() bool {
		got, getErr := svc.Get(context.Background(), run.ID)
		return getErr == nil && got.Status == StatusCompleted
	}, time.Second, time.Millisecond)

	canceled, changed, err := svc.Cancel(context.Background(), run.ID)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, StatusCompleted, canceled.Status)
}

func TestFinishedRunViewIgnoresLateCancelAfterSuccessfulExit(t *testing.T) {
	t.Parallel()

	run := Run{
		ID:              "late-cancel",
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "complete first",
		Status:          StatusRunning,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	final := finishedRunView(
		run,
		testCompletedOutput,
		nil,
		nil,
		time.Now(),
	)

	require.Equal(t, StatusCompleted, final.Status)
	require.Equal(t, testCompletedOutput, final.Result)
}

func TestFinishedRunViewKeepsChildFailureAfterLateCancel(t *testing.T) {
	t.Parallel()

	runErr := errors.New(testChildFailed)
	final := finishedRunView(
		Run{
			ID:              "late-cancel-error",
			OwnerUserID:     "user-a",
			ParentSessionID: "parent-a",
			Task:            "fail first",
			Status:          StatusRunning,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		},
		"",
		runErr,
		nil,
		time.Now(),
	)

	require.Equal(t, StatusFailed, final.Status)
	require.Equal(t, runErr.Error(), final.Error)
}

func TestFinishedRunViewPreservesCancelingTerminalState(t *testing.T) {
	t.Parallel()

	now := time.Now()
	base := Run{
		ID:              "canceling-terminal",
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "cancel first",
		Status:          StatusCanceling,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	noErr := finishedRunView(base, "", nil, nil, now)
	require.Equal(t, StatusCanceled, noErr.Status)
	require.Empty(t, noErr.Error)

	sentinelCancel := finishedRunView(base, "", context.Canceled, nil, now)
	require.Equal(t, StatusCanceled, sentinelCancel.Status)
	require.Empty(t, sentinelCancel.Error)

	customCancelErr := errors.New(testWorkerCanceledRequest)
	customErr := finishedRunView(base, "", customCancelErr, nil, now)
	require.Equal(t, StatusFailed, customErr.Status)
	require.Equal(t, customCancelErr.Error(), customErr.Error)

	cancelToolLeaseErr := errors.New(testCancelToolLeaseFailure)
	cancelToolLeaseFailure := finishedRunView(base, "", cancelToolLeaseErr, nil, now)
	require.Equal(t, StatusFailed, cancelToolLeaseFailure.Status)
	require.Equal(t, cancelToolLeaseErr.Error(), cancelToolLeaseFailure.Error)

	childErr := errors.New(testChildFailed)
	failed := finishedRunView(base, "", childErr, nil, now)
	require.Equal(t, StatusFailed, failed.Status)
	require.Equal(t, childErr.Error(), failed.Error)
}

func TestServiceFailureAndRestartNormalization(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	now := time.Now()
	require.NoError(t, store.Save(context.Background(), []Run{{
		ID:              "run-1",
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "resume me",
		Status:          StatusRunning,
		CreatedAt:       now,
		UpdatedAt:       now,
		StartedAt:       cloneTime(now),
	}}))

	svc, err := NewService(
		&captureRunner{reply: "ok"},
		WithStore(store),
		WithFinalizer(FinalizerFunc(
			func(ctx context.Context, run Run) map[string]string {
				require.Equal(t, StatusFailed, run.Status)
				_, ok := ctx.Deadline()
				require.True(t, ok)
				return map[string]string{
					testFinalizedKey: testFinalizedValue,
				}
			},
		)),
	)
	require.NoError(t, err)
	run, err := svc.Get(context.Background(), "run-1")
	require.NoError(t, err)
	require.Equal(t, StatusFailed, run.Status)
	require.Contains(t, run.Error, "previous runtime restart")
	require.Equal(t, testFinalizedValue, run.Metadata[testFinalizedKey])

	failing, err := NewService(&captureRunner{
		runErr: errors.New("runner boom"),
	})
	require.NoError(t, err)
	failing.Start(context.Background())

	spawned, err := failing.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "fail this run",
	})
	require.NoError(t, err)
	final, err := failing.Wait(context.Background(), spawned.ID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, final.Status)
	require.Contains(t, final.Error, "runner boom")

	canceling, err := NewService(&captureRunner{
		runErr: context.Canceled,
	})
	require.NoError(t, err)
	canceling.Start(context.Background())

	canceled, err := canceling.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "cancel this run",
	})
	require.NoError(t, err)
	final, err = canceling.Wait(context.Background(), canceled.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCanceled, final.Status)
}

func TestServiceMarksRunFailedWhenPersistingRunningStateFails(
	t *testing.T,
) {
	t.Parallel()

	store := &failOnSaveStore{failAt: runningStatePersistAttempt}
	svc, err := NewService(
		&captureRunner{reply: "ok"},
		WithStore(store),
		WithFinalizer(FinalizerFunc(
			func(ctx context.Context, run Run) map[string]string {
				require.Equal(t, StatusFailed, run.Status)
				return map[string]string{
					testFinalizedKey: testFinalizedValue,
				}
			},
		)),
	)
	require.NoError(t, err)
	svc.Start(context.Background())

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "fail persistence",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		got, getErr := svc.Get(context.Background(), run.ID)
		return getErr == nil &&
			got.Status == StatusFailed &&
			strings.Contains(got.Error, testPersistBoom) &&
			got.Metadata[testFinalizedKey] == testFinalizedValue
	}, time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		stored, loadErr := store.Load(context.Background())
		if loadErr != nil || len(stored) != 1 {
			return false
		}
		return stored[0].Status == StatusFailed &&
			stored[0].Metadata[testFinalizedKey] == testFinalizedValue
	}, time.Second, 10*time.Millisecond)
}

func TestServiceRunningPersistFailureWakesWaiters(t *testing.T) {
	t.Parallel()

	store := newBlockingFailStore(runningStatePersistAttempt)
	defer store.unblock()
	svc, err := NewService(
		&captureRunner{reply: "ok"},
		WithStore(store),
	)
	require.NoError(t, err)
	svc.Start(context.Background())

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "fail running persistence",
	})
	require.NoError(t, err)

	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("persist did not reach the failing save")
	}

	waitCh := make(chan waitResult, 1)
	go func() {
		final, waitErr := svc.Wait(context.Background(), run.ID)
		waitCh <- waitResult{run: final, err: waitErr}
	}()
	requireRegisteredWaiter(t, svc, run.ID)

	store.unblock()

	result := requireWaitResult(t, waitCh)
	require.NoError(t, result.err)
	require.Equal(t, StatusFailed, result.run.Status)
	require.Contains(t, result.run.Error, testPersistBoom)
}

func TestServiceWaitHonorsContext(t *testing.T) {
	t.Parallel()

	runner := &blockingRunner{started: make(chan struct{})}
	svc, err := NewService(runner)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Task:            "long work",
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.Wait(ctx, run.ID)
	require.ErrorIs(t, err, context.Canceled)
}
