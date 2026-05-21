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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	openclawsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const testParentAgentName = "parent"

func TestToolsSpawnListGetCancelWait(t *testing.T) {
	t.Parallel()

	runner := &blockingRunner{started: make(chan struct{})}
	svc, err := NewService(t.TempDir(), runner, nil)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	tools := NewTools(svc)
	ctx := newInvocationContext(
		"telegram:user",
		"telegram:dm:321",
		map[string]any{
			"openclaw.delivery.channel": "telegram",
			"openclaw.delivery.target":  "321",
		},
	)

	spawnedAny, err := tools.spawn.Call(
		ctx,
		[]byte(`{"task":"review the patch","timeout_seconds":5}`),
	)
	require.NoError(t, err)

	spawned := spawnedAny.(openclawsubagent.Run)
	require.Equal(t, "telegram:dm:321", spawned.ParentSessionID)
	require.Equal(t, openclawsubagent.StatusQueued, spawned.Status)

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("subagent run did not start in time")
	}

	listedAny, err := tools.list.Call(ctx, nil)
	require.NoError(t, err)
	listed := listedAny.(listResult)
	require.Len(t, listed.Runs, 1)
	require.Equal(t, spawned.ID, listed.Runs[0].ID)

	getArgs := []byte(fmt.Sprintf(`{"id":%q}`, spawned.ID))
	gotAny, err := tools.get.Call(ctx, getArgs)
	require.NoError(t, err)
	got := gotAny.(*openclawsubagent.Run)
	require.Equal(t, spawned.ID, got.ID)

	canceledAny, err := tools.cancel.Call(ctx, getArgs)
	require.NoError(t, err)
	canceled := canceledAny.(*openclawsubagent.Run)
	require.Equal(t, openclawsubagent.StatusCanceled, canceled.Status)

	waitedAny, err := tools.wait.Call(ctx, getArgs)
	require.NoError(t, err)
	waited := waitedAny.(*openclawsubagent.Run)
	require.Equal(t, openclawsubagent.StatusCanceled, waited.Status)
}

func TestSpawnToolSyncAndReviewModesWait(t *testing.T) {
	t.Parallel()

	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	runner := &captureRunner{reply: "review result"}
	svc, err := NewService(t.TempDir(), runner, router)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	tools := NewTools(svc)
	ctx := newInvocationContext(
		"telegram:user",
		"telegram:dm:321",
		map[string]any{
			"openclaw.delivery.channel": "telegram",
			"openclaw.delivery.target":  "321",
		},
	)

	syncedAny, err := tools.spawn.Call(
		ctx,
		[]byte(`{"task":"review","mode":"sync"}`),
	)
	require.NoError(t, err)
	synced := syncedAny.(*openclawsubagent.Run)
	require.Equal(t, openclawsubagent.StatusCompleted, synced.Status)
	require.Equal(t, "review result", synced.Result)

	reviewInv := newInvocation(
		"telegram:user",
		"telegram:dm:654",
		map[string]any{
			"openclaw.delivery.channel": "telegram",
			"openclaw.delivery.target":  "654",
		},
	)
	reviewCtx := agent.NewInvocationContext(
		context.Background(),
		reviewInv,
	)
	reviewedAny, err := tools.spawn.Call(
		reviewCtx,
		[]byte(`{"task":"review","mode":"review"}`),
	)
	require.NoError(t, err)
	reviewed := reviewedAny.(*openclawsubagent.Run)
	require.Equal(t, openclawsubagent.StatusCompleted, reviewed.Status)

	_, text := sender.snapshot()
	require.Empty(t, text)

	route, ok := agent.CurrentAwaitUserReplyRoute(reviewInv)
	require.True(t, ok)
	require.Equal(t, testParentAgentName, route.AgentName)
}

func TestSpawnToolSyncWaitTimeoutReturnsLatestRun(t *testing.T) {
	t.Parallel()

	runner := &blockingRunner{started: make(chan struct{})}
	svc, err := NewService(t.TempDir(), runner, nil)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	tools := NewTools(svc)
	ctx := newInvocationContext(
		"telegram:user",
		"telegram:dm:321",
		map[string]any{
			"openclaw.delivery.channel": "telegram",
			"openclaw.delivery.target":  "321",
		},
	)

	spawnedAny, err := tools.spawn.Call(
		ctx,
		[]byte(
			`{"task":"review","mode":"sync","wait_timeout_seconds":1}`,
		),
	)
	require.NoError(t, err)
	spawned := spawnedAny.(*openclawsubagent.Run)
	require.Contains(t, []openclawsubagent.Status{
		openclawsubagent.StatusQueued,
		openclawsubagent.StatusRunning,
	}, spawned.Status)
}

func TestWaitTimedOutRequiresWaitContextDeadline(t *testing.T) {
	t.Parallel()

	parent := context.Background()
	waitCtx, cancel := context.WithTimeout(parent, 0)
	defer cancel()
	<-waitCtx.Done()
	require.True(t, waitTimedOut(
		parent,
		waitCtx,
		context.DeadlineExceeded,
		1,
	))

	require.False(t, waitTimedOut(
		parent,
		parent,
		context.DeadlineExceeded,
		1,
	))

	canceledParent, cancelParent := context.WithCancel(parent)
	cancelParent()
	require.False(t, waitTimedOut(
		canceledParent,
		canceledParent,
		context.Canceled,
		1,
	))
}

func TestSpawnToolRejectsNestedSubagent(t *testing.T) {
	t.Parallel()

	runner := &captureRunner{reply: "ok"}
	svc, err := NewService(t.TempDir(), runner, nil)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	tools := NewTools(svc)
	ctx := newInvocationContext(
		"user-a",
		"session-a",
		map[string]any{openclawsubagent.RuntimeStateKeyRun: true},
	)

	_, err = tools.spawn.Call(ctx, []byte(`{"task":"nested"}`))
	require.ErrorContains(
		t,
		err,
		"nested subagent spawn is not supported",
	)
}

func TestToolsAllAndDeclarations(t *testing.T) {
	t.Parallel()

	tools := NewTools(nil)
	all := tools.All()
	require.Len(t, all, 9)

	svc := &Service{}
	tools.SetService(svc)
	for _, item := range all {
		require.NotNil(t, item.Declaration())
	}
	require.NotNil(t, tools.wait.Declaration())
	require.Contains(
		t,
		tools.spawn.Declaration().InputSchema.Properties,
		argMode,
	)
	require.Contains(
		t,
		tools.spawn.Declaration().InputSchema.Properties,
		argWaitSeconds,
	)
	require.Contains(
		t,
		tools.spawnAlias.Declaration().Description,
		toolSubagentsSpawn,
	)
}

func TestToolErrorPaths(t *testing.T) {
	t.Parallel()

	var nilTools *Tools
	require.Nil(t, nilTools.All())
	nilTools.SetService(nil)

	tools := NewTools(nil)
	ctx := newInvocationContext("user-a", "session-a", nil)

	_, err := tools.spawn.Call(ctx, []byte(`{"task":"demo"}`))
	require.ErrorContains(t, err, "service unavailable")

	_, err = tools.list.Call(ctx, nil)
	require.ErrorContains(t, err, "service unavailable")

	svc, err := NewService(t.TempDir(), &captureRunner{reply: "ok"}, nil)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})
	tools.SetService(svc)

	_, err = tools.get.Call(ctx, []byte(`{invalid`))
	require.Error(t, err)

	_, err = tools.cancel.Call(ctx, []byte(`{invalid`))
	require.Error(t, err)

	_, err = tools.wait.Call(ctx, []byte(`{invalid`))
	require.Error(t, err)

	_, err = tools.spawn.Call(ctx, []byte(`{invalid`))
	require.Error(t, err)

	_, err = tools.spawn.Call(ctx, []byte(`{"task":"demo","mode":"bad"}`))
	require.ErrorContains(t, err, "unsupported mode")

	_, err = tools.spawn.Call(context.Background(), []byte(`{"task":"demo"}`))
	require.ErrorContains(t, err, "current session context is unavailable")

	_, err = tools.list.Call(context.Background(), nil)
	require.ErrorContains(t, err, "current session context is unavailable")

	_, err = tools.get.Call(ctx, []byte(`{"id":" "}`))
	require.ErrorContains(t, err, "empty run id")

	_, err = tools.cancel.Call(ctx, []byte(`{"id":"missing"}`))
	require.ErrorIs(t, err, openclawsubagent.ErrRunNotFound)

	badCtx := newInvocationContext("user-a", "session-a", nil)
	_, err = tools.spawn.Call(badCtx, []byte(`{"task":"demo"}`))
	require.ErrorContains(t, err, "resolve delivery target")

	_, err = tools.list.Call(ctx, []byte(`{invalid`))
	require.Error(t, err)
}

func TestCurrentContextRequiresUserAndSessionFields(t *testing.T) {
	t.Parallel()

	ctx := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationSession(
				session.NewSession("app", "", "session-a"),
			),
		),
	)
	_, _, err := currentContext(ctx)
	require.ErrorContains(t, err, "current user id is unavailable")

	ctx = agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationSession(
				session.NewSession("app", "user-a", ""),
			),
		),
	)
	_, _, err = currentContext(ctx)
	require.ErrorContains(t, err, "current session id is unavailable")
}

func newInvocationContext(
	userID string,
	sessionID string,
	runtimeState map[string]any,
) context.Context {
	inv := newInvocation(userID, sessionID, runtimeState)
	return agent.NewInvocationContext(context.Background(), inv)
}

func newInvocation(
	userID string,
	sessionID string,
	runtimeState map[string]any,
) *agent.Invocation {
	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			session.NewSession("app", userID, sessionID),
		),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RuntimeState: runtimeState,
		}),
	)
	inv.AgentName = testParentAgentName
	return inv
}
