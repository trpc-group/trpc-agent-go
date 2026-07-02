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
			Stop: []string{"END"},
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
	req.GenerationConfig.Stop[0] = "STOP"
	req.StructuredOutput.JSONSchema.Schema["type"] = "array"
	req.ExtraFields["metadata"].(map[string]any)["id"] = "two"
	req.Headers["X-Trace"] = "two"

	got, ok := Request(inv)
	require.True(t, ok)
	require.Equal(t, "part", *got.Messages[0].ContentParts[0].Text)
	require.Equal(t, byte('{'), got.Messages[0].ToolCalls[0].Function.Arguments[0])
	require.Equal(t, "v", got.Messages[0].ToolCalls[0].ExtraFields["nested"].(map[string]any)["k"])
	require.Equal(t, "END", got.GenerationConfig.Stop[0])
	require.Equal(t, "object", got.StructuredOutput.JSONSchema.Schema["type"])
	require.Equal(t, "one", got.ExtraFields["metadata"].(map[string]any)["id"])
	require.Equal(t, "one", got.Headers["X-Trace"])

	got.GenerationConfig.Stop[0] = "again"
	again, ok := Request(inv)
	require.True(t, ok)
	require.Equal(t, "END", again.GenerationConfig.Stop[0])
}
