//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summaryfork

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type stubTool struct {
	name string
}

func (t stubTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func TestAttachSnapshotsRequest(t *testing.T) {
	text := "part"
	index := 1
	maxTokens := 10
	req := &model.Request{
		Messages: []model.Message{{
			Role: model.RoleAssistant,
			ContentParts: []model.ContentPart{{
				Type: model.ContentTypeText,
				Text: &text,
			}},
			ToolCalls: []model.ToolCall{{
				Index: &index,
				Function: model.FunctionDefinitionParam{
					Arguments: []byte(`{"q":"original"}`),
				},
				ExtraFields: map[string]any{
					"nested": map[string]any{"k": "v"},
				},
			}},
		}},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: &maxTokens,
			Stop:      []string{"END"},
		},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Schema: map[string]any{"type": "object"},
			},
		},
		ExtraFields: map[string]any{
			"metadata": map[string]any{"id": "one"},
		},
		Headers: map[string]string{"X-Trace": "one"},
	}

	inv := agent.NewInvocation()
	Attach(inv, req)

	*req.Messages[0].ContentParts[0].Text = "mutated"
	req.Messages[0].ToolCalls[0].Function.Arguments[0] = '['
	req.Messages[0].ToolCalls[0].ExtraFields["nested"].(map[string]any)["k"] = "changed"
	*req.GenerationConfig.MaxTokens = 20
	req.GenerationConfig.Stop[0] = "STOP"
	req.StructuredOutput.JSONSchema.Schema["type"] = "array"
	req.ExtraFields["metadata"].(map[string]any)["id"] = "two"
	req.Headers["X-Trace"] = "two"

	got, ok := Request(inv)
	require.True(t, ok)
	require.Equal(t, "part", *got.Messages[0].ContentParts[0].Text)
	require.Equal(t, byte('{'), got.Messages[0].ToolCalls[0].Function.Arguments[0])
	require.Equal(t, "v", got.Messages[0].ToolCalls[0].ExtraFields["nested"].(map[string]any)["k"])
	require.Equal(t, 10, *got.GenerationConfig.MaxTokens)
	require.Equal(t, "END", got.GenerationConfig.Stop[0])
	require.Equal(t, "object", got.StructuredOutput.JSONSchema.Schema["type"])
	require.Equal(t, "one", got.ExtraFields["metadata"].(map[string]any)["id"])
	require.Equal(t, "one", got.Headers["X-Trace"])

	got.GenerationConfig.Stop[0] = "again"
	*got.GenerationConfig.MaxTokens = 30
	again, ok := Request(inv)
	require.True(t, ok)
	require.Equal(t, 10, *again.GenerationConfig.MaxTokens)
	require.Equal(t, "END", again.GenerationConfig.Stop[0])
}

func TestAttachHandlesNilAndZeroValueRequest(t *testing.T) {
	Attach(nil, &model.Request{})
	Attach(agent.NewInvocation(), nil)

	inv := agent.NewInvocation()
	_, ok := Request(inv)
	require.False(t, ok)

	inv.SetState(stateKey, (*model.Request)(nil))
	_, ok = Request(inv)
	require.False(t, ok)

	Attach(inv, &model.Request{})
	got, ok := Request(inv)
	require.True(t, ok)
	require.NotNil(t, got)
	require.Nil(t, got.Messages)
	require.Nil(t, got.StructuredOutput)
	require.Nil(t, got.Headers)
	require.Nil(t, got.Tools)
}

func TestAttachSnapshotsMultimodalPartsAndTools(t *testing.T) {
	text := "text"
	req := &model.Request{
		Messages: []model.Message{{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{
					Type: model.ContentTypeText,
					Text: &text,
				},
				{
					Type: model.ContentTypeImage,
					Image: &model.Image{
						Data: []byte{1, 2, 3},
					},
				},
				{
					Type: model.ContentTypeAudio,
					Audio: &model.Audio{
						Data: []byte{4, 5, 6},
					},
				},
				{
					Type: model.ContentTypeFile,
					File: &model.File{
						Data: []byte{7, 8, 9},
					},
				},
			},
		}},
		Tools: map[string]tool.Tool{
			"lookup": stubTool{name: "lookup"},
		},
	}

	inv := agent.NewInvocation()
	Attach(inv, req)

	*req.Messages[0].ContentParts[0].Text = "changed"
	req.Messages[0].ContentParts[1].Image.Data[0] = 9
	req.Messages[0].ContentParts[2].Audio.Data[0] = 9
	req.Messages[0].ContentParts[3].File.Data[0] = 9
	req.Tools["lookup"] = stubTool{name: "changed"}
	req.Tools["extra"] = stubTool{name: "extra"}

	got, ok := Request(inv)
	require.True(t, ok)
	parts := got.Messages[0].ContentParts
	require.Equal(t, "text", *parts[0].Text)
	require.Equal(t, []byte{1, 2, 3}, parts[1].Image.Data)
	require.Equal(t, []byte{4, 5, 6}, parts[2].Audio.Data)
	require.Equal(t, []byte{7, 8, 9}, parts[3].File.Data)
	require.Len(t, got.Tools, 1)
	require.Equal(t, "lookup", got.Tools["lookup"].Declaration().Name)
}

func TestAppendResponseExtendsSnapshot(t *testing.T) {
	inv := agent.NewInvocation()
	Attach(inv, &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("stable system"),
			model.NewUserMessage("question"),
		},
	})

	AppendResponse(inv, &model.Response{Choices: []model.Choice{{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID: "call_1",
				Function: model.FunctionDefinitionParam{
					Name: "lookup",
				},
			}},
		},
	}}})
	AppendResponse(inv, &model.Response{Choices: []model.Choice{{
		Message: model.NewToolMessage("call_1", "lookup", "result"),
	}}})

	got, ok := Request(inv)
	require.True(t, ok)
	require.Len(t, got.Messages, 4)
	require.Equal(t, model.RoleAssistant, got.Messages[2].Role)
	require.Len(t, got.Messages[2].ToolCalls, 1)
	require.Equal(t, model.RoleTool, got.Messages[3].Role)
	require.Equal(t, "result", got.Messages[3].Content)
}

func TestAppendResponseNoopsWithoutPayload(t *testing.T) {
	AppendResponse(nil, &model.Response{Choices: []model.Choice{{
		Message: model.NewAssistantMessage("ignored"),
	}}})
	AppendResponse(agent.NewInvocation(), &model.Response{Choices: []model.Choice{{
		Message: model.NewAssistantMessage("ignored"),
	}}})

	inv := agent.NewInvocation()
	Attach(inv, &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
	})

	AppendResponse(inv, nil)
	AppendResponse(inv, &model.Response{})
	AppendResponse(inv, &model.Response{Choices: []model.Choice{{}}})

	got, ok := Request(inv)
	require.True(t, ok)
	require.Len(t, got.Messages, 1)
}

func TestAppendResponseUsesDeltaFallback(t *testing.T) {
	inv := agent.NewInvocation()
	Attach(inv, &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
	})

	AppendResponse(inv, &model.Response{Choices: []model.Choice{{
		Delta: model.NewAssistantMessage("streamed"),
	}}})

	got, ok := Request(inv)
	require.True(t, ok)
	require.Len(t, got.Messages, 2)
	require.Equal(t, "streamed", got.Messages[1].Content)
}

func TestAppendResponseKeepsPrimaryChoiceOnly(t *testing.T) {
	inv := agent.NewInvocation()
	Attach(inv, &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
	})

	AppendResponse(inv, &model.Response{Choices: []model.Choice{
		{
			Index:   1,
			Message: model.NewAssistantMessage("alternative"),
		},
		{
			Index:   0,
			Message: model.NewAssistantMessage("primary"),
		},
	}})

	got, ok := Request(inv)
	require.True(t, ok)
	require.Len(t, got.Messages, 2)
	require.Equal(t, "primary", got.Messages[1].Content)
}

func TestAppendResponseFallsBackToFirstChoice(t *testing.T) {
	inv := agent.NewInvocation()
	Attach(inv, &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
	})

	AppendResponse(inv, &model.Response{Choices: []model.Choice{
		{
			Index:   2,
			Message: model.NewAssistantMessage("first"),
		},
		{
			Index:   3,
			Message: model.NewAssistantMessage("second"),
		},
	}})

	got, ok := Request(inv)
	require.True(t, ok)
	require.Len(t, got.Messages, 2)
	require.Equal(t, "first", got.Messages[1].Content)
}
