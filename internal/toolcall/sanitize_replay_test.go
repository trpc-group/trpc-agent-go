//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolcall

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestSanitizeMessagesWithToolsResultPreservesNativeRounds(t *testing.T) {
	for _, tc := range []struct {
		name      string
		arguments string
		partial   bool
	}{
		{name: "argument whitespace", arguments: " \n{\"a\":1}\t "},
		{name: "schema error result", arguments: `{"a":"not-an-integer"}`},
		{name: "missing result", arguments: `{"a":1}`, partial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "native_a", Function: model.FunctionDefinitionParam{Name: "calculator", Arguments: []byte(tc.arguments)}},
				{ID: "native_b", Function: model.FunctionDefinitionParam{Name: "calculator", Arguments: []byte(`{"a":2}`)}},
			}}
			payload, err := json.Marshal(native.ToolCalls)
			require.NoError(t, err)
			require.NoError(t, native.SetReplayData("test.native", "test-model", payload))
			messages := []model.Message{
				model.NewUserMessage("calculate"), native,
				{Role: model.RoleTool, ToolID: "native_a", Content: "result or validation error"},
			}
			if !tc.partial {
				messages = append(messages, model.Message{Role: model.RoleTool, ToolID: "native_b", Content: "3"})
			}
			nativeEnd := len(messages)
			// A following legacy round must retain its argument normalization.
			messages = append(messages,
				model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
					ID: "legacy", Function: model.FunctionDefinitionParam{Name: "calculator", Arguments: []byte(" {\"a\":3} ")},
				}}},
				model.Message{Role: model.RoleTool, ToolID: "legacy", Content: "4"},
			)
			tools := map[string]tool.Tool{"calculator": stubTool{decl: &tool.Declaration{
				Name: "calculator", InputSchema: &tool.Schema{Type: "object", Properties: map[string]*tool.Schema{
					"a": {Type: "integer"},
				}},
			}}}
			result := SanitizeMessagesWithToolsResult(context.Background(), messages, tools)
			require.Len(t, result.Messages, len(messages))
			require.Nil(t, result.SourceIndexes, "retained messages keep their source positions")
			require.Equal(t, messages[:nativeEnd], result.Messages[:nativeEnd])
			actual, err := result.Messages[1].ReplayDataFor("test.native", "test-model")
			require.NoError(t, err, "history sanitation must preserve native projection bindings")
			require.JSONEq(t, string(payload), string(actual))
			require.Equal(t, []byte(`{"a":3}`), result.Messages[nativeEnd].ToolCalls[0].Function.Arguments)
		})
	}
}
