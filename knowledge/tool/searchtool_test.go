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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
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

func marshalArgsWithFilter(t *testing.T, query string, filter *searchfilter.UniversalFilterCondition) []byte {
	t.Helper()
	bts, err := json.Marshal(&KnowledgeSearchRequestWithFilter{Query: query, Filter: filter})
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
		kb := stubKnowledge{result: &knowledge.SearchResult{
			Documents: []*knowledge.Result{
				{
					Document: &document.Document{Content: "foo", Metadata: map[string]any{"source": "test"}},
					Score:    0.9,
				},
			},
		}}
		searchTool := NewKnowledgeSearchTool(kb)
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, "hello"))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Len(t, rsp.Documents, 1)
		require.Equal(t, "foo", rsp.Documents[0].Text)
		require.Equal(t, 0.9, rsp.Documents[0].Score)
		require.Equal(t, "test", rsp.Documents[0].Metadata["source"])
		require.Contains(t, rsp.Message, "Found 1 relevant document")
	})

	t.Run("success with multiple documents", func(t *testing.T) {
		kb := stubKnowledge{result: &knowledge.SearchResult{
			Documents: []*knowledge.Result{
				{
					Document: &document.Document{Content: "first result", Metadata: map[string]any{"rank": 1}},
					Score:    0.95,
				},
				{
					Document: &document.Document{Content: "second result", Metadata: map[string]any{"rank": 2}},
					Score:    0.85,
				},
				{
					Document: &document.Document{Content: "third result", Metadata: map[string]any{"rank": 3}},
					Score:    0.75,
				},
			},
		}}
		searchTool := NewKnowledgeSearchTool(kb)
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, "hello"))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Len(t, rsp.Documents, 3)
		require.Equal(t, "first result", rsp.Documents[0].Text)
		require.Equal(t, 0.95, rsp.Documents[0].Score)
		require.Equal(t, "second result", rsp.Documents[1].Text)
		require.Equal(t, 0.85, rsp.Documents[1].Score)
		require.Equal(t, "third result", rsp.Documents[2].Text)
		require.Equal(t, 0.75, rsp.Documents[2].Score)
		require.Contains(t, rsp.Message, "Found 3 relevant document")
	})

	t.Run("filters internal metadata", func(t *testing.T) {
		kb := stubKnowledge{result: &knowledge.SearchResult{
			Documents: []*knowledge.Result{
				{
					Document: &document.Document{
						Content: "test content",
						Metadata: map[string]any{
							"public_key":              "public_value",
							"trpc_agent_go_source":    "internal_source",
							"trpc_agent_go_file_path": "/path/to/file",
							"custom_field":            "custom_value",
						},
					},
					Score: 0.9,
				},
			},
		}}
		searchTool := NewKnowledgeSearchTool(kb)
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, "hello"))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Len(t, rsp.Documents, 1)
		// Should include public metadata
		require.Contains(t, rsp.Documents[0].Metadata, "public_key")
		require.Contains(t, rsp.Documents[0].Metadata, "custom_field")
		// Should NOT include internal metadata with trpc_agent_go_ prefix
		require.NotContains(t, rsp.Documents[0].Metadata, "trpc_agent_go_source")
		require.NotContains(t, rsp.Documents[0].Metadata, "trpc_agent_go_file_path")
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

		// Verify WithMaxResults option
		ttool = NewKnowledgeSearchTool(kb, WithMaxResults(5))
		decl = ttool.Declaration()
		require.NotEmpty(t, decl.Name)

		// Verify all options together
		ttool = NewKnowledgeSearchTool(kb, WithToolName(customName), WithToolDescription(customDesc), WithFilter(customFilter), WithMaxResults(10))
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

	t.Run("empty query and filter", func(t *testing.T) {
		kb := stubKnowledge{}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgsWithFilter(t, "", nil))
		require.Error(t, err)
		require.Contains(t, err.Error(), "at least one of query or filter must be provided")
	})

	t.Run("search error", func(t *testing.T) {
		kb := stubKnowledge{err: errors.New("search failed")}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		filter := &searchfilter.UniversalFilterCondition{Field: "category", Operator: "eq", Value: "documentation"}
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgsWithFilter(t, "hello", filter))
		require.Error(t, err)
		require.Contains(t, err.Error(), "search failed")
	})

	t.Run("no result", func(t *testing.T) {
		kb := stubKnowledge{}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		filter := &searchfilter.UniversalFilterCondition{Field: "category", Operator: "eq", Value: "documentation"}
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgsWithFilter(t, "hello", filter))
		require.Error(t, err)
		require.Contains(t, err.Error(), "no relevant information found")
	})

	t.Run("success with single filter", func(t *testing.T) {
		kb := stubKnowledge{result: &knowledge.SearchResult{
			Documents: []*knowledge.Result{
				{
					Document: &document.Document{Content: "filtered content", Metadata: map[string]any{"category": "documentation"}},
					Score:    0.85,
				},
			},
		}}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		filter := &searchfilter.UniversalFilterCondition{Field: "category", Operator: "eq", Value: "documentation"}
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgsWithFilter(t, "hello", filter))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Len(t, rsp.Documents, 1)
		require.Equal(t, "filtered content", rsp.Documents[0].Text)
		require.Equal(t, 0.85, rsp.Documents[0].Score)
		require.Contains(t, rsp.Message, "Found 1 relevant document")
	})

	t.Run("success with AND filter", func(t *testing.T) {
		kb := stubKnowledge{result: &knowledge.SearchResult{
			Documents: []*knowledge.Result{
				{
					Document: &document.Document{Content: "multi-filtered content"},
					Score:    0.92,
				},
			},
		}}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		filter := &searchfilter.UniversalFilterCondition{
			Operator: "and",
			Value: []*searchfilter.UniversalFilterCondition{
				{Field: "category", Operator: "eq", Value: "documentation"},
				{Field: "protocol", Operator: "eq", Value: "trpc-go"},
				{Field: "level", Operator: "eq", Value: "intermediate"},
			},
		}
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgsWithFilter(t, "trpc gateway", filter))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Len(t, rsp.Documents, 1)
		require.Equal(t, "multi-filtered content", rsp.Documents[0].Text)
		require.Equal(t, 0.92, rsp.Documents[0].Score)
		require.Contains(t, rsp.Message, "Found 1 relevant document")
	})

	t.Run("success with no filters", func(t *testing.T) {
		kb := stubKnowledge{result: &knowledge.SearchResult{
			Documents: []*knowledge.Result{
				{
					Document: &document.Document{Content: "unfiltered content"},
					Score:    0.75,
				},
			},
		}}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgsWithFilter(t, "general query", nil))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Len(t, rsp.Documents, 1)
		require.Equal(t, "unfiltered content", rsp.Documents[0].Text)
		require.Equal(t, 0.75, rsp.Documents[0].Score)
		require.Contains(t, rsp.Message, "Found 1 relevant document")
	})

	t.Run("verify declaration metadata", func(t *testing.T) {
		kb := stubKnowledge{}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		decl := searchTool.Declaration()
		require.NotEmpty(t, decl.Name)
		require.Equal(t, "knowledge_search_with_agentic_filter", decl.Name)
		require.NotEmpty(t, decl.Description)
		require.Contains(t, decl.Description, "Available metadata filters")
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

	t.Run("filters internal metadata", func(t *testing.T) {
		kb := stubKnowledge{result: &knowledge.SearchResult{
			Documents: []*knowledge.Result{
				{
					Document: &document.Document{
						Content: "test content",
						Metadata: map[string]any{
							"category":                "documentation",
							"trpc_agent_go_source":    "internal_source",
							"trpc_agent_go_file_name": "test.md",
							"author":                  "test_author",
						},
					},
					Score: 0.88,
				},
			},
		}}
		searchTool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
		filter := &searchfilter.UniversalFilterCondition{Field: "category", Operator: "eq", Value: "documentation"}
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgsWithFilter(t, "hello", filter))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Len(t, rsp.Documents, 1)
		// Should include public metadata
		require.Contains(t, rsp.Documents[0].Metadata, "category")
		require.Contains(t, rsp.Documents[0].Metadata, "author")
		// Should NOT include internal metadata with trpc_agent_go_ prefix
		require.NotContains(t, rsp.Documents[0].Metadata, "trpc_agent_go_source")
		require.NotContains(t, rsp.Documents[0].Metadata, "trpc_agent_go_file_name")
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

func TestConvertMetadataToFilterCondition(t *testing.T) {
	t.Run("convert single metadata", func(t *testing.T) {
		metadata := map[string]any{"category": "doc"}
		result := convertMetadataToFilterCondition(metadata)

		require.NotNil(t, result)
		require.Equal(t, "category", result.Field)
		require.Equal(t, searchfilter.OperatorEqual, result.Operator)
		require.Equal(t, "doc", result.Value)
	})

	t.Run("convert multiple metadata with AND", func(t *testing.T) {
		metadata := map[string]any{
			"category": "doc",
			"source":   "official",
		}
		result := convertMetadataToFilterCondition(metadata)

		require.NotNil(t, result)
		require.Equal(t, searchfilter.OperatorAnd, result.Operator)
		conditions := result.Value.([]*searchfilter.UniversalFilterCondition)
		require.Len(t, conditions, 2)
	})

	t.Run("handle empty metadata", func(t *testing.T) {
		result := convertMetadataToFilterCondition(nil)
		require.Nil(t, result)

		result = convertMetadataToFilterCondition(map[string]any{})
		require.Nil(t, result)
	})
}

func TestMergeFilterConditions(t *testing.T) {
	t.Run("merge with priority - agent > runner", func(t *testing.T) {
		// Agent level filter (highest priority)
		agentCondition := &searchfilter.UniversalFilterCondition{
			Field:    "source",
			Operator: searchfilter.OperatorEqual,
			Value:    "agent",
		}

		// Runner level filter (medium priority)
		runnerCondition := &searchfilter.UniversalFilterCondition{
			Field:    "region",
			Operator: searchfilter.OperatorEqual,
			Value:    "china",
		}

		result := mergeFilterConditions(agentCondition, runnerCondition)

		require.NotNil(t, result)
		require.Equal(t, searchfilter.OperatorAnd, result.Operator)
		conditions := result.Value.([]*searchfilter.UniversalFilterCondition)
		require.Len(t, conditions, 2)
		// Agent condition comes first (higher priority)
		require.Equal(t, "source", conditions[0].Field)
		require.Equal(t, "region", conditions[1].Field)
	})

	t.Run("handle nil filters", func(t *testing.T) {
		result := mergeFilterConditions(nil, nil)
		require.Nil(t, result)
	})

	t.Run("single non-nil filter", func(t *testing.T) {
		condition := &searchfilter.UniversalFilterCondition{
			Field:    "category",
			Operator: searchfilter.OperatorEqual,
			Value:    "doc",
		}
		result := mergeFilterConditions(condition, nil)
		require.NotNil(t, result)
		require.Equal(t, "category", result.Field)
	})
}

func TestGenerateAgenticFilterPrompt(t *testing.T) {
	t.Run("empty filter info", func(t *testing.T) {
		prompt := generateAgenticFilterPrompt(map[string][]any{})
		require.Contains(t, prompt, "helpful assistant")
		require.NotContains(t, prompt, "Available metadata filters")
	})

	t.Run("with filter info", func(t *testing.T) {
		filterInfo := map[string][]any{
			"category": {"doc", "tutorial"},
			"protocol": {"trpc-go", "http"},
			"empty":    {},
		}
		prompt := generateAgenticFilterPrompt(filterInfo)

		// Check for new prompt structure
		require.Contains(t, prompt, "Available metadata filters")
		require.Contains(t, prompt, "category")
		require.Contains(t, prompt, "protocol")
		require.Contains(t, prompt, "empty")

		// Check for filter usage sections
		require.Contains(t, prompt, "Filter Usage")
		require.Contains(t, prompt, "filter")

		// Check for operator information
		require.Contains(t, prompt, "eq")
		require.Contains(t, prompt, "or")
		require.Contains(t, prompt, "and")

		// Check for examples with double quotes
		require.Contains(t, prompt, "Filter Examples")
		require.Contains(t, prompt, `"field"`)
		require.Contains(t, prompt, `"operator"`)
		require.Contains(t, prompt, `"value"`)

		// Check for separated value sections
		require.Contains(t, prompt, "Fields with predefined values")
		require.Contains(t, prompt, "Fields accepting any value")
	})

	t.Run("verify prompt format", func(t *testing.T) {
		filterInfo := map[string][]any{
			"category": {"doc", "tutorial"},
			"status":   {"active"},
			"tag":      {},
		}
		prompt := generateAgenticFilterPrompt(filterInfo)

		// Print for manual inspection
		t.Logf("Generated prompt:\n%s", prompt)

		// Verify double quotes in examples
		require.Contains(t, prompt, `"field":`)
		require.Contains(t, prompt, `"operator":`)
		require.Contains(t, prompt, `"value":`)

		// Verify sections are separated
		require.Contains(t, prompt, "Fields with predefined values (use exact values only):")
		require.Contains(t, prompt, "Fields accepting any value:")

		// Verify structure: fields with values listed first, then fields without values
		require.Contains(t, prompt, "category:")
		require.Contains(t, prompt, "status:")
		require.Contains(t, prompt, "- tag")
	})
}

func TestAgenticFilterSearchToolWithAdvancedFilter(t *testing.T) {
	t.Run("successful search with simple filter", func(t *testing.T) {
		kb := stubKnowledge{
			result: &knowledge.SearchResult{
				Documents: []*knowledge.Result{
					{
						Document: &document.Document{Content: "test result"},
						Score:    0.95,
					},
				},
			},
		}

		tool := NewAgenticFilterSearchTool(kb, map[string][]any{
			"status": {"active", "inactive"},
		})
		require.NotNil(t, tool)

		req := &KnowledgeSearchRequestWithFilter{
			Query: "test query",
			Filter: &searchfilter.UniversalFilterCondition{
				Field:    "status",
				Operator: "eq",
				Value:    "active",
			},
		}

		args, err := json.Marshal(req)
		require.NoError(t, err)

		result, err := tool.(ctool.CallableTool).Call(context.Background(), args)
		require.NoError(t, err)

		resp := result.(*KnowledgeSearchResponse)
		require.Len(t, resp.Documents, 1)
		require.Equal(t, "test result", resp.Documents[0].Text)
		require.Equal(t, 0.95, resp.Documents[0].Score)
	})

	t.Run("successful search with AND filter", func(t *testing.T) {
		kb := stubKnowledge{
			result: &knowledge.SearchResult{
				Documents: []*knowledge.Result{
					{
						Document: &document.Document{Content: "filtered result"},
						Score:    0.88,
					},
				},
			},
		}

		tool := NewAgenticFilterSearchTool(kb, map[string][]any{
			"status": {"active", "inactive"},
			"age":    {},
		})

		req := &KnowledgeSearchRequestWithFilter{
			Query: "test query",
			Filter: &searchfilter.UniversalFilterCondition{
				Operator: "and",
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Field:    "status",
						Operator: "eq",
						Value:    "active",
					},
					{
						Field:    "age",
						Operator: "gt",
						Value:    float64(18),
					},
				},
			},
		}

		args, err := json.Marshal(req)
		require.NoError(t, err)

		result, err := tool.(ctool.CallableTool).Call(context.Background(), args)
		require.NoError(t, err)

		resp := result.(*KnowledgeSearchResponse)
		require.Len(t, resp.Documents, 1)
		require.Equal(t, "filtered result", resp.Documents[0].Text)
	})

	t.Run("successful search with OR filter", func(t *testing.T) {
		kb := stubKnowledge{
			result: &knowledge.SearchResult{
				Documents: []*knowledge.Result{
					{
						Document: &document.Document{Content: "or result"},
						Score:    0.75,
					},
				},
			},
		}

		tool := NewAgenticFilterSearchTool(kb, map[string][]any{
			"category": {"A", "B", "C"},
		})

		req := &KnowledgeSearchRequestWithFilter{
			Query: "test query",
			Filter: &searchfilter.UniversalFilterCondition{
				Operator: "or",
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Field:    "category",
						Operator: "eq",
						Value:    "A",
					},
					{
						Field:    "category",
						Operator: "eq",
						Value:    "B",
					},
				},
			},
		}

		args, err := json.Marshal(req)
		require.NoError(t, err)

		result, err := tool.(ctool.CallableTool).Call(context.Background(), args)
		require.NoError(t, err)

		resp := result.(*KnowledgeSearchResponse)
		require.Len(t, resp.Documents, 1)
		require.Equal(t, "or result", resp.Documents[0].Text)
	})

	t.Run("empty query error", func(t *testing.T) {
		kb := stubKnowledge{}
		tool := NewAgenticFilterSearchTool(kb, map[string][]any{})

		req := &KnowledgeSearchRequestWithFilter{
			Query: "",
		}

		args, err := json.Marshal(req)
		require.NoError(t, err)

		_, err = tool.(ctool.CallableTool).Call(context.Background(), args)
		require.Error(t, err)
		require.Contains(t, err.Error(), "at least one of query or filter must be provided")
	})

	t.Run("search error", func(t *testing.T) {
		kb := stubKnowledge{
			err: errors.New("search failed"),
		}
		tool := NewAgenticFilterSearchTool(kb, map[string][]any{})

		req := &KnowledgeSearchRequestWithFilter{
			Query: "test",
		}

		args, err := json.Marshal(req)
		require.NoError(t, err)

		_, err = tool.(ctool.CallableTool).Call(context.Background(), args)
		require.Error(t, err)
		require.Contains(t, err.Error(), "search failed")
	})
}
