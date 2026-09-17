//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package message

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestTextContent(t *testing.T) {
	hello, world, empty := "hello", "world", ""
	assert.Equal(t, "from-content", TextContent(model.Message{
		Content: "from-content",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &hello},
		},
	}))
	assert.Equal(t, "hello\nworld", TextContent(model.Message{
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText},
			{Type: model.ContentTypeText, Text: &empty},
			{Type: model.ContentTypeText, Text: &hello},
			{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.com/a.png"}},
			{Type: model.ContentTypeText, Text: &world},
		},
	}))
	assert.Empty(t, TextContent(model.Message{
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.com/a.png"}},
		},
	}))
}

func TestIsEmptyAssistantMessage(t *testing.T) {
	assert.True(t, IsEmptyAssistantMessage(model.Message{
		Role: model.RoleAssistant,
	}))
	assert.True(t, IsEmptyAssistantMessage(model.Message{
		Role:             model.RoleAssistant,
		ReasoningContent: "reasoning without visible payload",
	}))
	assert.False(t, IsEmptyAssistantMessage(model.Message{
		Role: model.RoleUser,
	}))
	assert.False(t, IsEmptyAssistantMessage(model.Message{
		Role:    model.RoleAssistant,
		Content: "visible content",
	}))
	assert.False(t, IsEmptyAssistantMessage(model.Message{
		Role: model.RoleAssistant,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText},
		},
	}))
	assert.False(t, IsEmptyAssistantMessage(model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{ID: "call_1"},
		},
	}))
}
