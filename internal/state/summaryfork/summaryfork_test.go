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
)

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
