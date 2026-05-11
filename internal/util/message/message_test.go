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
