//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openairesponses_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestRunnerResumeLatestAfterSupersededNativeToolCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	call := func(id string, argument int) string {
		return fmt.Sprintf(`[{"type":"reasoning","id":"rs_%s","summary":[]},{"type":"function_call","id":"fc_%s","call_id":"call_%s","name":"calculator","arguments":"{\"a\":%d}"}]`, id, id, id, argument)
	}
	server, srv := newSearchServer(t, call("a", 1), call("b", 2), messageOutput("answer", "done"))
	m := newSearchModel(srv)
	svc := openToolResultNativeSQLite(t, filepath.Join(t.TempDir(), "resume-latest.db"))
	key := session.Key{AppName: "native", UserID: "user", SessionID: "session"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	for i, text := range []string{"first request", "second request"} {
		ch, err := m.GenerateContent(ctx, &model.Request{Messages: []model.Message{model.NewUserMessage(text)}})
		require.NoError(t, err)
		pending := collect(t, ch)
		require.Len(t, pending, 1)
		invocation := []string{"run-a", "run-b"}[i]
		user := event.NewResponseEvent(invocation, "user", &model.Response{
			Done: true, Choices: []model.Choice{{Message: model.NewUserMessage(text)}},
		})
		toolCall := event.NewResponseEvent(invocation, "native", pending[0])
		user.RequestID, toolCall.RequestID = invocation, invocation
		appendNativeSessionEvents(t, ctx, svc, sess, user, toolCall)
	}
	var toolCalls, argument atomic.Int32
	calculator := function.NewFunctionTool(func(_ context.Context, in struct {
		A int `json:"a"`
	}) (int, error) {
		toolCalls.Add(1)
		argument.Store(int32(in.A))
		return in.A + 1, nil
	}, function.WithName("calculator"), function.WithDescription("Returns its input plus one."))
	r := runner.NewRunner("native", llmagent.New("native", llmagent.WithModel(m),
		llmagent.WithTools([]tool.Tool{calculator})), runner.WithSessionService(svc))
	defer r.Close()
	events, failures := runSearch(t, r, model.Message{}, agent.WithResume(true))
	require.Empty(t, failures)
	require.Equal(t, "done", finalText(events))
	require.EqualValues(t, 1, toolCalls.Load(), "only the latest pending call may execute")
	require.EqualValues(t, 2, argument.Load())
	requests := server.requests()
	require.Len(t, requests, 3, "resume must make exactly one model request")
	items := conversationItems(t, requests[2]["input"])
	require.Equal(t, []string{"message:user", "reasoning", "function_call", "function_call_output",
		"message:user", "reasoning", "function_call", "function_call_output"}, inputTypes(items))
	require.Equal(t, []string{"call_a", "call_a", "call_b", "call_b"},
		[]string{items[2].CallID, items[3].CallID, items[6].CallID, items[7].CallID})
	require.Contains(t, string(items[0].Content), "first request")
	require.Contains(t, string(items[4].Content), "second request")
	require.Equal(t, map[string]string{"call_a": missingInterruptedToolResult, "call_b": "3"}, inputToolOutputs(t, items))

	stored, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	var results []model.Message
	for _, evt := range stored.Events {
		if evt.Response != nil {
			for _, choice := range evt.Choices {
				if choice.Message.Role == model.RoleTool {
					results = append(results, choice.Message)
				}
			}
		}
	}
	require.Len(t, results, 1, "the old placeholder must not be written to the session")
	require.Equal(t, "call_b", results[0].ToolID)
	require.Equal(t, "3", results[0].Content)
}
