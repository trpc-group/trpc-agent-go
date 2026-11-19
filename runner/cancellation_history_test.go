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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestCancellationHistory_NextPromptKeepsCompletedSibling(t *testing.T) {
	for _, perCall := range []bool{false, true} {
		t.Run(fmt.Sprint(perCall), func(t *testing.T) {
			svc := inmemory.NewSessionService()
			defer svc.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var secondCalls atomic.Int32
			a := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { cancel(); return "first-write-evidence", nil }, function.WithName("a"))
			b := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { secondCalls.Add(1); return "second", nil }, function.WithName("b"))
			m := executionTestModel("a", "b")
			r := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{a, b})), WithSessionService(svc), WithToolExecutionTracking(true))
			defer r.Close()
			ch, err := r.Run(ctx, "u", "s", model.NewUserMessage("write"), agent.WithRequestID("old"), agent.WithToolResultEventPerCallEnabled(perCall))
			require.NoError(t, err)
			for range ch {
			}
			waitCtx, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			_, err = r.(ExecutionObserver).WaitExecution(waitCtx, "old")
			require.NoError(t, err)
			ch, err = r.Run(context.Background(), "u", "s", model.NewUserMessage("report previous results"))
			require.NoError(t, err)
			for range ch {
			}
			requests := m.Requests()
			require.Len(t, requests, 2)
			results := map[string]string{}
			for _, msg := range requests[1].messages {
				if msg.Role == model.RoleTool {
					results[msg.ToolID] = msg.Content
				}
			}
			require.Contains(t, results["0"], "first-write-evidence")
			require.Contains(t, results["1"], "not dispatched")
			require.Zero(t, secondCalls.Load())
			sess, err := svc.GetSession(context.Background(), session.Key{AppName: "execution-test", UserID: "u", SessionID: "s"})
			require.NoError(t, err)
			for _, e := range sess.Events {
				require.False(t, strings.HasPrefix(e.ID, "execution-projection:"))
			}
		})
	}
}

func TestCancellationHistory_InFlightSiblingIsUnknown(t *testing.T) {
	svc := inmemory.NewSessionService()
	defer svc.Close()
	release, entered := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	a := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { return "first-write-evidence", nil }, function.WithName("a"))
	b := function.NewFunctionTool(func(context.Context, struct{}) (string, error) {
		close(entered)
		<-release
		return "late-write-evidence", nil
	}, function.WithName("b"))
	m := executionTestModel("a", "b")
	r := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{a, b}), llmagent.WithEnableParallelTools(true)), WithSessionService(svc), WithToolExecutionTracking(true))
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := r.Run(ctx, "u", "s", model.NewUserMessage("write"), agent.WithRequestID("old"))
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
		t.Fatal("second tool did not start")
	}
	require.Eventually(t, func() bool {
		records := executionRecords(t, svc)
		return len(records) == 1 && records[0].Calls[0].Status == "completed"
	}, time.Second, time.Millisecond)
	cancel()
	<-drained
	next, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("report previous results"))
	require.NoError(t, err)
	for range next {
	}
	requests := m.Requests()
	require.Len(t, requests, 2)
	var sawFirst, sawUnknown bool
	for _, msg := range requests[1].messages {
		if msg.ToolID == "0" {
			sawFirst = strings.Contains(msg.Content, "first-write-evidence")
		}
		if msg.ToolID == "1" {
			sawUnknown = strings.Contains(msg.Content, "may still be running")
		}
	}
	require.True(t, sawFirst)
	require.True(t, sawUnknown)
	close(release)
	waitCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	_, err = r.(ExecutionObserver).WaitExecution(waitCtx, "old")
	require.NoError(t, err)
}
