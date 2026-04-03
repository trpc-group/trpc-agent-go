//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package messagemerger_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
	rootplugin "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/messagemerger"
)

func TestPlugin_MergesSupportedRoles(t *testing.T) {
	p := messagemerger.New()
	m := rootplugin.MustNewManager(p)
	callbacks := m.ModelCallbacks()
	require.NotNil(t, callbacks)
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("policy"),
			{
				Role: model.RoleSystem,
				ContentParts: []model.ContentPart{{
					Type: model.ContentTypeText,
					Text: model.StringPtr("extra system"),
				}},
			},
			model.NewUserMessage("hello"),
			{
				Role: model.RoleUser,
				ContentParts: []model.ContentPart{{
					Type: model.ContentTypeText,
					Text: model.StringPtr("extra user"),
				}},
			},
			{
				Role:             model.RoleAssistant,
				Content:          "first answer",
				ReasoningContent: "first reasoning",
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   "call_1",
					Function: model.FunctionDefinitionParam{
						Name:      "search",
						Arguments: []byte(`{"q":"weather"}`),
					},
				}},
			},
			{
				Role:             model.RoleAssistant,
				Content:          "second answer",
				ReasoningContent: "second reasoning",
			},
			model.NewToolMessage("call_1", "search", "search result"),
			model.NewToolMessage("call_2", "lookup", "lookup result"),
			model.NewUserMessage("tail"),
		},
	}
	_, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: req},
	)
	require.NoError(t, err)
	require.Len(t, req.Messages, 6)
	require.Equal(t, model.RoleSystem, req.Messages[0].Role)
	require.Empty(t, req.Messages[0].Content)
	require.Len(t, req.Messages[0].ContentParts, 3)
	require.NotNil(t, req.Messages[0].ContentParts[0].Text)
	require.Equal(t, "policy", *req.Messages[0].ContentParts[0].Text)
	require.NotNil(t, req.Messages[0].ContentParts[1].Text)
	require.Equal(t, "\n\n", *req.Messages[0].ContentParts[1].Text)
	require.NotNil(t, req.Messages[0].ContentParts[2].Text)
	require.Equal(t, "extra system", *req.Messages[0].ContentParts[2].Text)
	require.Equal(t, model.RoleUser, req.Messages[1].Role)
	require.Empty(t, req.Messages[1].Content)
	require.Len(t, req.Messages[1].ContentParts, 3)
	require.NotNil(t, req.Messages[1].ContentParts[0].Text)
	require.Equal(t, "hello", *req.Messages[1].ContentParts[0].Text)
	require.NotNil(t, req.Messages[1].ContentParts[1].Text)
	require.Equal(t, "\n\n", *req.Messages[1].ContentParts[1].Text)
	require.NotNil(t, req.Messages[1].ContentParts[2].Text)
	require.Equal(t, "extra user", *req.Messages[1].ContentParts[2].Text)
	require.Equal(t, model.RoleAssistant, req.Messages[2].Role)
	require.Equal(
		t,
		"first answer\n\nsecond answer",
		req.Messages[2].Content,
	)
	require.Equal(
		t,
		"first reasoning\n\nsecond reasoning",
		req.Messages[2].ReasoningContent,
	)
	require.Len(t, req.Messages[2].ToolCalls, 1)
	require.Equal(t, "call_1", req.Messages[2].ToolCalls[0].ID)
	require.Equal(t, model.RoleTool, req.Messages[3].Role)
	require.Equal(t, "call_1", req.Messages[3].ToolID)
	require.Equal(t, model.RoleTool, req.Messages[4].Role)
	require.Equal(t, "call_2", req.Messages[4].ToolID)
	require.Equal(t, model.RoleUser, req.Messages[5].Role)
	require.Equal(t, "tail", req.Messages[5].Content)
}

func TestPlugin_DoesNotMergeToolMessages(t *testing.T) {
	p := messagemerger.New()
	m := rootplugin.MustNewManager(p)
	callbacks := m.ModelCallbacks()
	require.NotNil(t, callbacks)
	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("question"),
			model.NewAssistantMessage("calling"),
			model.NewToolMessage("call_1", "search", "result one"),
			model.NewToolMessage("call_2", "lookup", "result two"),
		},
	}
	_, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: req},
	)
	require.NoError(t, err)
	require.Len(t, req.Messages, 4)
	require.Equal(t, model.RoleTool, req.Messages[2].Role)
	require.Equal(t, "call_1", req.Messages[2].ToolID)
	require.Equal(t, model.RoleTool, req.Messages[3].Role)
	require.Equal(t, "call_2", req.Messages[3].ToolID)
}

func TestPlugin_NilRequestIsSafe(t *testing.T) {
	p := messagemerger.New()
	m := rootplugin.MustNewManager(p)
	callbacks := m.ModelCallbacks()
	require.NotNil(t, callbacks)
	_, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{},
	)
	require.NoError(t, err)
}

func TestNew_DefaultName(t *testing.T) {
	got := messagemerger.New()
	require.Equal(t, "consecutive_message_merger", got.Name())
}

func TestNew_WithName(t *testing.T) {
	got := messagemerger.New(messagemerger.WithName("custom_merger"))
	require.Equal(t, "custom_merger", got.Name())
}

func TestNew_EmptySeparatorOmitsJoinText(t *testing.T) {
	p := messagemerger.New(messagemerger.WithSeparator(""))
	m := rootplugin.MustNewManager(p)
	callbacks := m.ModelCallbacks()
	require.NotNil(t, callbacks)
	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("foo"),
			model.NewUserMessage("bar"),
		},
	}
	_, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: req},
	)
	require.NoError(t, err)
	require.Len(t, req.Messages, 1)
	require.Equal(t, "foobar", req.Messages[0].Content)
}
