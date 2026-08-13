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

func TestTextContent(t *testing.T) {
	first := "first"
	second := "second"
	empty := ""
	spaced := "  hello  "
	tabbed := "\tworld\n"
	tests := []struct {
		name string
		msg  model.Message
		want string
	}{
		{
			name: "content is authoritative over parts",
			msg: model.Message{
				Content: "keep me",
				ContentParts: []model.ContentPart{
					{Type: model.ContentTypeText, Text: &first},
					{
						Type:  model.ContentTypeImage,
						Image: &model.Image{URL: "https://example.com/a.png"},
					},
				},
			},
			want: "keep me",
		},
		{
			name: "empty message",
			msg:  model.Message{},
			want: "",
		},
		{
			name: "text-only parts",
			msg: model.Message{
				ContentParts: []model.ContentPart{
					{Type: model.ContentTypeText, Text: &first},
				},
			},
			want: "first",
		},
		{
			name: "join text parts in order",
			msg: model.Message{
				ContentParts: []model.ContentPart{
					{Type: model.ContentTypeText, Text: &first},
					{Type: model.ContentTypeText, Text: &second},
				},
			},
			want: "first\nsecond",
		},
		{
			name: "text and image preserve text order",
			msg: model.Message{
				ContentParts: []model.ContentPart{
					{Type: model.ContentTypeText, Text: &first},
					{
						Type:  model.ContentTypeImage,
						Image: &model.Image{URL: "https://example.com/a.png"},
					},
					{Type: model.ContentTypeText, Text: &second},
				},
			},
			want: "first\nsecond",
		},
		{
			name: "pure image has no fabricated text",
			msg: model.Message{
				ContentParts: []model.ContentPart{
					{
						Type:  model.ContentTypeImage,
						Image: &model.Image{URL: "https://example.com/a.png"},
					},
				},
			},
			want: "",
		},
		{
			name: "ignore nil empty and non-text parts",
			msg: model.Message{
				ContentParts: []model.ContentPart{
					{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.com/a.png"}},
					{Type: model.ContentTypeText, Text: nil},
					{Type: model.ContentTypeText, Text: &empty},
					{Type: model.ContentTypeText, Text: &first},
					{Type: model.ContentTypeFile, File: &model.File{Name: "a.txt"}},
					{Type: model.ContentTypeText, Text: &second},
				},
			},
			want: "first\nsecond",
		},
		{
			name: "content whitespace is preserved",
			msg:  model.Message{Content: "  keep spaces  \n"},
			want: "  keep spaces  \n",
		},
		{
			name: "text part whitespace is preserved",
			msg: model.Message{
				ContentParts: []model.ContentPart{
					{Type: model.ContentTypeText, Text: &spaced},
					{Type: model.ContentTypeText, Text: &tabbed},
				},
			},
			want: "  hello  \n\tworld\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, TextContent(tt.msg))
		})
	}
}
