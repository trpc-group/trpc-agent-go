//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	ctool "trpc.group/trpc-go/trpc-agent-go/tool"
)

// stubKnowledge implements the knowledge.Knowledge interface for testing.
// It can be configured to return a predetermined result or error.

type stubKnowledge struct {
	result *knowledge.SearchResult
	err    error
}

func (s stubKnowledge) Search(ctx context.Context, req *knowledge.SearchRequest) (*knowledge.SearchResult, error) {
	return s.result, s.err
}

func marshalArgs(t *testing.T, query string) []byte {
	t.Helper()
	bts, err := json.Marshal(&KnowledgeSearchRequest{Query: query})
	require.NoError(t, err)
	return bts
}

func marshalArgsWithFilter(t *testing.T, query string, filters []KnowledgeFilter) []byte {
	t.Helper()
	bts, err := json.Marshal(&KnowledgeSearchRequestWithFilter{Query: query, Filters: filters})
	require.NoError(t, err)
	return bts
}

func TestKnowledgeSearchTool(t *testing.T) {
	t.Run("empty query", func(t *testing.T) {
		kb := stubKnowledge{}
		searchTool := NewKnowledgeSearchTool(kb)
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, ""))
		require.Error(t, err)
		require.Contains(t, err.Error(), "query cannot be empty")
	})

	t.Run("search error", func(t *testing.T) {
		kb := stubKnowledge{err: errors.New("boom")}
		searchTool := NewKnowledgeSearchTool(kb)
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, "hello"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "search failed")
	})

	t.Run("no result", func(t *testing.T) {
		kb := stubKnowledge{}
		searchTool := NewKnowledgeSearchTool(kb)
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, "hello"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "no relevant information found")
	})

	t.Run("success", func(t *testing.T) {
		kb := stubKnowledge{result: &knowledge.SearchResult{Text: "foo", Score: 0.9}}
		searchTool := NewKnowledgeSearchTool(kb)
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, "hello"))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Equal(t, "foo", rsp.Text)
		require.Equal(t, 0.9, rsp.Score)
		require.Contains(t, rsp.Message, "Found relevant content")
	})

	t.Run("verify options", func(t *testing.T) {
		kb := stubKnowledge{}

		// Verify Declaration metadata is populated.
		ttool := NewKnowledgeSearchTool(kb)
		decl := ttool.Declaration()
		require.NotEmpty(t, decl.Name)
		require.NotEmpty(t, decl.Description)
		require.NotNil(t, decl.InputSchema)
		require.NotNil(t, decl.OutputSchema)

		// Verify WithToolName option
		customName := "custom_search_tool"
		ttool = NewKnowledgeSearchTool(kb, WithToolName(customName))
		decl = ttool.Declaration()
		require.Equal(t, customName, decl.Name)

		// Verify WithToolDescription option
		customDesc := "Custom search description"
		ttool = NewKnowledgeSearchTool(kb, WithToolDescription(customDesc))
		decl = ttool.Declaration()
		require.Equal(t, customDesc, decl.Description)

		// Verify WithFilter option
		customFilter := map[string]any{"source": "internal"}
		ttool = NewKnowledgeSearchTool(kb, WithFilter(customFilter))
		decl = ttool.Declaration()
		require.NotEmpty(t, decl.Name)

		// Verify all options together
		ttool = NewKnowledgeSearchTool(kb, WithToolName(customName), WithToolDescription(customDesc), WithFilter(customFilter))
		decl = ttool.Declaration()
		require.Equal(t, customName, decl.Name)
		require.Equal(t, customDesc, decl.Description)
	})
}

func TestAgenticFilterSearchTool(t *testing.T) {
	agenticFilterInfo := map[string][]any{
		"category": {"documentation", "tutorial", "api"},
		"protocol": {"trpc-go", "http", "grpc"},
		"level":    {"beginner", "intermediate", "advanced"},
	}

	t.Run("empty query", func(t *testing.T) {
		kb := stubKnowledge{}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgsWithFilter(t, "", nil))
		require.Error(t, err)
		require.Contains(t, err.Error(), "query cannot be empty")
	})

	t.Run("search error", func(t *testing.T) {
		kb := stubKnowledge{err: errors.New("search failed")}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		filters := []KnowledgeFilter{{Key: "category", Value: "documentation"}}
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgsWithFilter(t, "hello", filters))
		require.Error(t, err)
		require.Contains(t, err.Error(), "search failed")
	})

	t.Run("no result", func(t *testing.T) {
		kb := stubKnowledge{}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		filters := []KnowledgeFilter{{Key: "category", Value: "documentation"}}
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgsWithFilter(t, "hello", filters))
		require.Error(t, err)
		require.Contains(t, err.Error(), "no relevant information found")
	})

	t.Run("success with single filter", func(t *testing.T) {
		kb := stubKnowledge{result: &knowledge.SearchResult{Text: "filtered content", Score: 0.85}}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		filters := []KnowledgeFilter{{Key: "category", Value: "documentation"}}
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgsWithFilter(t, "hello", filters))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Equal(t, "filtered content", rsp.Text)
		require.Equal(t, 0.85, rsp.Score)
		require.Contains(t, rsp.Message, "Found relevant content")
	})

	t.Run("success with multiple filters", func(t *testing.T) {
		kb := stubKnowledge{result: &knowledge.SearchResult{Text: "multi-filtered content", Score: 0.92}}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		filters := []KnowledgeFilter{
			{Key: "category", Value: "documentation"},
			{Key: "protocol", Value: "trpc-go"},
			{Key: "level", Value: "intermediate"},
		}
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgsWithFilter(t, "trpc gateway", filters))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Equal(t, "multi-filtered content", rsp.Text)
		require.Equal(t, 0.92, rsp.Score)
		require.Contains(t, rsp.Message, "Found relevant content")
	})

	t.Run("success with no filters", func(t *testing.T) {
		kb := stubKnowledge{result: &knowledge.SearchResult{Text: "unfiltered content", Score: 0.75}}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgsWithFilter(t, "general query", nil))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Equal(t, "unfiltered content", rsp.Text)
		require.Equal(t, 0.75, rsp.Score)
		require.Contains(t, rsp.Message, "Found relevant content")
	})

	t.Run("verify declaration metadata", func(t *testing.T) {
		kb := stubKnowledge{}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		decl := searchTool.Declaration()
		require.NotEmpty(t, decl.Name)
		require.Equal(t, "knowledge_search_with_agentic_filter", decl.Name)
		require.NotEmpty(t, decl.Description)
		require.Contains(t, decl.Description, "Available filters")
		require.Contains(t, decl.Description, "category")
		require.Contains(t, decl.Description, "protocol")
		require.Contains(t, decl.Description, "level")
		require.NotNil(t, decl.InputSchema)
		require.NotNil(t, decl.OutputSchema)
	})

	t.Run("verify description generation with empty filter info", func(t *testing.T) {
		kb := stubKnowledge{}
		searchTool := NewAgenticFilterSearchTool(kb, map[string][]any{})
		decl := searchTool.Declaration()
		require.Contains(t, decl.Description, "helpful assistant")
		require.NotContains(t, decl.Description, "Available filters")
	})

	t.Run("verify options", func(t *testing.T) {
		kb := stubKnowledge{}

		// Verify WithToolName option
		customName := "custom_agentic_search"
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo, WithToolName(customName))
		decl := searchTool.Declaration()
		require.Equal(t, customName, decl.Name)

		// Verify WithToolDescription option
		customDesc := "Custom agentic description"
		searchTool = NewAgenticFilterSearchTool(kb, agenticFilterInfo, WithToolDescription(customDesc))
		decl = searchTool.Declaration()
		require.Contains(t, decl.Description, "tool description:"+customDesc)
		require.Contains(t, decl.Description, "filter info:")

		// Verify WithFilter option
		customFilter := map[string]any{"source": "internal"}
		searchTool = NewAgenticFilterSearchTool(kb, agenticFilterInfo, WithFilter(customFilter))
		decl = searchTool.Declaration()
		require.NotEmpty(t, decl.Name)

		// Verify all options together
		searchTool = NewAgenticFilterSearchTool(kb, agenticFilterInfo, WithToolName(customName), WithToolDescription(customDesc), WithFilter(customFilter))
		decl = searchTool.Declaration()
		require.Equal(t, customName, decl.Name)
		require.Contains(t, decl.Description, "tool description:"+customDesc)
	})
}

func TestGetFinalFilter(t *testing.T) {
	t.Run("merge filters with priority", func(t *testing.T) {
		agentFilter := map[string]any{
			"source": "agent",
			"common": "agent_value",
		}
		runnerFilter := map[string]any{
			"runner": "runner_value",
			"common": "runner_value", // Will be overridden by agent
		}
		invocationFilter := map[string]any{
			"invocation": "invocation_value",
			"common":     "invocation_value", // Will be overridden by runner and agent
		}

		result := getFinalFilter(agentFilter, runnerFilter, invocationFilter)

		require.Equal(t, "agent", result["source"])
		require.Equal(t, "runner_value", result["runner"])
		require.Equal(t, "invocation_value", result["invocation"])
		require.Equal(t, "agent_value", result["common"]) // Agent has highest priority (added last)
	})

	t.Run("handle nil filters", func(t *testing.T) {
		result := getFinalFilter(nil, nil, nil)
		require.Empty(t, result)
	})

	t.Run("partial nil filters", func(t *testing.T) {
		agentFilter := map[string]any{"agent": "value"}
		result := getFinalFilter(agentFilter, nil, nil)
		require.Equal(t, "value", result["agent"])
		require.Len(t, result, 1)
	})
}

func TestGenerateAgenticFilterPrompt(t *testing.T) {
	t.Run("empty filter info", func(t *testing.T) {
		prompt := generateAgenticFilterPrompt(map[string][]any{})
		require.Contains(t, prompt, "helpful assistant")
		require.NotContains(t, prompt, "Available filters")
	})

	t.Run("with filter info", func(t *testing.T) {
		filterInfo := map[string][]any{
			"category": {"doc", "tutorial"},
			"protocol": {"trpc-go", "http"},
			"empty":    {},
		}
		prompt := generateAgenticFilterPrompt(filterInfo)

		require.Contains(t, prompt, "Available filters")
		require.Contains(t, prompt, "category")
		require.Contains(t, prompt, "protocol")
		require.Contains(t, prompt, "empty")
		require.Contains(t, prompt, "Usage Rules")
		require.Contains(t, prompt, "Examples")
		require.Contains(t, prompt, "generate appropriate value")
		require.Contains(t, prompt, "choose from these options")
	})
}
