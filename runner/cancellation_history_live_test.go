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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type historyLiveModel struct {
	model.Model
	requests chan *model.Request
}

func (m *historyLiveModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.requests <- req
	return m.Model.GenerateContent(ctx, req)
}

func TestCancellationLive_ProjectedHistory(t *testing.T) {
	if os.Getenv("TRPC_CANCEL_LIVE") != "1" {
		t.Skip("set TRPC_CANCEL_LIVE=1 to enable paid provider validation")
	}
	for _, key := range []string{"OPENAI_BASE_URL", "OPENAI_API_KEY", "MODEL_NAME"} {
		require.NotEmpty(t, os.Getenv(key), "missing %s", key)
	}
	svc := inmemory.NewSessionService()
	defer svc.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { cancel(); return "first-write-evidence", nil }, function.WithName("a"))
	b := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { return "must not execute", nil }, function.WithName("b"))
	seed := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(executionTestModel("a", "b")), llmagent.WithTools([]tool.Tool{a, b})), WithSessionService(svc), WithToolExecutionTracking(true))
	defer seed.Close()
	ch, err := seed.Run(ctx, "u", "s", model.NewUserMessage("Execute both local tools."), agent.WithRequestID("seed"))
	require.NoError(t, err)
	for range ch {
	}
	waitCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	_, err = seed.(ExecutionObserver).WaitExecution(waitCtx, "seed")
	require.NoError(t, err)
	live := &historyLiveModel{Model: openai.New(os.Getenv("MODEL_NAME"), openai.WithBaseURL(os.Getenv("OPENAI_BASE_URL")), openai.WithAPIKey(os.Getenv("OPENAI_API_KEY"))), requests: make(chan *model.Request, 4)}
	maxTokens := 2048
	next := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(live), llmagent.WithGenerationConfig(model.GenerationConfig{MaxTokens: &maxTokens})), WithSessionService(svc), WithToolExecutionTracking(true))
	defer next.Close()
	liveCtx, stopLive := context.WithTimeout(context.Background(), 90*time.Second)
	defer stopLive()
	ch, err = next.Run(liveCtx, "u", "s", model.NewUserMessage("Briefly report which previous tool completed and which was not dispatched. Do not execute any tools."))
	require.NoError(t, err)
	var content string
	for e := range ch {
		if e.Response == nil {
			continue
		}
		require.Nil(t, e.Error)
		for _, choice := range e.Choices {
			if choice.Message.Role == model.RoleAssistant {
				content += choice.Message.Content
			}
		}
	}
	require.NotEmpty(t, content)
	req := <-live.requests
	var first, second string
	for _, msg := range req.Messages {
		if msg.ToolID == "0" {
			first = msg.Content
		}
		if msg.ToolID == "1" {
			second = msg.Content
		}
	}
	require.Contains(t, first, "first-write-evidence")
	require.True(t, strings.Contains(second, "not dispatched"))
	t.Log("live provider accepted projected completed sibling plus explicit not-dispatched tool response")
}
