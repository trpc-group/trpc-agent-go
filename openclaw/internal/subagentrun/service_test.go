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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	coretaskrun "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
	openclawsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
)

const (
	testStoreDirPerm  = 0o700
	testStoreFilePerm = 0o600
	testProfileID     = "reviewer"
	testProfilePrompt = "profile instruction"
	testProfileState  = "profile_state"
	testProfileSystem = "profile system prompt"
	testProfileRoot   = "/tmp/openclaw-profile-root"
	testProfileUserID = "telegram:user"
)

type captureRunner struct {
	mu        sync.Mutex
	ctx       context.Context
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

	spawnCtx := runtimeprofile.WithRequest(
		runtimeprofile.WithProfile(
			context.Background(),
			runtimeprofile.Profile{
				ID: testProfileID,
				Prompt: runtimeprofile.Prompt{
					Instruction:  testProfilePrompt,
					SystemPrompt: testProfileSystem,
				},
				Workspace: runtimeprofile.WorkspacePolicy{
					AllowedRoots: []string{testProfileRoot},
				},
				State: map[string]any{
					testProfileState: "profile-a",
				},
			},
		),
		runtimeprofile.Request{
			ProfileID: testProfileID,
			UserID:    testProfileUserID,
		},
	)
	run, err := svc.Spawn(spawnCtx, SpawnRequest{
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
	require.Equal(t, openclawsubagent.StatusQueued, run.Status)
	require.True(t, strings.HasPrefix(run.ID, subagentIDPrefix))

	final, err := svc.WaitForUser(
		context.Background(),
		"telegram:user",
		run.ID,
	)
	require.NoError(t, err)
	require.Equal(t, openclawsubagent.StatusCompleted, final.Status)
	require.Equal(t, "finished delegated work", final.Result)
	requireRunHidesInternalFields(t, *final)

	runs := svc.ListForUser("telegram:user", openclawsubagent.ListFilter{
		ParentSessionID: "telegram:dm:100",
	})
	require.Len(t, runs, 1)
	require.Equal(t, run.ID, runs[0].ID)

	runner.mu.Lock()
	require.Equal(t, "telegram:user", runner.userID)
	require.Equal(t, "check the incident timeline", runner.message.Content)
	require.True(t, strings.HasPrefix(runner.sessionID, subagentIDPrefix))
	parentSessionKey := openclawsubagent.RuntimeStateKeyParentSessionID
	require.Equal(
		t,
		true,
		runner.runOpts.RuntimeState[openclawsubagent.RuntimeStateKeyRun],
	)
	require.Equal(
		t,
		run.ID,
		runner.runOpts.RuntimeState[openclawsubagent.RuntimeStateKeyRunID],
	)
	require.Equal(
		t,
		"telegram:dm:100",
		runner.runOpts.RuntimeState[parentSessionKey],
	)
	require.NotContains(
		t,
		runner.runOpts.RuntimeState,
		coretaskrun.RuntimeStateKeyRun,
	)
	require.NotContains(
		t,
		runner.runOpts.RuntimeState,
		coretaskrun.RuntimeStateKeyRunID,
	)
	require.NotContains(
		t,
		runner.runOpts.RuntimeState,
		coretaskrun.RuntimeStateKeyParentSessionID,
	)
	require.Equal(
		t,
		"telegram",
		runner.runOpts.RuntimeState["openclaw.delivery.channel"],
	)
	require.Equal(t, testProfilePrompt, runner.runOpts.Instruction)
	require.Equal(
		t,
		testProfileSystem,
		runner.runOpts.GlobalInstruction,
	)
	require.Equal(
		t,
		testProfileID,
		runner.runOpts.RuntimeState[runtimeprofile.RuntimeStateProfileID],
	)
	require.Equal(
		t,
		"profile-a",
		runner.runOpts.RuntimeState[testProfileState],
	)
	workspace, ok := runtimeprofile.WorkspaceFromContext(runner.ctx)
	require.True(t, ok)
	require.Equal(t, []string{testProfileRoot}, workspace.AllowedRoots)
	req, ok := runtimeprofile.RequestFromContext(runner.ctx)
	require.True(t, ok)
	require.Equal(t, testProfileID, req.ProfileID)
	require.Equal(t, testProfileUserID, req.UserID)
	require.Len(t, runner.runOpts.InjectedContextMessages, 1)
	require.Equal(
		t,
		subagentRunPrompt,
		runner.runOpts.InjectedContextMessages[0].Content,
	)
	require.Contains(
		t,
		runner.runOpts.InjectedContextMessages[0].Content,
		"Do not return only a statement of what you will do",
	)
	runner.mu.Unlock()

	require.Eventually(t, func() bool {
		target, text := sender.snapshot()
		return target == "100" &&
			strings.Contains(text, notificationPrefixCompleted) &&
			strings.Contains(text, run.ID)
	}, time.Second, 10*time.Millisecond)
}

func requireRunHidesInternalFields(t *testing.T, run openclawsubagent.Run) {
	t.Helper()

	data, err := json.Marshal(run)
	require.NoError(t, err)
	payload := string(data)
	require.NotContains(t, payload, "owner_user_id")
	require.NotContains(t, payload, "request_id")
	require.NotContains(t, payload, "agent_name")
	require.NotContains(t, payload, "metadata")
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
	require.Equal(t, openclawsubagent.StatusCanceled, canceled.Status)

	final, err := svc.WaitForUser(context.Background(), "user-a", run.ID)
	require.NoError(t, err)
	require.Equal(t, openclawsubagent.StatusCanceled, final.Status)

	_, text := sender.snapshot()
	require.Empty(t, text)
}

func TestServiceListScopesByOwnerAndParent(t *testing.T) {
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
		return len(svc.ListForUser(
			"user-a",
			openclawsubagent.ListFilter{},
		)) == 2
	}, time.Second, 10*time.Millisecond)

	filtered := svc.ListForUser("user-a", openclawsubagent.ListFilter{
		ParentSessionID: "parent-a",
	})
	require.Len(t, filtered, 1)
	require.Equal(t, first.ID, filtered[0].ID)
}

func TestServiceLoadsLegacySubagentRunsFile(t *testing.T) {
	t.Parallel()

	const (
		legacyRunID          = "subagent:legacy"
		legacyOwnerUserID    = "user-a"
		legacyParentSession  = "parent-a"
		legacyTask           = "legacy task"
		legacyDelivery       = "telegram"
		legacyDeliveryTarget = "100"
	)

	stateDir := t.TempDir()
	storePath := subagentStorePath(stateDir)
	require.NoError(t, os.MkdirAll(
		filepath.Dir(storePath),
		testStoreDirPerm,
	))

	createdAt := time.Now().Add(-time.Hour).UTC()
	startedAt := createdAt.Add(time.Minute)
	legacyFile := struct {
		Version int               `json:"version"`
		Runs    []coretaskrun.Run `json:"runs,omitempty"`
	}{
		Version: 1,
		Runs: []coretaskrun.Run{{
			ID:              legacyRunID,
			OwnerUserID:     legacyOwnerUserID,
			ParentSessionID: legacyParentSession,
			ChildSessionID:  legacyRunID,
			RequestID:       legacyRunID,
			Task:            legacyTask,
			Status:          coretaskrun.StatusRunning,
			Metadata: map[string]string{
				metadataDeliveryChannel: legacyDelivery,
				metadataDeliveryTarget:  legacyDeliveryTarget,
			},
			CreatedAt: createdAt,
			UpdatedAt: startedAt,
			StartedAt: &startedAt,
		}},
	}
	data, err := json.Marshal(legacyFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		storePath,
		data,
		testStoreFilePerm,
	))

	svc, err := NewService(stateDir, &captureRunner{reply: "ok"}, nil)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	runs := svc.ListForUser(legacyOwnerUserID, openclawsubagent.ListFilter{
		ParentSessionID: legacyParentSession,
	})
	require.Len(t, runs, 1)
	require.Equal(t, legacyRunID, runs[0].ID)
	require.Equal(t, legacyParentSession, runs[0].ParentSessionID)
	require.Equal(t, legacyRunID, runs[0].ChildSessionID)
	require.Equal(t, legacyTask, runs[0].Task)
	require.Equal(t, openclawsubagent.StatusFailed, runs[0].Status)
	requireRunHidesInternalFields(t, runs[0])
}

func TestServiceValidatesInputAndPropagatesErrors(t *testing.T) {
	t.Parallel()

	_, err := NewService("", &captureRunner{reply: "ok"}, nil)
	require.ErrorContains(t, err, "empty state dir")

	_, err = NewService(t.TempDir(), nil, nil)
	require.ErrorContains(t, err, "nil runner")

	var nilSvc *Service
	require.NoError(t, nilSvc.Close())
	nilSvc.Start(context.Background())
	_, err = nilSvc.Spawn(context.Background(), SpawnRequest{})
	require.ErrorContains(t, err, "nil service")
	require.Nil(
		t,
		nilSvc.ListForUser("user-a", openclawsubagent.ListFilter{}),
	)

	svc, err := NewService(t.TempDir(), &captureRunner{reply: "ok"}, nil)
	require.NoError(t, err)
	_, err = svc.Spawn(context.Background(), SpawnRequest{})
	require.ErrorIs(t, err, openclawsubagent.ErrNotStarted)
	svc.Start(context.Background())

	_, err = svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "session-a",
		Task:            "task",
	})
	require.ErrorContains(t, err, "subagent: empty owner")

	_, err = svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID: "user-a",
		Task:        "task",
	})
	require.ErrorContains(t, err, "subagent: empty parent session id")

	_, err = svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "user-a",
		ParentSessionID: "session-a",
	})
	require.ErrorContains(t, err, "subagent: empty task")

	_, err = svc.GetForUser("user-a", "missing")
	require.ErrorIs(t, err, openclawsubagent.ErrRunNotFound)
	_, _, err = svc.CancelForUser("user-a", "missing")
	require.ErrorIs(t, err, openclawsubagent.ErrRunNotFound)
}

func TestServiceFailureNotification(t *testing.T) {
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

	final, err := svc.WaitForUser(context.Background(), "user-a", run.ID)
	require.NoError(t, err)
	require.Equal(t, openclawsubagent.StatusFailed, final.Status)
	require.Contains(t, final.Error, "runner boom")

	require.Eventually(t, func() bool {
		_, text := sender.snapshot()
		return strings.Contains(text, notificationPrefixFailed)
	}, time.Second, 10*time.Millisecond)
}

func TestFormatNotification(t *testing.T) {
	t.Parallel()

	run := coretaskrun.Run{
		ID:      "run-1",
		Status:  coretaskrun.StatusCompleted,
		Result:  "full result",
		Summary: "summary only",
	}
	require.Contains(t, formatNotification(run), notificationPrefixCompleted)
	require.Contains(t, formatNotification(run), "full result")
	require.NotContains(t, formatNotification(run), "summary only")

	run.Status = coretaskrun.StatusFailed
	run.Summary = "boom"
	require.Contains(t, formatNotification(run), notificationPrefixFailed)

	run.Status = coretaskrun.StatusCanceled
	require.Empty(t, formatNotification(run))
}
