//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package taskrun

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type fakeController struct {
	mu             sync.Mutex
	nextID         int
	spawned        taskrunruntime.SpawnRequest
	waits          int
	waitErr        error
	waitForContext bool
	runs           map[string]taskrunruntime.Run
}

func newFakeController() *fakeController {
	return &fakeController{
		runs: make(map[string]taskrunruntime.Run),
	}
}

func (c *fakeController) Spawn(
	ctx context.Context,
	req taskrunruntime.SpawnRequest,
) (taskrunruntime.Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextID++
	id := fmt.Sprintf("run-%d", c.nextID)
	run := taskrunruntime.Run{
		ID:              id,
		OwnerUserID:     req.OwnerUserID,
		ParentSessionID: req.ParentSessionID,
		AgentName:       req.AgentName,
		Task:            req.Task,
		Status:          taskrunruntime.StatusQueued,
	}
	c.spawned = req
	c.runs[id] = run
	return run, nil
}

func (c *fakeController) List(
	ctx context.Context,
	filter taskrunruntime.ListFilter,
) ([]taskrunruntime.Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	runs := make([]taskrunruntime.Run, 0, len(c.runs))
	for _, run := range c.runs {
		if filter.OwnerUserID != "" && run.OwnerUserID != filter.OwnerUserID {
			continue
		}
		if filter.ParentSessionID != "" &&
			run.ParentSessionID != filter.ParentSessionID {
			continue
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (c *fakeController) Get(
	ctx context.Context,
	runID string,
) (*taskrunruntime.Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	run, ok := c.runs[runID]
	if !ok {
		return nil, taskrunruntime.ErrRunNotFound
	}
	return &run, nil
}

func (c *fakeController) Cancel(
	ctx context.Context,
	runID string,
) (*taskrunruntime.Run, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	run, ok := c.runs[runID]
	if !ok {
		return nil, false, taskrunruntime.ErrRunNotFound
	}
	run.Status = taskrunruntime.StatusCanceled
	c.runs[runID] = run
	return &run, true, nil
}

func (c *fakeController) Wait(
	ctx context.Context,
	runID string,
) (*taskrunruntime.Run, error) {
	c.mu.Lock()
	c.waits++
	run, ok := c.runs[runID]
	if !ok {
		c.mu.Unlock()
		return nil, taskrunruntime.ErrRunNotFound
	}
	if c.waitErr != nil {
		err := c.waitErr
		c.mu.Unlock()
		return nil, err
	}
	if c.waitForContext {
		c.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	run.Status = taskrunruntime.StatusCompleted
	run.Result = "done"
	c.runs[runID] = run
	c.mu.Unlock()
	return &run, nil
}

func TestToolsSpawnListGetCancelWait(t *testing.T) {
	t.Parallel()

	controller := newFakeController()
	tools := NewTools(
		controller,
		WithDefaultAgentName("worker"),
		WithRuntimeState(map[string]any{"trace_id": "trace-1"}),
		WithInjectedContextMessages([]model.Message{
			model.NewSystemMessage("extra context"),
		}),
	)
	ctx := newInvocationContext("user-a", "session-a", nil)

	spawnedAny, err := tools.spawn.Call(
		ctx,
		[]byte(`{"task":"review","timeout_seconds":5}`),
	)
	require.NoError(t, err)
	spawned := spawnedAny.(taskrunruntime.Run)
	require.Equal(t, "session-a", spawned.ParentSessionID)
	require.Equal(t, taskrunruntime.StatusQueued, spawned.Status)

	controller.mu.Lock()
	require.Equal(t, "worker", controller.spawned.AgentName)
	require.Equal(t, "trace-1", controller.spawned.RuntimeState["trace_id"])
	require.Equal(t, "review", controller.spawned.Task)
	require.NotZero(t, controller.spawned.Timeout)
	require.Len(t, controller.spawned.InjectedContextMessages, 1)
	require.Equal(
		t,
		"extra context",
		controller.spawned.InjectedContextMessages[0].Content,
	)
	require.Zero(t, controller.waits)
	controller.mu.Unlock()

	listedAny, err := tools.list.Call(ctx, []byte(`{"ignored":true}`))
	require.NoError(t, err)
	listed := listedAny.(listResult)
	require.Len(t, listed.Runs, 1)
	require.Equal(t, spawned.ID, listed.Runs[0].ID)

	getArgs := []byte(fmt.Sprintf(`{"id":%q}`, spawned.ID))
	gotAny, err := tools.get.Call(ctx, getArgs)
	require.NoError(t, err)
	got := gotAny.(*taskrunruntime.Run)
	require.Equal(t, spawned.ID, got.ID)

	waitArgs := []byte(fmt.Sprintf(
		`{"id":%q,"timeout_seconds":1}`,
		spawned.ID,
	))
	waitedAny, err := tools.wait.Call(ctx, waitArgs)
	require.NoError(t, err)
	waited := waitedAny.(*taskrunruntime.Run)
	require.Equal(t, taskrunruntime.StatusCompleted, waited.Status)

	canceledAny, err := tools.cancel.Call(ctx, getArgs)
	require.NoError(t, err)
	canceled := canceledAny.(*taskrunruntime.Run)
	require.Equal(t, taskrunruntime.StatusCanceled, canceled.Status)
}

func TestSpawnToolSyncModeWaits(t *testing.T) {
	t.Parallel()

	controller := newFakeController()
	tools := NewTools(controller)
	ctx := newInvocationContext("user-a", "session-a", nil)

	spawnedAny, err := tools.spawn.Call(
		ctx,
		[]byte(
			`{"task":"review","mode":"sync","wait_timeout_seconds":1}`,
		),
	)
	require.NoError(t, err)
	spawned := spawnedAny.(*taskrunruntime.Run)
	require.Equal(t, taskrunruntime.StatusCompleted, spawned.Status)
	require.Equal(t, "done", spawned.Result)

	controller.mu.Lock()
	defer controller.mu.Unlock()
	require.Equal(t, 1, controller.waits)
}

func TestSpawnToolSyncWaitTimeoutReturnsLatestRun(t *testing.T) {
	t.Parallel()

	controller := newFakeController()
	controller.waitForContext = true
	tools := NewTools(controller)
	ctx := newInvocationContext("user-a", "session-a", nil)

	spawnedAny, err := tools.spawn.Call(
		ctx,
		[]byte(
			`{"task":"review","mode":"sync","wait_timeout_seconds":1}`,
		),
	)
	require.NoError(t, err)
	spawned := spawnedAny.(*taskrunruntime.Run)
	require.Equal(t, taskrunruntime.StatusQueued, spawned.Status)

	controller.mu.Lock()
	defer controller.mu.Unlock()
	require.Equal(t, 1, controller.waits)
}

func TestSpawnToolSyncWaitDeadlineErrors(t *testing.T) {
	t.Parallel()

	controller := newFakeController()
	controller.waitErr = context.DeadlineExceeded
	tools := NewTools(controller)
	ctx := newInvocationContext("user-a", "session-a", nil)

	_, err := tools.spawn.Call(
		ctx,
		[]byte(
			`{"task":"review","mode":"sync","wait_timeout_seconds":1}`,
		),
	)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	controller = newFakeController()
	controller.waitForContext = true
	tools = NewTools(controller)
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = tools.spawn.Call(
		ctx,
		[]byte(
			`{"task":"review","mode":"sync","wait_timeout_seconds":1}`,
		),
	)
	require.ErrorIs(t, err, context.Canceled)
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

func TestToolDeclarations(t *testing.T) {
	t.Parallel()

	tools := NewTools(newFakeController())
	require.Equal(t, toolSpawn, tools.spawn.Declaration().Name)
	require.Equal(t, []string{argTask},
		tools.spawn.Declaration().InputSchema.Required)
	require.Contains(t,
		tools.spawn.Declaration().InputSchema.Properties,
		argTimeoutSeconds,
	)
	require.Contains(t,
		tools.spawn.Declaration().InputSchema.Properties,
		argMode,
	)
	require.Contains(t,
		tools.spawn.Declaration().InputSchema.Properties,
		argWaitSeconds,
	)
	require.Equal(t, toolList, tools.list.Declaration().Name)
	require.Equal(t, schemaTypeObject,
		tools.list.Declaration().InputSchema.Type)
	require.Equal(t, toolGet, tools.get.Declaration().Name)
	require.Equal(t, []string{argID},
		tools.get.Declaration().InputSchema.Required)
	require.Equal(t, toolCancel, tools.cancel.Declaration().Name)
	require.Equal(t, toolWait, tools.wait.Declaration().Name)
	require.Contains(t,
		tools.wait.Declaration().InputSchema.Properties,
		argTimeoutSeconds,
	)

	var nilTools *Tools
	require.Nil(t, nilTools.All())

	var empty Tools
	empty.SetController(newFakeController())
	require.NotNil(t, empty.state)
	require.NotNil(t, empty.state.controller)
}

func TestToolsRejectNestedSpawnByDefault(t *testing.T) {
	t.Parallel()

	tools := NewTools(newFakeController())
	ctx := newInvocationContext(
		"user-a",
		"session-a",
		map[string]any{taskrunruntime.RuntimeStateKeyRun: true},
	)

	_, err := tools.spawn.Call(ctx, []byte(`{"task":"nested"}`))
	require.ErrorContains(t, err, "nested task runs are not supported")

	tools = NewTools(newFakeController(), WithNestedSpawns(true))
	_, err = tools.spawn.Call(ctx, []byte(`{"task":"nested"}`))
	require.NoError(t, err)
}

func TestToolsRequireContextAndController(t *testing.T) {
	t.Parallel()

	tools := NewTools(nil)
	require.Len(t, tools.All(), 5)
	tools.SetController(nil)

	ctx := newInvocationContext("user-a", "session-a", nil)
	_, err := tools.spawn.Call(ctx, []byte(`{"task":"demo"}`))
	require.ErrorContains(t, err, "controller unavailable")

	tools.SetController(newFakeController())
	_, err = tools.spawn.Call(context.Background(), []byte(`{"task":"demo"}`))
	require.ErrorContains(t, err, "current session context is unavailable")

	_, err = tools.spawn.Call(ctx, []byte(`{invalid`))
	require.Error(t, err)

	_, err = tools.spawn.Call(ctx, []byte(`{"task":"demo","mode":"bad"}`))
	require.ErrorContains(t, err, "unsupported mode")

	_, err = tools.list.Call(ctx, []byte(`{invalid`))
	require.Error(t, err)

	_, err = tools.get.Call(ctx, []byte(`{"id":" "}`))
	require.ErrorContains(t, err, "empty run id")

	_, err = tools.cancel.Call(ctx, []byte(`{"id":"missing"}`))
	require.ErrorIs(t, err, taskrunruntime.ErrRunNotFound)
}

func TestToolsRejectCrossOwnerAccess(t *testing.T) {
	t.Parallel()

	controller := newFakeController()
	tools := NewTools(controller)
	ctx := newInvocationContext("user-a", "session-a", nil)
	spawnedAny, err := tools.spawn.Call(ctx, []byte(`{"task":"review"}`))
	require.NoError(t, err)
	spawned := spawnedAny.(taskrunruntime.Run)

	otherCtx := newInvocationContext("user-b", "session-a", nil)
	args := []byte(fmt.Sprintf(`{"id":%q}`, spawned.ID))
	_, err = tools.get.Call(otherCtx, args)
	require.ErrorIs(t, err, taskrunruntime.ErrRunNotFound)

	_, err = tools.cancel.Call(otherCtx, args)
	require.ErrorIs(t, err, taskrunruntime.ErrRunNotFound)

	_, err = tools.wait.Call(otherCtx, args)
	require.ErrorIs(t, err, taskrunruntime.ErrRunNotFound)
}

func TestCurrentContextRequiresUserAndSession(t *testing.T) {
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
