//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestExecutionObserver_NonCooperativeTool(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	svc := inmemory.NewSessionService()
	defer svc.Close()
	tl := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { close(entered); <-release; return "done", nil }, function.WithName("a"))
	r := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(executionTestModel("a")), llmagent.WithTools([]tool.Tool{tl})), WithSessionService(svc), WithToolExecutionTracking(true))
	defer r.Close()
	observer := r.(ExecutionObserver)
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("run"), agent.WithRequestID("non-cooperative"))
	require.NoError(t, err)
	drained := make(chan struct{})
	go func() {
		for range ch {
		}
		close(drained)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("tool did not start")
	}
	cancelled := make(chan bool, 1)
	go func() { cancelled <- r.(ManagedRunner).Cancel("non-cooperative") }()
	select {
	case ok := <-cancelled:
		require.True(t, ok)
	case <-time.After(time.Second):
		t.Fatal("Cancel waited for non-cooperative work")
	}
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("output did not close")
	}
	status, ok := observer.ExecutionStatus("non-cooperative")
	require.True(t, ok)
	require.True(t, status.OutputClosed)
	require.True(t, status.CancellationRequested)
	require.False(t, status.ExecutionSettled)
	require.Equal(t, 1, status.ActiveTools)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	status, err = observer.WaitExecution(ctx, "non-cooperative")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, status.ExecutionSettled)
	close(release)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	status, err = observer.WaitExecution(waitCtx, "non-cooperative")
	require.NoError(t, err)
	require.True(t, status.ExecutionSettled)
	require.Zero(t, status.ActiveTools)
	require.Empty(t, status.PersistenceError)
	_, err = observer.WaitExecution(waitCtx, "non-cooperative")
	require.NoError(t, err)
	_, err = observer.WaitExecution(waitCtx, "missing")
	require.ErrorIs(t, err, ErrExecutionNotFound)
}

func TestExecutionObserver_RetentionAndCapacity(t *testing.T) {
	r := &runner{toolExecutionTracking: true}
	for i := 0; i < executionCapacity; i++ {
		_, err := r.reserveExecution(fmt.Sprint(i))
		require.NoError(t, err)
	}
	_, err := r.reserveExecution("overflow")
	require.ErrorContains(t, err, "capacity")
	_, err = r.reserveExecution("0")
	require.ErrorContains(t, err, "retained")
	old := r.observations["0"]
	old.complete(true, true, false)
	_, err = r.reserveExecution("replacement")
	require.NoError(t, err)
	_, ok := r.ExecutionStatus("0")
	require.False(t, ok)
	expiring := r.observations["1"]
	expiring.complete(true, true, false)
	expiring.mu.Lock()
	expiring.completedAt = time.Now().Add(-executionRetention)
	expiring.mu.Unlock()
	_, ok = r.ExecutionStatus("1")
	require.False(t, ok)
	_, err = (&runner{}).WaitExecution(context.Background(), "disabled")
	require.ErrorIs(t, err, ErrExecutionNotFound)
}

func TestExecutionObserver_PersistenceUncertaintySurvivesSettlement(t *testing.T) {
	svc := &cancellationResultFailure{Service: inmemory.NewSessionService()}
	defer svc.Close()
	tl := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { return "written", nil }, function.WithName("a"))
	r := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(executionTestModel("a")), llmagent.WithTools([]tool.Tool{tl})), WithSessionService(svc), WithToolExecutionTracking(true))
	defer r.Close()
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("run"), agent.WithRequestID("failed-store"))
	require.NoError(t, err)
	for range ch {
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, err := r.(ExecutionObserver).WaitExecution(ctx, "failed-store")
	require.NoError(t, err)
	require.True(t, status.ExecutionSettled)
	require.Contains(t, status.PersistenceError, "uncertain")
}
