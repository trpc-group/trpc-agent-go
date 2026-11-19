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
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/tooljournal"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func executionFinalModel() *sequentialModel {
	return &sequentialModel{name: "final", responses: []*model.Response{{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}}}}}
}

func seedInterruptedRound(t *testing.T, svc session.Service) (tooljournal.Record, int32) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	a := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { calls.Add(1); cancel(); return "first evidence", nil }, function.WithName("a"))
	b := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { calls.Add(100); return "unexpected", nil }, function.WithName("b"))
	seed := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(executionTestModel("a", "b")), llmagent.WithTools([]tool.Tool{a, b})), WithSessionService(svc), WithToolExecutionTracking(true))
	defer seed.Close()
	ch, err := seed.Run(ctx, "u", "s", model.NewUserMessage("write"), agent.WithRequestID("seed"))
	require.NoError(t, err)
	for range ch {
	}
	waitCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	_, err = seed.(ExecutionObserver).WaitExecution(waitCtx, "seed")
	require.NoError(t, err)
	records := executionRecords(t, svc)
	require.Len(t, records, 1)
	require.Equal(t, "completed", records[0].Calls[0].Status)
	require.Equal(t, "not_started", records[0].Calls[1].Status)
	return records[0], calls.Load()
}

func TestCancellationResume_ReusesCompletedAndRunsOnlyNotStarted(t *testing.T) {
	svc := inmemory.NewSessionService()
	defer svc.Close()
	_, seedCalls := seedInterruptedRound(t, svc)
	require.EqualValues(t, 1, seedCalls)
	var callsA, callsB atomic.Int32
	a := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { callsA.Add(1); return "duplicate", nil }, function.WithName("a"))
	b := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { callsB.Add(1); return "second evidence", nil }, function.WithName("b"))
	m := executionFinalModel()
	r := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{a, b})), WithSessionService(svc), WithToolExecutionTracking(true))
	defer r.Close()
	ch, err := r.Run(context.Background(), "u", "s", model.Message{}, agent.WithResume(true), agent.WithStrictToolResume(true), agent.WithRequestID("resume"))
	require.NoError(t, err)
	for e := range ch {
		if e.Response != nil {
			require.Nil(t, e.Error)
		}
	}
	require.Zero(t, callsA.Load())
	require.EqualValues(t, 1, callsB.Load())
	require.Len(t, m.Requests(), 1)
	results := map[string]string{}
	for _, msg := range m.Requests()[0].messages {
		if msg.Role == model.RoleTool {
			results[msg.ToolID] = msg.Content
		}
	}
	require.Contains(t, results["0"], "first evidence")
	require.Contains(t, results["1"], "second evidence")
	records := executionRecords(t, svc)
	require.Len(t, records, 1)
	require.Equal(t, "completed", records[0].Calls[1].Status)
	require.Equal(t, "resume", records[0].Calls[1].DispatchRequestID)
}

func TestCancellationResume_IntentWithoutOutcomeCannotReplay(t *testing.T) {
	for _, status := range []string{"dispatched", "unknown"} {
		t.Run(status, func(t *testing.T) {
			svc := inmemory.NewSessionService()
			defer svc.Close()
			record, _ := seedInterruptedRound(t, svc)
			record.Calls[0].Status = status
			record.Calls[0].Choices = nil
			data, err := json.Marshal(record)
			require.NoError(t, err)
			sess, err := svc.GetSession(context.Background(), session.Key{AppName: "execution-test", UserID: "u", SessionID: "s"})
			require.NoError(t, err)
			e := event.New("crashed", "agent")
			e.StateDelta = map[string][]byte{tooljournal.Prefix + record.ID: data}
			require.NoError(t, svc.AppendEvent(context.Background(), sess, e))
			var calls atomic.Int32
			tools := []tool.Tool{}
			for _, name := range []string{"a", "b"} {
				tools = append(tools, function.NewFunctionTool(func(context.Context, struct{}) (string, error) { calls.Add(1); return "duplicate", nil }, function.WithName(name)))
			}
			m := executionFinalModel()
			r := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithTools(tools)), WithSessionService(svc), WithToolExecutionTracking(true))
			defer r.Close()
			ch, err := r.Run(context.Background(), "u", "s", model.Message{}, agent.WithResume(true))
			require.NoError(t, err)
			var rejected bool
			for e := range ch {
				if e.Response != nil && e.Error != nil && strings.Contains(e.Error.Message, "verify external effects") {
					rejected = true
				}
			}
			require.True(t, rejected)
			require.Zero(t, calls.Load())
			require.Empty(t, m.Requests())
		})
	}
}

func TestCancellationResume_ConcurrentRequestsDoNotDuplicate(t *testing.T) {
	svc := inmemory.NewSessionService()
	defer svc.Close()
	seedInterruptedRound(t, svc)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var calls atomic.Int32
	b := function.NewFunctionTool(func(context.Context, struct{}) (string, error) {
		calls.Add(1)
		close(entered)
		<-release
		return "second evidence", nil
	}, function.WithName("b"))
	r := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(executionFinalModel()), llmagent.WithTools([]tool.Tool{b})), WithSessionService(svc), WithToolExecutionTracking(true))
	defer r.Close()
	first, err := r.Run(context.Background(), "u", "s", model.Message{}, agent.WithResume(true), agent.WithRequestID("first"))
	require.NoError(t, err)
	drained := make(chan struct{})
	go func() {
		for range first {
		}
		close(drained)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("resumed tool did not start")
	}
	second, err := r.Run(context.Background(), "u", "s", model.Message{}, agent.WithResume(true), agent.WithRequestID("second"))
	require.NoError(t, err)
	var rejected bool
	for e := range second {
		if e.Response != nil && e.Error != nil && strings.Contains(e.Error.Message, "still executing") {
			rejected = true
		}
	}
	require.True(t, rejected)
	require.EqualValues(t, 1, calls.Load())
	close(release)
	<-drained
}
