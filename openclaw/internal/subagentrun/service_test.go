//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package subagentrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	publicsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
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

type stubSender struct {
	mu      sync.Mutex
	target  string
	text    string
	sendErr error
}

func (s *stubSender) ID() string {
	return "telegram"
}

func (s *stubSender) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (s *stubSender) SendText(
	ctx context.Context,
	target string,
	text string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.target = target
	s.text = text
	return s.sendErr
}

func (s *stubSender) snapshot() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.target, s.text
}

func TestServiceSpawnCompletesRunAndNotifies(t *testing.T) {
	t.Parallel()

	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	runner := &captureRunner{reply: "finished delegated work"}
	svc, err := NewService(t.TempDir(), runner, router)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "telegram:user",
		ParentSessionID: "telegram:dm:100",
		Task:            "check the incident timeline",
		TimeoutSeconds:  30,
		Delivery: deliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)
	require.Equal(t, publicsubagent.StatusQueued, run.Status)

	require.Eventually(t, func() bool {
		current, getErr := svc.GetForUser("telegram:user", run.ID)
		if getErr != nil || current == nil {
			return false
		}
		return current.Status == publicsubagent.StatusCompleted
	}, time.Second, 10*time.Millisecond)

	current, err := svc.GetForUser("telegram:user", run.ID)
	require.NoError(t, err)
	require.Equal(t, publicsubagent.StatusCompleted, current.Status)
	require.Equal(t, "finished delegated work", current.Result)
	require.Equal(t, "finished delegated work", current.Summary)
	require.NotEmpty(t, current.ChildSessionID)

	runs := svc.ListForUser("telegram:user", publicsubagent.ListFilter{
		ParentSessionID: "telegram:dm:100",
	})
	require.Len(t, runs, 1)
	require.Equal(t, run.ID, runs[0].ID)

	runner.mu.Lock()
	require.Equal(t, "telegram:user", runner.userID)
	require.Equal(t, "check the incident timeline", runner.message.Content)
	require.True(
		t,
		strings.HasPrefix(runner.sessionID, subagentSessionPrefix),
	)
	require.Equal(
		t,
		true,
		runner.runOpts.RuntimeState[runtimeStateSubagentRun],
	)
	require.Equal(
		t,
		run.ID,
		runner.runOpts.RuntimeState[runtimeStateSubagentRunID],
	)
	require.Equal(
		t,
		"telegram:dm:100",
		runner.runOpts.RuntimeState[runtimeStateSubagentParentID],
	)
	require.Equal(
		t,
		"telegram",
		runner.runOpts.RuntimeState["openclaw.delivery.channel"],
	)
	require.Equal(
		t,
		"100",
		runner.runOpts.RuntimeState["openclaw.delivery.target"],
	)
	require.Len(t, runner.runOpts.InjectedContextMessages, 1)
	require.Equal(
		t,
		subagentRunPrompt,
		runner.runOpts.InjectedContextMessages[0].Content,
	)
	runner.mu.Unlock()

	require.Eventually(t, func() bool {
		target, text := sender.snapshot()
		return target == "100" &&
			strings.Contains(text, notificationPrefixCompleted) &&
			strings.Contains(text, run.ID)
	}, time.Second, 10*time.Millisecond)
}

func TestServiceCancelForUser(t *testing.T) {
	t.Parallel()

	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	runner := &blockingRunner{started: make(chan struct{})}
	svc, err := NewService(t.TempDir(), runner, router)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "session-a",
		Task:            "wait for cancel",
		Delivery: deliveryTarget{
			Channel: "telegram",
			Target:  "900",
		},
	})
	require.NoError(t, err)

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("subagent run did not start in time")
	}

	canceled, changed, err := svc.CancelForUser("user-a", run.ID)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, publicsubagent.StatusCanceled, canceled.Status)

	require.Eventually(t, func() bool {
		current, getErr := svc.GetForUser("user-a", run.ID)
		if getErr != nil || current == nil {
			return false
		}
		return current.Status == publicsubagent.StatusCanceled
	}, time.Second, 10*time.Millisecond)

	_, text := sender.snapshot()
	require.Empty(t, text)
}

func TestServiceListForUserScopesByOwnerAndParent(t *testing.T) {
	t.Parallel()

	runner := &captureRunner{reply: "ok"}
	svc, err := NewService(t.TempDir(), runner, nil)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

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

	require.Eventually(t, func() bool {
		return len(svc.ListForUser("user-a", publicsubagent.ListFilter{})) == 2
	}, time.Second, 10*time.Millisecond)

	filtered := svc.ListForUser("user-a", publicsubagent.ListFilter{
		ParentSessionID: "parent-a",
	})
	require.Len(t, filtered, 1)
	require.Equal(t, first.ID, filtered[0].ID)
}

func TestNewServiceMarksInterruptedRunsFailed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	startedAt := time.Now().Add(-time.Minute)
	path := filepath.Join(
		dir,
		subagentDirName,
		subagentRunsFileName,
	)
	runs := map[string]*runRecord{
		"run-1": {
			Run: publicsubagent.Run{
				ID:              "run-1",
				ParentSessionID: "parent",
				Task:            "resume me",
				Status:          publicsubagent.StatusRunning,
				CreatedAt:       startedAt,
				UpdatedAt:       startedAt,
				StartedAt:       cloneTime(startedAt),
			},
			OwnerUserID: "user-a",
		},
	}
	require.NoError(t, saveRuns(path, runs))

	svc, err := NewService(dir, &captureRunner{reply: "ok"}, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.GetForUser("user-a", "run-1")
	require.NoError(t, err)
	require.Equal(t, publicsubagent.StatusFailed, run.Status)
	require.Contains(t, run.Error, "previous runtime restart")
}

func TestNewServiceValidatesInput(t *testing.T) {
	t.Parallel()

	_, err := NewService("", &captureRunner{reply: "ok"}, nil)
	require.ErrorContains(t, err, "empty state dir")

	_, err = NewService(t.TempDir(), nil, nil)
	require.ErrorContains(t, err, "nil runner")
}

func TestFormatNotification(t *testing.T) {
	t.Parallel()

	record := &runRecord{
		Run: publicsubagent.Run{
			ID:      "run-1",
			Status:  publicsubagent.StatusCompleted,
			Result:  "full result",
			Summary: "summary only",
		},
	}
	require.Contains(t, formatNotification(record), notificationPrefixCompleted)
	require.Contains(t, formatNotification(record), "full result")
	require.NotContains(t, formatNotification(record), "summary only")

	record.Status = publicsubagent.StatusFailed
	record.Summary = "boom"
	require.Contains(t, formatNotification(record), notificationPrefixFailed)

	record.Status = publicsubagent.StatusCanceled
	require.Contains(t, formatNotification(record), notificationPrefixCanceled)

	record.Status = publicsubagent.StatusQueued
	require.Empty(t, formatNotification(record))
}

func TestReplyAccumulatorConsumeDeltaAndError(t *testing.T) {
	t.Parallel()

	var acc replyAccumulator
	acc.consume(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletionChunk,
			Choices: []model.Choice{{
				Delta: model.Message{Content: "hello "},
			}},
		},
	})
	acc.consume(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletionChunk,
			Choices: []model.Choice{{
				Delta: model.Message{Content: "world"},
			}},
		},
	})
	require.Equal(t, "hello world", acc.text)

	acc.consume(&event.Event{
		Response: &model.Response{
			Error: &model.ResponseError{
				Message: "stream failed",
			},
		},
	})
	require.ErrorContains(t, acc.err, "stream failed")
}

func TestServiceSpawnValidation(t *testing.T) {
	t.Parallel()

	svc, err := NewService(t.TempDir(), &captureRunner{reply: "ok"}, nil)
	require.NoError(t, err)

	_, err = svc.Spawn(context.Background(), SpawnRequest{})
	require.ErrorContains(t, err, "not started")

	svc.Start(context.Background())

	_, err = svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "session-a",
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
		ParentSessionID: "session-a",
	})
	require.ErrorContains(t, err, "empty task")
}

func TestServiceRunFailureMarksRunFailed(t *testing.T) {
	t.Parallel()

	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	runner := &captureRunner{runErr: errors.New("runner boom")}
	svc, err := NewService(t.TempDir(), runner, router)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	run, err := svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "session-a",
		Task:            "fail this run",
		Delivery: deliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		current, getErr := svc.GetForUser("user-a", run.ID)
		if getErr != nil || current == nil {
			return false
		}
		return current.Status == publicsubagent.StatusFailed
	}, time.Second, 10*time.Millisecond)

	current, err := svc.GetForUser("user-a", run.ID)
	require.NoError(t, err)
	require.Equal(t, publicsubagent.StatusFailed, current.Status)
	require.Contains(t, current.Error, "runner boom")

	require.Eventually(t, func() bool {
		_, text := sender.snapshot()
		return strings.Contains(text, notificationPrefixFailed)
	}, time.Second, 10*time.Millisecond)
}

func TestServiceHelperPaths(t *testing.T) {
	t.Parallel()

	var nilSvc *Service
	nilSvc.Start(context.Background())
	require.NoError(t, nilSvc.Close())
	require.NoError(t, nilSvc.persist())

	svc, err := NewService(t.TempDir(), &captureRunner{reply: "ok"}, nil)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	_, err = svc.GetForUser("user-a", "missing")
	require.ErrorIs(t, err, publicsubagent.ErrRunNotFound)

	_, _, err = svc.CancelForUser("user-a", "missing")
	require.ErrorIs(t, err, publicsubagent.ErrRunNotFound)

	require.Empty(t, formatNotification(nil))
}

func TestServiceFinishRunCanceledPaths(t *testing.T) {
	t.Parallel()

	svc, err := NewService(t.TempDir(), &captureRunner{reply: "ok"}, nil)
	require.NoError(t, err)

	now := time.Now()
	svc.clock = func() time.Time {
		return now
	}

	svc.runs["run-1"] = &runRecord{
		Run: publicsubagent.Run{
			ID:        "run-1",
			Status:    publicsubagent.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		OwnerUserID: "user-a",
	}
	svc.running["run-1"] = &runningRun{cancelRequested: true}
	svc.finishRun("run-1", "ignored", nil)

	run, err := svc.GetForUser("user-a", "run-1")
	require.NoError(t, err)
	require.Equal(t, publicsubagent.StatusCanceled, run.Status)
	require.Equal(t, "canceled", run.Summary)

	svc.runs["run-2"] = &runRecord{
		Run: publicsubagent.Run{
			ID:        "run-2",
			Status:    publicsubagent.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		OwnerUserID: "user-a",
	}
	svc.running["run-2"] = &runningRun{}
	svc.finishRun("run-2", "", context.Canceled)

	run, err = svc.GetForUser("user-a", "run-2")
	require.NoError(t, err)
	require.Equal(t, publicsubagent.StatusCanceled, run.Status)
}

func TestServiceNotifyCompletionToleratesSenderError(t *testing.T) {
	t.Parallel()

	router := outbound.NewRouter()
	sender := &stubSender{sendErr: errors.New("send failed")}
	router.RegisterSender(sender)

	svc, err := NewService(t.TempDir(), &captureRunner{reply: "ok"}, router)
	require.NoError(t, err)

	svc.notifyCompletion(&runRecord{
		Run: publicsubagent.Run{
			ID:      "run-1",
			Status:  publicsubagent.StatusCompleted,
			Summary: "done",
		},
		Delivery: deliveryTarget{
			Channel: "telegram",
			Target:  "42",
		},
	})

	target, text := sender.snapshot()
	require.Equal(t, "42", target)
	require.Contains(t, text, "run-1")

	svc.notifyCompletion(&runRecord{
		Run: publicsubagent.Run{
			ID:     "run-2",
			Status: publicsubagent.StatusCompleted,
		},
	})
}

func TestServiceErrorBranches(t *testing.T) {
	t.Parallel()

	var nilSvc *Service
	_, err := nilSvc.Spawn(context.Background(), SpawnRequest{})
	require.ErrorContains(t, err, "nil service")
	require.Nil(t, nilSvc.ListForUser("user-a", publicsubagent.ListFilter{}))

	svc := &Service{
		path:    t.TempDir(),
		clock:   time.Now,
		runs:    make(map[string]*runRecord),
		running: make(map[string]*runningRun),
	}

	_, _, _, err = svc.markRunning(
		context.Background(),
		"missing",
		0,
	)
	require.ErrorIs(t, err, publicsubagent.ErrRunNotFound)

	svc.runs["run-canceled"] = &runRecord{
		Run: publicsubagent.Run{
			ID:     "run-canceled",
			Status: publicsubagent.StatusCanceled,
		},
	}
	_, _, _, err = svc.markRunning(
		context.Background(),
		"run-canceled",
		0,
	)
	require.ErrorContains(t, err, "run canceled before start")

	badPath := filepath.Join(t.TempDir(), "runs-dir")
	require.NoError(t, os.MkdirAll(badPath, 0o700))
	svc.path = badPath
	svc.runs["run-persist"] = &runRecord{
		Run: publicsubagent.Run{
			ID:        "run-persist",
			Status:    publicsubagent.StatusQueued,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		OwnerUserID: "user-a",
	}
	_, _, _, err = svc.markRunning(
		context.Background(),
		"run-persist",
		0,
	)
	require.Error(t, err)
	run, getErr := svc.GetForUser("user-a", "run-persist")
	require.NoError(t, getErr)
	require.Equal(t, publicsubagent.StatusFailed, run.Status)

	require.ErrorContains(
		t,
		svc.runChild(context.Background(), nil, runningRun{}, &replyAccumulator{}),
		"nil run record",
	)

	svc.finishRun("missing", "", nil)

	canceled := false
	svc.running = map[string]*runningRun{
		"nil-entry": nil,
		"active": {
			cancel: func() {
				canceled = true
			},
		},
	}
	svc.stopAllRunning()
	require.True(t, canceled)
	require.NotNil(t, svc.running["active"])
	require.True(t, svc.running["active"].cancelRequested)
}

func TestReplyAccumulatorNoOpBranches(t *testing.T) {
	t.Parallel()

	var acc replyAccumulator
	acc.consume(nil)
	acc.consume(&event.Event{
		Response: &model.Response{},
	})
	acc.consume(&event.Event{
		Response: &model.Response{
			Error: &model.ResponseError{Message: "event failed"},
		},
	})
	require.ErrorContains(t, acc.err, "event failed")

	acc = replyAccumulator{}
	acc.consumeFull(nil)
	acc.consumeFull(&model.Response{})
	acc.consumeFull(&model.Response{
		Choices: []model.Choice{{}},
	})
	require.Empty(t, acc.text)

	acc.consumeDelta(nil)
	acc.seenFull = true
	acc.consumeDelta(&model.Response{
		Choices: []model.Choice{{
			Delta: model.Message{Content: "ignored"},
		}},
	})
	require.Empty(t, acc.text)

	acc = replyAccumulator{}
	acc.consumeDelta(&model.Response{
		Choices: []model.Choice{{}},
	})
	require.Empty(t, acc.text)
}
