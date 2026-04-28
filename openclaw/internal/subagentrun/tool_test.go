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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	publicsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestToolsSpawnListGetCancel(t *testing.T) {
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
		nil,
	)

	spawnedAny, err := tools.spawn.Call(
		ctx,
		[]byte(`{"task":"review the patch","timeout_seconds":5}`),
	)
	require.NoError(t, err)

	spawned := spawnedAny.(publicsubagent.Run)
	require.Equal(t, "telegram:dm:321", spawned.ParentSessionID)
	require.Equal(t, publicsubagent.StatusQueued, spawned.Status)

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

	getArgs := []byte(`{"id":"` + spawned.ID + `"}`)
	gotAny, err := tools.get.Call(ctx, getArgs)
	require.NoError(t, err)
	got := gotAny.(*publicsubagent.Run)
	require.Equal(t, spawned.ID, got.ID)

	canceledAny, err := tools.cancel.Call(ctx, getArgs)
	require.NoError(t, err)
	canceled := canceledAny.(*publicsubagent.Run)
	require.Equal(t, publicsubagent.StatusCanceled, canceled.Status)

	listedAny, err = tools.listAlias.Call(ctx, []byte(`{}`))
	require.NoError(t, err)
	listed = listedAny.(listResult)
	require.Len(t, listed.Runs, 1)
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
		map[string]any{runtimeStateSubagentRun: true},
	)

	_, err = tools.spawn.Call(ctx, []byte(`{"task":"nested"}`))
	require.ErrorContains(
		t,
		err,
		"nested subagent spawn is not supported",
	)
}

func TestCurrentContextRequiresInvocation(t *testing.T) {
	t.Parallel()

	_, _, err := currentContext(context.Background())
	require.ErrorContains(t, err, "current session context is unavailable")
}

func TestToolsAllAndDeclarations(t *testing.T) {
	t.Parallel()

	tools := NewTools(nil)
	all := tools.All()
	require.Len(t, all, 8)

	svc := &Service{}
	tools.SetService(svc)
	for _, item := range all {
		require.NotNil(t, item.Declaration())
	}
	require.Contains(
		t,
		tools.spawnAlias.Declaration().Description,
		toolSubagentsSpawn,
	)
	require.Contains(
		t,
		tools.listAlias.Declaration().Description,
		toolSubagentsList,
	)
	require.Contains(
		t,
		tools.getAlias.Declaration().Description,
		toolSubagentsGet,
	)
	require.Contains(
		t,
		tools.cancelAlias.Declaration().Description,
		toolSubagentsCancel,
	)
}

func TestDecodeRunIDArgsRequiresID(t *testing.T) {
	t.Parallel()

	ctx := newInvocationContext("user-a", "session-a", nil)
	_, _, err := decodeRunIDArgs(ctx, []byte(`{"id":" "}`))
	require.ErrorContains(t, err, "empty run id")
}

func TestToolsRequireConfiguredService(t *testing.T) {
	t.Parallel()

	tools := NewTools(nil)
	ctx := newInvocationContext("user-a", "session-a", nil)

	_, err := tools.spawn.Call(ctx, []byte(`{"task":"demo"}`))
	require.ErrorContains(t, err, "service unavailable")

	_, err = tools.list.Call(ctx, nil)
	require.ErrorContains(t, err, "service unavailable")
}

func TestListToolRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	svc, err := NewService(t.TempDir(), &captureRunner{reply: "ok"}, nil)
	require.NoError(t, err)
	svc.Start(context.Background())
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	tools := NewTools(svc)
	ctx := newInvocationContext("user-a", "session-a", nil)
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

func TestToolAdditionalErrorPaths(t *testing.T) {
	t.Parallel()

	var nilTools *Tools
	require.Nil(t, nilTools.All())
	nilTools.SetService(nil)

	var nilSpawn *spawnTool
	var nilList *listTool
	var nilGet *getTool
	var nilCancel *cancelTool
	require.Nil(t, nilSpawn.base())
	require.Nil(t, nilList.base())
	require.Nil(t, nilGet.base())
	require.Nil(t, nilCancel.base())

	tools := NewTools(nil)
	ctx := newInvocationContext("user-a", "session-a", nil)

	_, err := tools.get.Call(ctx, []byte(`{"id":"run-1"}`))
	require.ErrorContains(t, err, "service unavailable")

	_, err = tools.cancel.Call(ctx, []byte(`{"id":"run-1"}`))
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

	_, err = tools.spawn.Call(ctx, []byte(`{invalid`))
	require.Error(t, err)

	_, err = tools.spawn.Call(context.Background(), []byte(`{"task":"demo"}`))
	require.ErrorContains(t, err, "current session context is unavailable")

	_, err = tools.list.Call(context.Background(), nil)
	require.ErrorContains(t, err, "current session context is unavailable")

	_, err = tools.get.Call(context.Background(), []byte(`{"id":"run-1"}`))
	require.ErrorContains(t, err, "current session context is unavailable")

	_, err = tools.cancel.Call(ctx, []byte(`{"id":"missing"}`))
	require.ErrorIs(t, err, publicsubagent.ErrRunNotFound)

	badCtx := newInvocationContext("user-a", "session-a", nil)
	_, err = tools.spawn.Call(badCtx, []byte(`{"task":"demo"}`))
	require.ErrorContains(t, err, "resolve delivery target")
}

func newInvocationContext(
	userID string,
	sessionID string,
	runtimeState map[string]any,
) context.Context {
	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			session.NewSession("app", userID, sessionID),
		),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RuntimeState: runtimeState,
		}),
	)
	return agent.NewInvocationContext(context.Background(), inv)
}
