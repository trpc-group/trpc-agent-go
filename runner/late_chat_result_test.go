//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	openaiopt "github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// A stopped run may finish persisting a tool result after a newer user turn.
// The next Chat request must keep that turn while pairing the older result.
func TestRunnerChatLateToolResultPreservesNewerTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type request struct {
		Path     string `json:"-"`
		Messages []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	requests := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body request
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body.Path = r.URL.Path
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"final","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	svc := inmemory.NewSessionService()
	defer svc.Close()
	key := session.Key{AppName: "late-chat", UserID: "user", SessionID: "session"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	call := model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
		ID: "call-old", Type: "function", Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte(`{}`)},
	}}}
	fixture := []struct {
		invocation, author string
		message            model.Message
		object             string
	}{
		{"old-run", "user", model.NewUserMessage("old question"), model.ObjectTypeChatCompletion},
		{"old-run", "assistant", call, model.ObjectTypeChatCompletion},
		{"new-run", "assistant", model.NewSystemMessage("new system instruction"), model.ObjectTypeChatCompletion},
		{"new-run", "user", model.NewUserMessage("new question"), model.ObjectTypeChatCompletion},
		{"old-run", "assistant", model.NewToolMessage("call-old", "lookup", "old result"), model.ObjectTypeToolResponse},
	}
	for i, item := range fixture {
		evt := event.NewResponseEvent(item.invocation, item.author, &model.Response{
			Object: item.object, Done: true, Choices: []model.Choice{{Message: item.message}},
		})
		evt.RequestID = item.invocation
		evt.FilterKey = key.AppName
		evt.Timestamp = time.Now().Add(time.Duration(i-len(fixture)) * time.Second)
		require.NoError(t, svc.AppendEvent(ctx, sess, evt))
	}

	m := openai.New("test", openai.WithBaseURL(server.URL), openai.WithAPIKey("test-key"),
		openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)))
	a := llmagent.New("assistant", llmagent.WithModel(m), llmagent.WithMessageTimelineFilterMode("all"))
	r := runner.NewRunner("late-chat", a, runner.WithSessionService(svc))
	defer r.Close()
	stream, err := r.Run(ctx, "user", "session", model.Message{})
	require.NoError(t, err)
	for evt := range stream {
		if evt.Response != nil {
			require.Nil(t, evt.Error)
		}
	}
	require.NoError(t, ctx.Err())
	select {
	case req := <-requests:
		require.Equal(t, "/chat/completions", req.Path)
		require.Len(t, req.Messages, 5)
		require.Equal(t, "system", req.Messages[0].Role)
		require.Equal(t, "new system instruction", req.Messages[0].Content)
		require.Equal(t, "user", req.Messages[1].Role)
		require.Equal(t, "old question", req.Messages[1].Content)
		require.Equal(t, "assistant", req.Messages[2].Role)
		require.Len(t, req.Messages[2].ToolCalls, 1)
		require.Equal(t, "call-old", req.Messages[2].ToolCalls[0].ID)
		require.Equal(t, "tool", req.Messages[3].Role)
		require.Equal(t, "call-old", req.Messages[3].ToolCallID)
		require.Equal(t, "old result", req.Messages[3].Content)
		require.Equal(t, "user", req.Messages[4].Role)
		require.Equal(t, "new question", req.Messages[4].Content)
	case <-ctx.Done():
		t.Fatal("timed out waiting for Chat request")
	}
}
