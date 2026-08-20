//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestAppendMessages(t *testing.T) {
	base := []model.Message{model.NewUserMessage("a")}
	op := AppendMessages{Items: []model.Message{model.NewAssistantMessage("b")}}
	out := op.Apply(base)
	require.Len(t, out, 2)
	require.Equal(t, model.RoleUser, out[0].Role)
	require.Equal(t, model.RoleAssistant, out[1].Role)
}

func TestReplaceLastUser(t *testing.T) {
	messages := []model.Message{
		model.NewUserMessage("u1"),
		model.NewAssistantMessage("a1"),
		model.NewUserMessage("u2"),
	}
	out := (ReplaceLastUser{Content: "u2-new"}).Apply(messages)
	require.Len(t, out, 3)
	require.Equal(t, model.RoleUser, out[2].Role)
	require.Equal(t, "u2-new", out[2].Content)
}

func TestReplaceLastUserNoUserAppends(t *testing.T) {
	messages := []model.Message{model.NewAssistantMessage("a1")}
	out := (ReplaceLastUser{Content: "u-new"}).Apply(messages)
	require.Len(t, out, 2)
	require.Equal(t, model.RoleUser, out[1].Role)
	require.Equal(t, "u-new", out[1].Content)
}

func TestRemoveAllMessages(t *testing.T) {
	base := []model.Message{model.NewUserMessage("x")}
	out := (RemoveAllMessages{}).Apply(base)
	require.Nil(t, out)
}

func TestRewriteUserMessageText(t *testing.T) {
	hello := "hello"
	world := "world"
	imagePart := model.ContentPart{
		Type:  model.ContentTypeImage,
		Image: &model.Image{URL: "https://example.com/a.png"},
	}

	t.Run("no content parts uses NewUserMessage", func(t *testing.T) {
		got := rewriteUserMessageText(model.NewUserMessage("old"), "new")
		require.Equal(t, model.NewUserMessage("new"), got)
	})

	t.Run("collapse text parts and keep image", func(t *testing.T) {
		msg := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{Type: model.ContentTypeText, Text: &hello},
				imagePart,
				{Type: model.ContentTypeText, Text: &world},
			},
		}
		got := rewriteUserMessageText(msg, "rewritten")
		require.Equal(t, model.RoleUser, got.Role)
		require.Empty(t, got.Content)
		require.Len(t, got.ContentParts, 2)
		require.Equal(t, model.ContentTypeText, got.ContentParts[0].Type)
		require.NotNil(t, got.ContentParts[0].Text)
		require.Equal(t, "rewritten", *got.ContentParts[0].Text)
		require.Equal(t, imagePart, got.ContentParts[1])
	})

	t.Run("pure image adds text and keeps image", func(t *testing.T) {
		msg := model.Message{
			Role:         model.RoleUser,
			ContentParts: []model.ContentPart{imagePart},
		}
		got := rewriteUserMessageText(msg, "caption")
		require.Empty(t, got.Content)
		require.Len(t, got.ContentParts, 2)
		require.Equal(t, model.ContentTypeText, got.ContentParts[0].Type)
		require.Equal(t, "caption", *got.ContentParts[0].Text)
		require.Equal(t, imagePart, got.ContentParts[1])
	})

	t.Run("clears stale content field", func(t *testing.T) {
		msg := model.Message{
			Role:    model.RoleUser,
			Content: "from-content",
			ContentParts: []model.ContentPart{
				{Type: model.ContentTypeText, Text: &hello},
				imagePart,
			},
		}
		got := rewriteUserMessageText(msg, "new")
		require.Empty(t, got.Content)
		require.Equal(t, "new", *got.ContentParts[0].Text)
		require.Equal(t, imagePart, got.ContentParts[1])
	})

	t.Run("preserves non-textual metadata", func(t *testing.T) {
		msg := model.Message{
			Role:    model.RoleUser,
			Content: "old",
			ContentParts: []model.ContentPart{
				{Type: model.ContentTypeText, Text: &hello},
				imagePart,
			},
			ToolID:             "tool-1",
			ToolName:           "lookup",
			ToolCalls:          []model.ToolCall{{ID: "call-1", Type: "function"}},
			ReasoningContent:   "think",
			ReasoningSignature: "sig-abc",
		}
		got := rewriteUserMessageText(msg, "rewritten")
		require.Equal(t, "tool-1", got.ToolID)
		require.Equal(t, "lookup", got.ToolName)
		require.Equal(t, msg.ToolCalls, got.ToolCalls)
		require.Equal(t, "think", got.ReasoningContent)
		require.Equal(t, "sig-abc", got.ReasoningSignature)
		require.Equal(t, "rewritten", *got.ContentParts[0].Text)
		require.Equal(t, imagePart, got.ContentParts[1])

		contentOnly := model.Message{
			Role:               model.RoleUser,
			Content:            "old",
			ToolID:             "tool-2",
			ReasoningContent:   "think2",
			ReasoningSignature: "sig-xyz",
		}
		gotContentOnly := rewriteUserMessageText(contentOnly, "new")
		require.Equal(t, model.NewUserMessage("new"), gotContentOnly)
	})
}

func TestReplaceLastUserMessage(t *testing.T) {
	hello := "hello"
	imagePart := model.ContentPart{
		Type:  model.ContentTypeImage,
		Image: &model.Image{URL: "https://example.com/a.png"},
	}
	rewritten := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &hello},
			imagePart,
		},
	}

	t.Run("replaces last user", func(t *testing.T) {
		messages := []model.Message{
			model.NewUserMessage("u1"),
			model.NewAssistantMessage("a1"),
			model.NewUserMessage("u2"),
		}
		out := (replaceLastUserMessage{Message: rewritten}).Apply(messages)
		require.Len(t, out, 3)
		require.True(t, model.MessagesEqual(rewritten, out[2]))
	})

	t.Run("appends when no user", func(t *testing.T) {
		messages := []model.Message{model.NewAssistantMessage("a1")}
		out := (replaceLastUserMessage{Message: rewritten}).Apply(messages)
		require.Len(t, out, 2)
		require.True(t, model.MessagesEqual(rewritten, out[1]))
	})
}
