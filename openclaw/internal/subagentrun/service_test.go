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
	"trpc.group/trpc-go/trpc-agent-go/internal/gitworktree"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
	openclawsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
)

const (
	testStoreDirPerm   = 0o700
	testStoreFilePerm  = 0o600
	testProfileID      = "reviewer"
	testProfilePrompt  = "profile instruction"
	testProfileState   = "profile_state"
	testProfileSystem  = "profile system prompt"
	testProfileRoot    = "/tmp/openclaw-profile-root"
	testProfileUserID  = "telegram:user"
	testFinalizePath   = "/tmp/subagent-worktree"
	testFinalizeBranch = "feature/kept-branch"
	testSubagentRunID  = "subagent:test-run"
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

type fakeWorktreeManager struct {
	mu             sync.Mutex
	createReq      gitworktree.CreateRequest
	finalizeLease  gitworktree.Lease
	finalizeCtxErr error
	lease          gitworktree.Lease
	finalizeResult gitworktree.FinalizeResult
	createErr      error
	finalizeErr    error
}

func (m *fakeWorktreeManager) Create(
	_ context.Context,
	req gitworktree.CreateRequest,
) (gitworktree.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createReq = req
	if m.createErr != nil {
		return gitworktree.Lease{}, m.createErr
	}
	lease := m.lease
	lease.ID = req.ID
	return lease, nil
}

func (m *fakeWorktreeManager) Finalize(
	ctx context.Context,
	lease gitworktree.Lease,
) (gitworktree.FinalizeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalizeLease = lease
	if ctx != nil {
		m.finalizeCtxErr = ctx.Err()
	}
	result := m.finalizeResult
	if result.Path == "" {
		result.Path = lease.Path
	}
	if result.Branch == "" {
		result.Branch = lease.Branch
	}
	return result, m.finalizeErr
}

func (m *fakeWorktreeManager) snapshot() (
	gitworktree.CreateRequest,
	gitworktree.Lease,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createReq, m.finalizeLease
}

func (m *fakeWorktreeManager) finalizeContextErr() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.finalizeCtxErr
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

func requireRunnerStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("subagent run did not start in time")
	}
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

func TestServiceSpawnWithWorktreeIsolation(t *testing.T) {
	t.Parallel()

	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	lease := gitworktree.Lease{
		Path:       "/tmp/openclaw-worktree",
		Branch:     "openclaw-worktree-subagent-test",
		RepoRoot:   testProfileRoot,
		BaseCommit: "abc123",
	}
	worktrees := &fakeWorktreeManager{
		lease: lease,
		finalizeResult: gitworktree.FinalizeResult{
			Preserved:  true,
			HasChanges: true,
			Reason:     "status",
		},
	}
	runner := &captureRunner{reply: "changed files"}
	svc, err := NewService(
		t.TempDir(),
		runner,
		router,
		WithWorktreeManager(worktrees),
	)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	spawnCtx := runtimeprofile.WithProfile(
		context.Background(),
		runtimeprofile.Profile{
			Workspace: runtimeprofile.WorkspacePolicy{
				Workdir:      testProfileRoot,
				AllowedRoots: []string{testProfileRoot},
			},
		},
	)
	run, err := svc.Spawn(spawnCtx, SpawnRequest{
		OwnerUserID:     "telegram:user",
		ParentSessionID: "telegram:dm:100",
		Task:            "edit in isolation",
		Isolation:       isolationWorktree,
		Delivery: deliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, run.Workspace)
	require.Equal(t, isolationWorktree, run.Workspace.Isolation)
	require.Equal(t, lease.Path, run.Workspace.Path)

	final, err := svc.WaitForUser(
		context.Background(),
		"telegram:user",
		run.ID,
	)
	require.NoError(t, err)
	require.Equal(t, openclawsubagent.StatusCompleted, final.Status)
	require.NotNil(t, final.Workspace)
	require.Equal(t, worktreeCleanupPreserved, final.Workspace.Cleanup)

	createReq, finalizeLease := worktrees.snapshot()
	require.Equal(t, testProfileRoot, createReq.Workdir)
	require.Equal(t, run.ID, createReq.ID)
	require.Equal(t, run.ID, finalizeLease.ID)
	require.Equal(t, lease.Path, finalizeLease.Path)

	runner.mu.Lock()
	workspace, ok := runtimeprofile.WorkspaceFromContext(runner.ctx)
	require.True(t, ok)
	require.Equal(t, lease.Path, workspace.Workdir)
	require.Equal(t, []string{lease.Path}, workspace.AllowedRoots)
	require.Equal(
		t,
		lease.Path,
		runner.runOpts.RuntimeState[runtimeprofile.RuntimeStateWorkspaceWorkdir],
	)
	require.Len(t, runner.runOpts.InjectedContextMessages, 2)
	require.Contains(
		t,
		runner.runOpts.InjectedContextMessages[1].Content,
		lease.Path,
	)
	runner.mu.Unlock()

	require.Eventually(t, func() bool {
		_, text := sender.snapshot()
		return strings.Contains(text, "Worktree preserved: "+lease.Path)
	}, time.Second, 10*time.Millisecond)
}

func TestServiceWorktreeFinalizeWaitsForChildExit(t *testing.T) {
	t.Parallel()

	lease := gitworktree.Lease{
		Path:       "/tmp/openclaw-worktree-cancel",
		Branch:     "openclaw-worktree-subagent-cancel",
		RepoRoot:   testProfileRoot,
		BaseCommit: "abc123",
	}
	worktrees := &fakeWorktreeManager{
		lease: lease,
		finalizeResult: gitworktree.FinalizeResult{
			Removed: true,
			Reason:  "removed",
		},
	}
	runner := &controlledCancelRunner{
		started:   make(chan struct{}),
		canceling: make(chan struct{}),
		release:   make(chan struct{}),
	}
	svc, err := NewService(
		t.TempDir(),
		runner,
		nil,
		WithWorktreeManager(worktrees),
	)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	spawnCtx := runtimeprofile.WithProfile(
		context.Background(),
		runtimeprofile.Profile{
			Workspace: runtimeprofile.WorkspacePolicy{
				Workdir:      testProfileRoot,
				AllowedRoots: []string{testProfileRoot},
			},
		},
	)
	run, err := svc.Spawn(spawnCtx, SpawnRequest{
		OwnerUserID:     "telegram:user",
		ParentSessionID: "telegram:dm:100",
		Task:            "cancel in isolation",
		Isolation:       isolationWorktree,
	})
	require.NoError(t, err)
	requireRunnerStarted(t, runner.started)

	canceled, changed, err := svc.CancelForUser("telegram:user", run.ID)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, openclawsubagent.StatusCanceling, canceled.Status)
	_, finalizeLease := worktrees.snapshot()
	require.Empty(t, finalizeLease.Path)

	select {
	case <-runner.canceling:
	case <-time.After(time.Second):
		t.Fatal("runner did not observe cancellation")
	}
	runner.releaseOnce.Do(func() {
		close(runner.release)
	})

	final, err := svc.WaitForUser(
		context.Background(),
		"telegram:user",
		run.ID,
	)
	require.NoError(t, err)
	require.Equal(t, openclawsubagent.StatusCanceled, final.Status)
	_, finalizeLease = worktrees.snapshot()
	require.Equal(t, lease.Path, finalizeLease.Path)
}

func TestServiceCleansUnspawnedWorktreeWithUncanceledContext(t *testing.T) {
	t.Parallel()

	lease := gitworktree.Lease{
		Path:       "/tmp/openclaw-worktree-unspawned",
		Branch:     "openclaw-worktree-subagent-unspawned",
		RepoRoot:   testProfileRoot,
		BaseCommit: "abc123",
	}
	worktrees := &fakeWorktreeManager{
		lease: lease,
		finalizeResult: gitworktree.FinalizeResult{
			Removed: true,
			Reason:  "removed",
		},
	}
	svc, err := NewService(
		t.TempDir(),
		&captureRunner{reply: "ok"},
		nil,
		WithWorktreeManager(worktrees),
	)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	ctx, cancel := context.WithCancel(runtimeprofile.WithProfile(
		context.Background(),
		runtimeprofile.Profile{
			Workspace: runtimeprofile.WorkspacePolicy{
				Workdir:      testProfileRoot,
				AllowedRoots: []string{testProfileRoot},
			},
		},
	))
	cancel()
	_, err = svc.Spawn(ctx, SpawnRequest{
		OwnerUserID:     "telegram:user",
		ParentSessionID: "telegram:dm:100",
		Task:            "spawn with canceled context",
		Isolation:       isolationWorktree,
	})
	require.ErrorIs(t, err, context.Canceled)

	_, finalizeLease := worktrees.snapshot()
	require.Equal(t, lease.Path, finalizeLease.Path)
	require.NoError(t, worktrees.finalizeContextErr())
}

func TestServiceWorktreeIsolationRequiresProfileWorkdir(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		t.TempDir(),
		&captureRunner{reply: "ok"},
		nil,
		WithWorktreeManager(&fakeWorktreeManager{}),
	)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	_, err = svc.Spawn(context.Background(), SpawnRequest{
		OwnerUserID:     "telegram:user",
		ParentSessionID: "telegram:dm:100",
		Task:            "edit in isolation",
		Isolation:       isolationWorktree,
	})
	require.ErrorContains(
		t,
		err,
		"worktree isolation requires runtime profile workdir",
	)
}

func TestMetadataForFinalizeResultMarksErrors(t *testing.T) {
	t.Parallel()

	metadata := metadataForFinalizeResult(
		gitworktree.FinalizeResult{
			Removed: true,
			Reason:  "remove_error",
		},
		errors.New("delete branch"),
	)

	require.Equal(t, worktreeCleanupError, metadata[metadataWorktreeCleanup])
	require.Contains(
		t,
		metadata[metadataWorktreeCleanupNote],
		"delete branch",
	)

	metadata = metadataForFinalizeResult(
		gitworktree.FinalizeResult{},
		errors.New("finalize failed"),
	)
	require.Equal(
		t,
		"finalize failed",
		metadata[metadataWorktreeCleanupNote],
	)
}

func TestMetadataForFinalizeResultRecordsCurrentWorktree(t *testing.T) {
	t.Parallel()

	metadata := metadataForFinalizeResult(
		gitworktree.FinalizeResult{
			Path:      testFinalizePath,
			Branch:    testFinalizeBranch,
			Preserved: true,
		},
		nil,
	)

	require.Equal(t, worktreeCleanupPreserved, metadata[metadataWorktreeCleanup])
	require.Equal(t, testFinalizePath, metadata[metadataWorktreePath])
	require.Equal(t, testFinalizeBranch, metadata[metadataWorktreeBranch])
}

func TestServiceCreateWorktreeUnavailable(t *testing.T) {
	t.Parallel()

	var nilService *Service
	_, err := nilService.createWorktree(context.Background(), testSubagentRunID)
	require.ErrorContains(t, err, "worktree isolation unavailable")

	svc := &Service{}
	_, err = svc.createWorktree(context.Background(), testSubagentRunID)
	require.ErrorContains(t, err, "worktree isolation unavailable")
}

func TestServiceCleanupUnspawnedWorktreeBranches(t *testing.T) {
	t.Parallel()

	lease := gitworktree.Lease{
		Path:       "/tmp/openclaw-worktree-unspawned-direct",
		Branch:     "openclaw-worktree-subagent-unspawned-direct",
		RepoRoot:   testProfileRoot,
		BaseCommit: "abc123",
	}

	var nilService *Service
	nilService.cleanupUnspawnedWorktree(context.Background(), &lease)

	svc := &Service{}
	svc.cleanupUnspawnedWorktree(context.Background(), nil)

	worktrees := &fakeWorktreeManager{
		finalizeErr: errors.New("cleanup failed"),
	}
	svc = &Service{worktrees: worktrees}
	svc.cleanupUnspawnedWorktree(context.Background(), &lease)
	_, finalizeLease := worktrees.snapshot()
	require.Equal(t, lease.Path, finalizeLease.Path)
	require.NoError(t, worktrees.finalizeContextErr())
}

func TestServiceFinalizeWorktreeBranches(t *testing.T) {
	t.Parallel()

	run := coretaskrun.Run{}
	var nilService *Service
	require.Nil(t, nilService.finalizeWorktree(context.Background(), run))

	svc := &Service{}
	require.Nil(t, svc.finalizeWorktree(context.Background(), run))

	svc = &Service{worktrees: &fakeWorktreeManager{}}
	require.Nil(t, svc.finalizeWorktree(context.Background(), run))

	lease := gitworktree.Lease{
		Path:       "/tmp/openclaw-worktree-finalize-error",
		Branch:     "openclaw-worktree-subagent-finalize-error",
		RepoRoot:   testProfileRoot,
		BaseCommit: "abc123",
	}
	svc = &Service{
		worktrees: &fakeWorktreeManager{
			finalizeErr: errors.New("finalize failed"),
		},
	}
	run.ID = testSubagentRunID
	run.Metadata = metadataForWorktreeLease(lease)
	metadata := svc.finalizeWorktree(context.Background(), run)
	require.Equal(t, worktreeCleanupError, metadata[metadataWorktreeCleanup])
	require.Contains(t, metadata[metadataWorktreeCleanupNote], "finalize failed")
}

func TestRunContextFromContextWithLeaseOnly(t *testing.T) {
	t.Parallel()

	lease := &gitworktree.Lease{
		Path: "/tmp/openclaw-worktree-profile-only",
	}
	wrap := runContextFromContext(context.Background(), lease)
	require.NotNil(t, wrap)

	ctx := wrap(nil)
	workspace, ok := runtimeprofile.WorkspaceFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, lease.Path, workspace.Workdir)
	require.Equal(t, []string{lease.Path}, workspace.AllowedRoots)
}

func TestWorktreeSourceWorkdirPropagatesProfileError(t *testing.T) {
	t.Parallel()

	ctx := runtimeprofile.WithProfile(
		context.Background(),
		runtimeprofile.Profile{
			Workspace: runtimeprofile.WorkspacePolicy{
				AllowedRoots: []string{testProfileRoot},
			},
		},
	)
	_, err := worktreeSourceWorkdir(ctx)
	require.ErrorIs(t, err, runtimeprofile.ErrWorkspaceDenied)
}

func TestWorktreeNotificationDetailBranches(t *testing.T) {
	t.Parallel()

	const (
		testWorktreePath   = "/tmp/openclaw-worktree-notification"
		testWorktreeBranch = "openclaw-worktree-subagent-notification"
	)

	tests := []struct {
		name     string
		metadata map[string]string
		want     string
	}{
		{
			name: "shared isolation",
			metadata: map[string]string{
				metadataIsolation: "shared",
			},
		},
		{
			name: "removed cleanup",
			metadata: map[string]string{
				metadataIsolation:       isolationWorktree,
				metadataWorktreeCleanup: worktreeCleanupRemoved,
				metadataWorktreePath:    testWorktreePath,
			},
		},
		{
			name: "preserved without path",
			metadata: map[string]string{
				metadataIsolation:       isolationWorktree,
				metadataWorktreeCleanup: worktreeCleanupPreserved,
			},
		},
		{
			name: "error without branch",
			metadata: map[string]string{
				metadataIsolation:       isolationWorktree,
				metadataWorktreeCleanup: worktreeCleanupError,
				metadataWorktreePath:    testWorktreePath,
			},
			want: "Worktree cleanup failed: " + testWorktreePath,
		},
		{
			name: "preserved without branch",
			metadata: map[string]string{
				metadataIsolation:       isolationWorktree,
				metadataWorktreeCleanup: worktreeCleanupPreserved,
				metadataWorktreePath:    testWorktreePath,
			},
			want: "Worktree preserved: " + testWorktreePath,
		},
		{
			name: "error with branch",
			metadata: map[string]string{
				metadataIsolation:       isolationWorktree,
				metadataWorktreeCleanup: worktreeCleanupError,
				metadataWorktreePath:    testWorktreePath,
				metadataWorktreeBranch:  testWorktreeBranch,
			},
			want: "Worktree cleanup failed: " + testWorktreePath +
				" (branch " + testWorktreeBranch + ")",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(
				t,
				tt.want,
				worktreeNotificationDetail(coretaskrun.Run{
					Metadata: tt.metadata,
				}),
			)
		})
	}
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
	require.Equal(t, openclawsubagent.StatusCanceling, canceled.Status)

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
