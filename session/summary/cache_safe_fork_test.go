//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCacheSafeForkRequestContext(t *testing.T) {
	emptyCtx := ContextWithCacheSafeForkRequest(nil, nil)
	require.NotNil(t, emptyCtx)
	got, ok := CacheSafeForkRequestFromContext(emptyCtx)
	require.False(t, ok)
	require.Nil(t, got)

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hello")}}
	ctx := ContextWithCacheSafeForkRequest(nil, req)
	got, ok = CacheSafeForkRequestFromContext(ctx)
	require.True(t, ok)
	require.Same(t, req, got)

	got, ok = CacheSafeForkRequestFromContext(context.Background())
	require.False(t, ok)
	require.Nil(t, got)

	got, ok = CacheSafeForkRequestFromContext(nil)
	require.False(t, ok)
	require.Nil(t, got)
}

func TestCloneRequestForCacheSafeFork_NilFields(t *testing.T) {
	require.Nil(t, cloneRequestForCacheSafeFork(nil))

	cloned := cloneRequestForCacheSafeFork(&model.Request{})
	require.NotNil(t, cloned)
	require.Nil(t, cloned.Messages)
	require.Nil(t, cloned.StructuredOutput)
	require.Nil(t, cloned.ExtraFields)
	require.Nil(t, cloned.Headers)
	require.Nil(t, cloned.Tools)
}

func TestCloneRequestForCacheSafeFork_DeepClonesMutableFields(t *testing.T) {
	text := "text part"
	index := 3
	lookupTool := &cacheSafeForkTestTool{name: "lookup"}
	parent := &model.Request{
		Messages: []model.Message{
			{
				Role:    model.RoleAssistant,
				Content: "assistant",
				ContentParts: []model.ContentPart{
					{Type: model.ContentTypeText, Text: &text},
					{
						Type:  model.ContentTypeImage,
						Image: &model.Image{Data: []byte{1, 2}, Detail: "high", Format: "png"},
					},
					{
						Type:  model.ContentTypeAudio,
						Audio: &model.Audio{Data: []byte{3, 4}, Format: "wav"},
					},
					{
						Type: model.ContentTypeFile,
						File: &model.File{Name: "a.txt", Data: []byte{5, 6}, MimeType: "text/plain"},
					},
				},
				ToolCalls: []model.ToolCall{{
					ID:    "call-1",
					Index: &index,
					Function: model.FunctionDefinitionParam{
						Name:      "lookup",
						Arguments: []byte(`{"q":"cache"}`),
					},
					ExtraFields: map[string]any{"nested": map[string]any{"k": "v"}},
				}},
			},
		},
		GenerationConfig: model.GenerationConfig{Stop: []string{"END"}},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:   "answer",
				Schema: map[string]any{"type": "object", "nested": map[string]any{"k": "v"}},
			},
		},
		ExtraFields: map[string]any{"metadata": map[string]any{"id": "one"}},
		Headers:     map[string]string{"X-Trace": "one"},
		Tools:       map[string]tool.Tool{"lookup": lookupTool},
	}

	cloned := cloneRequestForCacheSafeFork(parent)
	require.NotSame(t, parent, cloned)
	require.NotSame(t, &parent.Messages[0], &cloned.Messages[0])
	require.NotSame(t, parent.Messages[0].ContentParts[0].Text, cloned.Messages[0].ContentParts[0].Text)
	require.NotSame(t, parent.Messages[0].ContentParts[1].Image, cloned.Messages[0].ContentParts[1].Image)
	require.NotSame(t, parent.Messages[0].ContentParts[2].Audio, cloned.Messages[0].ContentParts[2].Audio)
	require.NotSame(t, parent.Messages[0].ContentParts[3].File, cloned.Messages[0].ContentParts[3].File)
	require.NotSame(t, parent.Messages[0].ToolCalls[0].Index, cloned.Messages[0].ToolCalls[0].Index)
	require.NotSame(t, parent.StructuredOutput, cloned.StructuredOutput)
	require.NotSame(t, parent.StructuredOutput.JSONSchema, cloned.StructuredOutput.JSONSchema)

	*cloned.Messages[0].ContentParts[0].Text = "changed"
	cloned.Messages[0].ContentParts[1].Image.Data[0] = 9
	cloned.Messages[0].ContentParts[2].Audio.Data[0] = 9
	cloned.Messages[0].ContentParts[3].File.Data[0] = 9
	cloned.Messages[0].ToolCalls[0].Function.Arguments[0] = '['
	*cloned.Messages[0].ToolCalls[0].Index = 8
	cloned.Messages[0].ToolCalls[0].ExtraFields["nested"].(map[string]any)["k"] = "changed"
	cloned.GenerationConfig.Stop[0] = "STOP"
	cloned.StructuredOutput.JSONSchema.Schema["type"] = "array"
	cloned.StructuredOutput.JSONSchema.Schema["nested"].(map[string]any)["k"] = "changed"
	cloned.ExtraFields["metadata"].(map[string]any)["id"] = "two"
	cloned.Headers["X-Trace"] = "two"
	delete(cloned.Tools, "lookup")

	require.Equal(t, "text part", *parent.Messages[0].ContentParts[0].Text)
	require.Equal(t, byte(1), parent.Messages[0].ContentParts[1].Image.Data[0])
	require.Equal(t, byte(3), parent.Messages[0].ContentParts[2].Audio.Data[0])
	require.Equal(t, byte(5), parent.Messages[0].ContentParts[3].File.Data[0])
	require.Equal(t, byte('{'), parent.Messages[0].ToolCalls[0].Function.Arguments[0])
	require.Equal(t, 3, *parent.Messages[0].ToolCalls[0].Index)
	require.Equal(t, "v", parent.Messages[0].ToolCalls[0].ExtraFields["nested"].(map[string]any)["k"])
	require.Equal(t, "END", parent.GenerationConfig.Stop[0])
	require.Equal(t, "object", parent.StructuredOutput.JSONSchema.Schema["type"])
	require.Equal(t, "v", parent.StructuredOutput.JSONSchema.Schema["nested"].(map[string]any)["k"])
	require.Equal(t, "one", parent.ExtraFields["metadata"].(map[string]any)["id"])
	require.Equal(t, "one", parent.Headers["X-Trace"])
	require.Same(t, lookupTool, parent.Tools["lookup"])
}

func TestSessionSummarizer_CacheSafeForkOptions(t *testing.T) {
	s := NewSummarizer(
		&fakeModel{},
		WithMaxSummaryWords(7),
		WithCacheSafeForkPrompt("Compact within {max_summary_words} words."),
		WithCacheSafeForking(true),
	).(*sessionSummarizer)
	require.True(t, s.Metadata()[metadataKeyCacheSafeForking].(bool))

	rendered, err := s.buildCacheSafeForkPrompt()
	require.NoError(t, err)
	require.Equal(t, "Compact within 7 words.", rendered)

	withoutCustomPrompt := NewSummarizer(
		&fakeModel{},
		WithCacheSafeForkPrompt(""),
	).(*sessionSummarizer)
	defaultPrompt, err := withoutCustomPrompt.buildCacheSafeForkPrompt()
	require.NoError(t, err)
	require.Contains(t, defaultPrompt, "Summarize the user, assistant, and tool conversation above")
	require.NotContains(t, defaultPrompt, "{max_summary_words}")

	disabled := NewSummarizer(&fakeModel{}, WithCacheSafeForking(false)).(*sessionSummarizer)
	require.False(t, disabled.Metadata()[metadataKeyCacheSafeForking].(bool))
}

func TestSessionSummarizer_CacheSafeForkingDisabledUsesStandaloneRequest(t *testing.T) {
	capture := &cacheSafeCaptureModel{response: "summary"}
	s := NewSummarizer(
		capture,
		WithPrompt("Conversation:\n{conversation_text}\n\nSummary:"),
	)
	parent := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("parent system"),
			model.NewUserMessage("parent user"),
		},
	}
	ctx := ContextWithCacheSafeForkRequest(context.Background(), parent)
	sess := &session.Session{ID: "disabled", Events: []event.Event{{
		Author:    "user",
		Timestamp: time.Now(),
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.NewUserMessage("event text"),
		}}},
	}}}

	text, err := s.Summarize(ctx, sess)
	require.NoError(t, err)
	require.Equal(t, "summary", text)
	require.Len(t, capture.request.Messages, 1)
	require.Contains(t, capture.request.Messages[0].Content, "event text")
	require.NotContains(t, capture.request.Messages[0].Content, "parent user")
}

type cacheSafeForkTestTool struct {
	name string
}

func (t *cacheSafeForkTestTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}
