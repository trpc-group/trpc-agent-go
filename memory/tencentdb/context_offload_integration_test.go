//go:build integration

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	pluginpkg "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestContextOffloadPlugin_IntegrationLocalModel(t *testing.T) {
	if os.Getenv("TDAI_CONTEXT_OFFLOAD_INTEGRATION") != "1" {
		t.Skip("set TDAI_CONTEXT_OFFLOAD_INTEGRATION=1 to run the live model integration test")
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY is not set")
	}
	modelName := os.Getenv("TDAI_CONTEXT_OFFLOAD_TEST_MODEL")
	if modelName == "" {
		modelName = "gpt-5.2"
	}
	opts := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL := os.Getenv("OPENAI_BASE_URL"); baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	offloadModel := openai.New(modelName, opts...)
	dataDir := t.TempDir()
	svc, err := NewService(
		WithGatewayURL("http://127.0.0.1:1"),
		WithContextOffload(ContextOffloadConfig{
			Enabled: true,
			DataDir: dataDir,
			Model:   offloadModel,
			L0: ContextOffloadL0Config{
				MinToolResultBytes: 10,
			},
			L2: ContextOffloadL2Config{
				NullThreshold: 1,
			},
		}),
		WithConversationSearchTool(false),
	)
	require.NoError(t, err)
	defer svc.Close()

	mgr := pluginpkg.MustNewManager(svc.ContextOffloadPlugin())
	sess := &session.Session{AppName: "app", UserID: "user", ID: "live"}
	inv := &agent.Invocation{InvocationID: "inv", AgentName: "agent", Session: sess, Plugins: mgr}
	msg := model.NewToolMessage(
		"call-live",
		"read_file",
		"File pkg/offload/demo.go defines a ContextOffloadPlugin. It writes refs, summarizes tool results, and injects Mermaid task context.",
	)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := mgr.AfterToolMessages(ctx, &pluginpkg.AfterToolMessagesArgs{
		Invocation: inv,
		Request: &model.Request{Messages: []model.Message{
			model.NewUserMessage("实现 TencentDB context offload"),
		}},
		ToolCalls: []model.ToolCall{{
			ID: "call-live",
			Function: model.FunctionDefinitionParam{
				Name:      "read_file",
				Arguments: []byte(`{"path":"pkg/offload/demo.go"}`),
			},
		}},
		ToolResultMessages: []model.Message{msg},
		Messages: []model.Message{
			model.NewUserMessage("实现 TencentDB context offload"),
			msg,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.ToolResultMessages, 1)
	assert.Contains(t, result.ToolResultMessages[0].Content, "result_ref:")

	store := newOffloadStorageContext(svc.opts, sess, inv.AgentName)
	entries, err := readAllOffloadEntries(store)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.NotEmpty(t, entries[0].Summary)
	assert.NotEmpty(t, entries[0].ResultRef)
	require.NotNil(t, entries[0].NodeID)

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("继续")}}
	invCtx := agent.NewInvocationContext(ctx, inv).Context
	callbacks := mgr.ModelCallbacks()
	require.NotNil(t, callbacks)
	_, err = callbacks.BeforeModel[0](invCtx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.NotEmpty(t, req.Messages)
	assert.Contains(t, req.Messages[0].Content, "<current_task_context>")
}
