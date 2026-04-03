//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package content

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestExtractGroundingContext(t *testing.T) {
	actual := &evalset.Invocation{
		UserContent: &model.Message{Content: "What is the weather in Paris?"},
		ContextMessages: []*model.Message{
			{Role: model.RoleSystem, Content: "Use the tool output as the source of truth."},
		},
		IntermediateResponses: []*model.Message{
			{Role: model.RoleAssistant, Content: "Let me check the weather tool."},
		},
		Tools: []*evalset.Tool{
			{
				ID:        "tool-1",
				Name:      "weather_lookup",
				Arguments: map[string]any{"location": "Paris"},
				Result:    map[string]any{"temperatureC": 18, "condition": "Cloudy"},
			},
			{
				ID:   "tool-2",
				Name: "knowledge_search",
				Result: map[string]any{
					"documents": []map[string]any{
						{"text": "Paris is in France.", "score": 0.9},
					},
				},
			},
		},
	}
	text, err := ExtractGroundingContext(actual)
	require.NoError(t, err)
	assert.Contains(t, text, "User prompt:")
	assert.Contains(t, text, "What is the weather in Paris?")
	assert.Contains(t, text, "Context messages:")
	assert.Contains(t, text, "Use the tool output as the source of truth.")
	assert.NotContains(t, text, "Intermediate responses:")
	assert.NotContains(t, text, "Let me check the weather tool.")
	assert.Contains(t, text, "tool_calls:")
	assert.Contains(t, text, "\"id\": \"tool-1\"")
	assert.Contains(t, text, "\"name\": \"weather_lookup\"")
	assert.Contains(t, text, "\"location\": \"Paris\"")
	assert.Contains(t, text, "tool_outputs:")
	assert.Contains(t, text, "\"temperatureC\": 18")
	assert.Contains(t, text, "Paris is in France.")
}

func TestExtractGroundingContextWithoutArtifacts(t *testing.T) {
	text, err := ExtractGroundingContext(&evalset.Invocation{})
	require.NoError(t, err)
	assert.Equal(t, "No validation context was captured.", text)
}

func TestExtractGroundingContextNilInvocation(t *testing.T) {
	text, err := ExtractGroundingContext(nil)
	require.NoError(t, err)
	assert.Equal(t, "No validation context was captured.", text)
}

func TestExtractGroundingContextIgnoresEmptyArtifacts(t *testing.T) {
	actual := &evalset.Invocation{
		ContextMessages: []*model.Message{
			{Role: model.RoleSystem, Content: "   "},
		},
		IntermediateResponses: []*model.Message{
			{Role: model.RoleAssistant, Content: ""},
		},
		Tools: []*evalset.Tool{
			nil,
		},
	}
	text, err := ExtractGroundingContext(actual)
	require.NoError(t, err)
	assert.Equal(t, "No validation context was captured.", text)
}

func TestExtractGroundingContextToolMarshalError(t *testing.T) {
	actual := &evalset.Invocation{
		Tools: []*evalset.Tool{
			{
				Name:      "bad_tool",
				Arguments: make(chan int),
			},
		},
	}
	_, err := ExtractGroundingContext(actual)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal tool bad_tool arguments")
}

func TestExtractGroundingContextToolResultMarshalError(t *testing.T) {
	actual := &evalset.Invocation{
		Tools: []*evalset.Tool{
			{
				Name:   "bad_tool",
				Result: make(chan int),
			},
		},
	}
	_, err := ExtractGroundingContext(actual)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal tool bad_tool result")
}

func TestMarshalGroundingSectionError(t *testing.T) {
	_, err := marshalGroundingSection(make(chan int))
	require.Error(t, err)
}
