//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestToolToText_IncludesParameters(t *testing.T) {
	tt := &testTool{
		decl: tool.Declaration{
			Name:        "get_weather",
			Description: "Get the current weather in a given location",
			InputSchema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"unit": {
						Type:        "string",
						Description: "The unit of temperature",
					},
					"location": {
						Type:        "string",
						Description: "The city and state, e.g. San Francisco, CA",
					},
				},
			},
		},
	}

	require.Equal(
		t,
		"Tool: get_weather\nDescription: Get the current weather in a given location\nParameters: location (string): The city and state, e.g. San Francisco, CA, unit (string): The unit of temperature",
		toolToText(tt),
	)

	require.Equal(t, "Tool: name\nDescription: desc", toolToText(&testTool{decl: tool.Declaration{Name: "name", Description: "desc"}}))
	require.Empty(t, toolToText(nilTool{}))
}

type nilTool struct{}

func (nilTool) Declaration() *tool.Declaration { return nil }

func TestNewToolKnowledge_DefaultAndOverrideVectorStore(t *testing.T) {
	k0 := NewToolKnowledge(nil)
	require.NotNil(t, k0)
	require.NotNil(t, k0.s)
	_, ok := k0.s.(*inmemory.VectorStore)
	require.True(t, ok, "default VectorStore should be inmemory.VectorStore")

	custom := inmemory.New()
	k1 := NewToolKnowledge(nil, WithVectorStore(custom))
	require.Same(t, custom, k1.s)
}
