//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner_test

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/tooljournal"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Live tests are opt-in and use only a local counter as the tool side effect.
// They do not assert that remote inference stops or that billing is cancelled.
func cancellationLiveModel(t *testing.T, forceTool bool) model.Model {
	t.Helper()
	if os.Getenv("TRPC_CANCEL_LIVE") != "1" {
		t.Skip("set TRPC_CANCEL_LIVE=1 to enable paid provider validation")
	}
	for _, key := range []string{"OPENAI_BASE_URL", "OPENAI_API_KEY", "MODEL_NAME"} {
		if os.Getenv(key) == "" {
			t.Fatalf("%s must be configured", key)
		}
	}
	opts := []openai.Option{openai.WithBaseURL(os.Getenv("OPENAI_BASE_URL")), openai.WithAPIKey(os.Getenv("OPENAI_API_KEY"))}
	if forceTool {
		opts = append(opts, openai.WithExtraFields(map[string]any{
			"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "cancel_probe"}},
		}))
	}
	return openai.New(os.Getenv("MODEL_NAME"), opts...)
}

func TestCancellationLive_BeforeTool(t *testing.T) {
	m := cancellationLiveModel(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var calls atomic.Int32
	var beforeCalls, afterCalls atomic.Int32
	callbacks := tool.NewCallbacks()
	callbacks.RegisterBeforeTool(func(ctx context.Context, _ *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		beforeCalls.Add(1)
		toolCtx, stop := context.WithCancel(ctx)
		stop()
		return &tool.BeforeToolResult{Context: toolCtx}, nil
	})
	callbacks.RegisterAfterTool(func(_ context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		afterCalls.Add(1)
		return nil, nil
	})
	probe := function.NewFunctionTool(func(context.Context, struct{}) (string, error) {
		calls.Add(1)
		return "local counter incremented", nil
	}, function.WithName("cancel_probe"), function.WithDescription("Increment a local test counter once."))
	maxTokens := 2048
	ag := llmagent.New("cancel-live", llmagent.WithModel(m), llmagent.WithMaxLLMCalls(1),
		llmagent.WithTools([]tool.Tool{probe}), llmagent.WithToolCallbacks(callbacks),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true, MaxTokens: &maxTokens}))
	r := runner.NewRunner("cancel-live", ag)
	defer r.Close()
	events, err := r.Run(ctx, "user", "before-tool", model.NewUserMessage("Call cancel_probe exactly once with no arguments."))
	require.NoError(t, err)
	waitForCancellationOutputClose(t, events, 95*time.Second)
	require.Positive(t, beforeCalls.Load(), "provider must reach the before-tool callback")
	require.Zero(t, calls.Load())
	require.Zero(t, afterCalls.Load())
	t.Log("live provider produced tool call; cancelled effective context prevented tool execution and after-tool callbacks")
}

func TestCancellationLive_ModelStream(t *testing.T) {
	m := cancellationLiveModel(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	maxTokens := 2048
	r := runner.NewRunner("cancel-stream", llmagent.New("agent", llmagent.WithModel(m),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true, MaxTokens: &maxTokens})))
	defer r.Close()
	events, err := r.Run(ctx, "user", "stream", model.NewUserMessage("Explain how a database transaction works in 30 numbered detailed steps."))
	require.NoError(t, err)
	closedAfter := cancelModelStreamOnPartial(t, events, cancel, 90*time.Second)
	t.Logf("live stream cancelled; runner output closed in %s (not a remote-termination assertion)", closedAfter)
}

func TestCancellationLive_TrackedLateResult(t *testing.T) {
	m := cancellationLiveModel(t, true)
	svc := inmemory.NewSessionService()
	defer svc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var calls atomic.Int32
	probe := function.NewFunctionTool(func(context.Context, struct{}) (string, error) {
		calls.Add(1)
		cancel()
		return "durable live evidence", nil
	}, function.WithName("cancel_probe"), function.WithDescription("Increment a local test counter once."))
	r := runner.NewRunner("cancel-journal", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{probe})), runner.WithSessionService(svc), runner.WithToolExecutionTracking(true))
	defer r.Close()
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Call cancel_probe exactly once."), agent.WithRequestID("live-tracked"))
	require.NoError(t, err)
	for range ch {
	}
	require.Eventually(t, func() bool {
		sess, err := svc.GetSession(context.Background(), session.Key{AppName: "cancel-journal", UserID: "user", SessionID: "session"})
		if err != nil {
			return false
		}
		records, err := tooljournal.Records(sess)
		if err != nil || len(records) != 1 || records[0].Calls[0].Status != "completed" {
			return false
		}
		for _, e := range sess.Events {
			if e.IsToolResultResponse() && strings.Contains(e.Choices[0].Message.Content, "durable live evidence") {
				return true
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1, calls.Load())
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	status, err := r.(runner.ExecutionObserver).WaitExecution(waitCtx, "live-tracked")
	require.NoError(t, err)
	require.True(t, status.ExecutionSettled)
	require.True(t, status.OutputClosed)
	require.True(t, status.CancellationRequested)
	require.Zero(t, status.ActiveTools)
	require.Empty(t, status.PersistenceError)
	t.Log("live cancelled run settled with persisted tool evidence and no active tools")
}
