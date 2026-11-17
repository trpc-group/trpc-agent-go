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
	"errors"
	"fmt"
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

func executionTestModel(names ...string) *sequentialModel {
	calls := make([]model.ToolCall, len(names))
	for i, name := range names {
		calls[i] = model.ToolCall{ID: fmt.Sprint(i), Type: "function", Function: model.FunctionDefinitionParam{Name: name, Arguments: []byte(`{}`)}}
	}
	return &sequentialModel{name: "execution-test", responses: []*model.Response{
		{ID: "round", Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, ToolCalls: calls}}}},
		{ID: "final", Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}}},
	}}
}

func executionRecords(t *testing.T, svc session.Service) []tooljournal.Record {
	t.Helper()
	sess, err := svc.GetSession(context.Background(), session.Key{AppName: "execution-test", UserID: "u", SessionID: "s"})
	require.NoError(t, err)
	records, err := tooljournal.Records(sess)
	require.NoError(t, err)
	return records
}

func TestToolExecutionTracking_OutputModes(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		for _, perCall := range []bool{false, true} {
			t.Run(fmt.Sprintf("parallel=%v/perCall=%v", parallel, perCall), func(t *testing.T) {
				svc := inmemory.NewSessionService()
				defer svc.Close()
				var calls atomic.Int32
				tools := []tool.Tool{}
				for _, name := range []string{"a", "b"} {
					tools = append(tools, function.NewFunctionTool(func(context.Context, struct{}) (string, error) { calls.Add(1); return "evidence", nil }, function.WithName(name)))
				}
				r := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(executionTestModel("a", "b")), llmagent.WithTools(tools), llmagent.WithEnableParallelTools(parallel)), WithSessionService(svc), WithToolExecutionTracking(true))
				defer r.Close()
				ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("run"), agent.WithToolResultEventPerCallEnabled(perCall))
				require.NoError(t, err)
				for range ch {
				}
				require.EqualValues(t, 2, calls.Load())
				records := executionRecords(t, svc)
				require.Len(t, records, 1)
				require.NotEmpty(t, records[0].SourceEventID)
				for _, call := range records[0].Calls {
					require.Equal(t, "completed", call.Status)
					require.Equal(t, 1, call.Attempts)
					require.Contains(t, call.Choices[0].Message.Content, "evidence")
				}
			})
		}
	}
}

type executionFailStore struct {
	session.Service
	failAt int
	writes atomic.Int32
}

func (s *executionFailStore) AppendEvent(ctx context.Context, sess *session.Session, e *event.Event, opts ...session.Option) error {
	for key := range e.StateDelta {
		if strings.HasPrefix(key, tooljournal.Prefix) && int(s.writes.Add(1)) == s.failAt {
			return errors.New("injected journal append failure")
		}
	}
	return s.Service.AppendEvent(ctx, sess, e, opts...)
}

func TestToolExecutionTracking_FailClosed(t *testing.T) {
	for _, stage := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(stage), func(t *testing.T) {
			svc := &executionFailStore{Service: inmemory.NewSessionService(), failAt: stage}
			defer svc.Close()
			var calls atomic.Int32
			tl := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { calls.Add(1); return "written", nil }, function.WithName("a"))
			r := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(executionTestModel("a")), llmagent.WithTools([]tool.Tool{tl})), WithSessionService(svc), WithToolExecutionTracking(true))
			defer r.Close()
			ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("run"))
			require.NoError(t, err)
			for range ch {
			}
			require.Zero(t, calls.Load(), "failed plan/dispatch/attempt persistence must not start external work")
		})
	}
}

func TestToolExecutionTracking_LateCompletion(t *testing.T) {
	svc := inmemory.NewSessionService()
	defer svc.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	tl := function.NewFunctionTool(func(context.Context, struct{}) (string, error) {
		close(entered)
		<-release
		return "late evidence", nil
	}, function.WithName("a"))
	r := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(executionTestModel("a")), llmagent.WithTools([]tool.Tool{tl})), WithSessionService(svc), WithToolExecutionTracking(true))
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := r.Run(ctx, "u", "s", model.NewUserMessage("run"))
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
	cancel()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("output did not close")
	}
	records := executionRecords(t, svc)
	require.Equal(t, "dispatched", records[0].Calls[0].Status)
	close(release)
	require.Eventually(t, func() bool {
		records := executionRecords(t, svc)
		return len(records) == 1 && records[0].Calls[0].Status == "completed"
	}, time.Second, time.Millisecond)
	require.Contains(t, executionRecords(t, svc)[0].Calls[0].Choices[0].Message.Content, "late evidence")
}

func TestToolExecutionTracking_RetryAttempts(t *testing.T) {
	svc := inmemory.NewSessionService()
	defer svc.Close()
	var calls atomic.Int32
	tl := function.NewFunctionTool(func(context.Context, struct{}) (string, error) {
		if calls.Add(1) == 1 {
			return "", errors.New("retry me")
		}
		return "ok", nil
	}, function.WithName("a"))
	r := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(executionTestModel("a")), llmagent.WithTools([]tool.Tool{tl}), llmagent.WithToolCallRetryPolicy(&tool.RetryPolicy{MaxAttempts: 2, RetryOn: func(context.Context, *tool.RetryInfo) (bool, error) { return true, nil }})), WithSessionService(svc), WithToolExecutionTracking(true))
	defer r.Close()
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("run"))
	require.NoError(t, err)
	for range ch {
	}
	records := executionRecords(t, svc)
	require.Len(t, records, 1)
	require.Equal(t, 2, records[0].Calls[0].Attempts)
	require.Equal(t, "completed", records[0].Calls[0].Status)
}
