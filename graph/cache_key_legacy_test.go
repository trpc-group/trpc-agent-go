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
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// legacyCacheKeyGolden holds default cache keys computed by the graph package
// before native replay support. Regenerate the values only from that revision
// with GRAPH_LEGACY_CACHE_KEY_PRINT=1.
var legacyCacheKeyGolden = map[string]string{
	"message_direct":  "c0a55227364f6e54f53c3510b09af87bb2348ff0c1966d56c0bdf4819a6f84a7",
	"message_pointer": "66ec2cef8c032ff9f8155d2c542420158dbf78e23ee88d3a0bfcd934bb3c56dd",
	"message_value":   "37f6adfdc6faf6f77dd50f7c4cd4ec8cff2a539566e3c21c9965fe9db635157b",
	"nested":          "a2ce92e6b1942bf0fa44e68e8c52f8a2da761cf43ce116525e35ffc918de5ad3",
	"response":        "8652b458200943d7a3fe9e9ba56a15f3b3004ebfb96c7579167e05283369eb41",
	"state":           "14904dc30727cc9acabf5d5cd6d5a781ae70f40bd592c3f46c9b895f02ebfea1",
}

func legacyCacheKeyMessage() model.Message {
	index := 1
	text := "part"
	return model.Message{
		Role:               model.RoleAssistant,
		Content:            "content",
		ContentParts:       []model.ContentPart{{Type: model.ContentTypeText, Text: &text}},
		ReasoningContent:   "reasoning",
		ReasoningSignature: "signature",
		ToolCalls: []model.ToolCall{{
			Type:  "function",
			ID:    "call_1",
			Index: &index,
			Function: model.FunctionDefinitionParam{
				Name:      "lookup",
				Arguments: []byte(`{"q":"one"}`),
			},
		}},
	}
}

func legacyCacheKeyInputs() map[string]any {
	message := legacyCacheKeyMessage()
	messages := []model.Message{
		model.NewUserMessage("question"),
		message,
		model.NewToolMessage("call_1", "lookup", "result"),
	}
	return map[string]any{
		"state": State{
			StateKeyMessages:  messages,
			StateKeyUserInput: "question",
			"counter":         3,
		},
		"message_value":   map[string]any{"message": message},
		"message_pointer": map[string]any{"message": &message},
		"message_direct":  message,
		"response": map[string]any{"response": &model.Response{
			ID:      "resp",
			Done:    true,
			Choices: []model.Choice{{Index: 0, Message: message, Delta: message}},
		}},
		"nested": map[string]any{"nested": map[string]any{
			"messages": []any{message, model.NewUserMessage("question")},
		}},
	}
}

func TestDefaultCachePolicy_LegacyMessageKeysGolden(t *testing.T) {
	policy := DefaultCachePolicy()
	got := make(map[string]string)
	for name, input := range legacyCacheKeyInputs() {
		key, err := policy.KeyFunc(sanitizeForCacheKey(input))
		require.NoError(t, err, name)
		got[name] = string(key)
	}
	if os.Getenv("GRAPH_LEGACY_CACHE_KEY_PRINT") == "1" {
		names := make([]string, 0, len(got))
		for name := range got {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			t.Logf("%q: %q,", name, got[name])
		}
		return
	}
	require.Equal(t, legacyCacheKeyGolden, got)
}
