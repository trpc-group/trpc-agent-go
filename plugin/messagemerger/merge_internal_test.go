//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package messagemerger

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestMergeConsecutiveMessages_EmptyInput(t *testing.T) {
	var messages []model.Message
	require.Nil(t, mergeConsecutiveMessages(messages, "\n\n"))
}

func TestCanMergeConsecutiveMessage(t *testing.T) {
	testCases := []struct {
		name string
		msg  model.Message
		want bool
	}{
		{
			name: "supported user role",
			msg:  model.NewUserMessage("hello"),
			want: true,
		},
		{
			name: "tool id prevents merge",
			msg: model.Message{
				Role:   model.RoleUser,
				ToolID: "call_1",
			},
			want: false,
		},
		{
			name: "tool name prevents merge",
			msg: model.Message{
				Role:     model.RoleAssistant,
				ToolName: "search",
			},
			want: false,
		},
		{
			name: "unsupported role is skipped",
			msg: model.Message{
				Role: model.RoleTool,
			},
			want: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, canMergeConsecutiveMessage(tc.msg))
		})
	}
}

func TestJoinMessageText(t *testing.T) {
	require.Equal(t, "tail", joinMessageText("", "tail", "\n\n"))
	require.Equal(t, "head", joinMessageText("head", "", "\n\n"))
	require.Equal(t, "head\n\ntail", joinMessageText("head", "tail", "\n\n"))
}

func TestCloneMessage_ClonesTopLevelSlices(t *testing.T) {
	text := "before"
	original := model.Message{
		Role: model.RoleAssistant,
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeText,
			Text: &text,
		}},
		ToolCalls: []model.ToolCall{{
			ID: "call_1",
		}},
	}
	cloned := cloneMessage(original)
	original.ContentParts[0] = textContentPart("after")
	original.ToolCalls[0] = model.ToolCall{ID: "call_2"}
	require.NotNil(t, cloned.ContentParts[0].Text)
	require.Equal(t, "before", *cloned.ContentParts[0].Text)
	require.Equal(t, "call_1", cloned.ToolCalls[0].ID)
}

func TestMessageTextBoundaries(t *testing.T) {
	empty := ""
	imageOnly := model.Message{
		ContentParts: []model.ContentPart{{
			Type:  model.ContentTypeImage,
			Image: &model.Image{URL: "https://example.com/image.png"},
		}},
	}
	emptyTextPart := model.Message{
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeText,
			Text: &empty,
		}},
	}
	require.False(t, messageStartsWithText(model.Message{}))
	require.True(t, messageStartsWithText(model.NewUserMessage("hello")))
	require.False(t, messageStartsWithText(imageOnly))
	require.False(t, messageStartsWithText(emptyTextPart))
	require.False(t, messageEndsWithText(model.Message{}))
	require.True(t, messageEndsWithText(model.NewAssistantMessage("world")))
	require.False(t, messageEndsWithText(imageOnly))
	require.False(t, messageEndsWithText(emptyTextPart))
}

func TestShouldInsertMessageSeparator(t *testing.T) {
	dst := model.NewUserMessage("alpha")
	src := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			textContentPart("beta"),
		},
	}
	require.True(t, shouldInsertMessageSeparator(dst, src, "\n\n"))
	require.False(t, shouldInsertMessageSeparator(dst, src, ""))
	require.False(t, shouldInsertMessageSeparator(
		model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{{
				Type:  model.ContentTypeImage,
				Image: &model.Image{URL: "https://example.com/last.png"},
			}},
		},
		src,
		"\n\n",
	))
}

func TestMergeMessage_AppendsSourceToolCalls(t *testing.T) {
	dst := model.Message{
		Role:    model.RoleAssistant,
		Content: "first",
		ToolCalls: []model.ToolCall{{
			ID: "call_1",
		}},
	}
	src := model.Message{
		Role:    model.RoleAssistant,
		Content: "second",
		ToolCalls: []model.ToolCall{{
			ID: "call_2",
		}},
	}
	merged := mergeMessage(dst, src, "\n\n")
	require.Equal(t, "first\n\nsecond", merged.Content)
	require.Len(t, merged.ToolCalls, 2)
	require.Equal(t, "call_1", merged.ToolCalls[0].ID)
	require.Equal(t, "call_2", merged.ToolCalls[1].ID)
}

func TestNew_EmptyNameUsesDefault(t *testing.T) {
	got := New(WithName(""))
	require.Equal(t, defaultPluginName, got.Name())
}

func TestRegister_NilReceiverOrRegistryIsSafe(t *testing.T) {
	var nilPlugin *messageMergerPlugin
	require.NotPanics(t, func() {
		nilPlugin.Register(nil)
	})
	plugin := &messageMergerPlugin{}
	require.NotPanics(t, func() {
		plugin.Register(nil)
	})
}
