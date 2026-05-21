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
)

type captureRunner struct {
	mu        sync.Mutex
	userID    string
	sessionID string
	message   model.Message
	runOpts   agent.RunOptions
	reply     string
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
	r.userID = userID
	r.sessionID = sessionID
	r.message = message
	var runOpts agent.RunOptions
	for _, opt := range opts {
		opt(&runOpts)
	}
	r.runOpts = runOpts
	reply := r.reply
	runErr := r.runErr
	r.mu.Unlock()

	if runErr != nil {
		return nil, runErr
	}

	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage(reply),
			}},
		},
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
		AgentName:       "worker",
		Task:            "review the patch",
		Timeout:         time.Second,
		RuntimeState: map[string]any{
			"trace_id": "trace-1",
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

	final, err := svc.Wait(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, final.Status)
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
	require.Equal(t, StatusCanceled, canceled.Status)

	final, err := svc.Wait(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCanceled, final.Status)
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

	svc, err := NewService(&captureRunner{reply: "ok"}, WithStore(store))
	require.NoError(t, err)
	run, err := svc.Get(context.Background(), "run-1")
	require.NoError(t, err)
	require.Equal(t, StatusFailed, run.Status)
	require.Contains(t, run.Error, "previous runtime restart")

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
			strings.Contains(got.Error, testPersistBoom)
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
