//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package content

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
)

func TestExtractKnowledgeRecall(t *testing.T) {
	intermediate := &evalset.IntermediateData{
		ToolResponses: []*genai.FunctionResponse{
			{
				Name: "knowledge_search",
				Response: map[string]any{
					"documents": []map[string]any{
						{
							"text":     "golang doc",
							"metadata": map[string]any{"source": "kb", "lang": "en"},
							"score":    0.9,
						},
					},
				},
			},
		},
	}

	result, err := ExtractKnowledgeRecall(intermediate)
	require.NoError(t, err)
	require.Contains(t, result, `"documents"`)
	require.Contains(t, result, "golang doc")
}

func TestExtractKnowledgeRecallIgnoresNonKnowledgeTools(t *testing.T) {
	intermediate := &evalset.IntermediateData{
		ToolResponses: []*genai.FunctionResponse{
			{
				Name:     "other_tool",
				Response: map[string]any{"documents": []any{}},
			},
		},
	}

	result, err := ExtractKnowledgeRecall(intermediate)
	require.NoError(t, err)
	require.Empty(t, result)
}

func TestExtractKnowledgeRecallReturnsErrorOnBadPayload(t *testing.T) {
	intermediate := &evalset.IntermediateData{
		ToolResponses: []*genai.FunctionResponse{
			{
				Name: "knowledge_search",
				Response: map[string]any{
					"documents": "invalid",
				},
			},
		},
	}

	_, err := ExtractKnowledgeRecall(intermediate)
	require.Error(t, err)
}

func TestExtractKnowledgeRecallEmptyInput(t *testing.T) {
	result, err := ExtractKnowledgeRecall(nil)
	require.NoError(t, err)
	require.Empty(t, result)

	result, err = ExtractKnowledgeRecall(&evalset.IntermediateData{ToolResponses: nil})
	require.NoError(t, err)
	require.Empty(t, result)
}

func TestExtractKnowledgeRecallSanitizeNonText(t *testing.T) {
	var ctrl strings.Builder
	for range 10 {
		ctrl.WriteRune('\u0001')
	}
	intermediate := &evalset.IntermediateData{
		ToolResponses: []*genai.FunctionResponse{
			{
				Name: "knowledge_search",
				Response: map[string]any{
					"documents": []map[string]any{
						{
							"text":     ctrl.String(),
							"metadata": map[string]any{},
							"score":    0.1,
						},
					},
				},
			},
		},
	}
	result, err := ExtractKnowledgeRecall(intermediate)
	require.NoError(t, err)
	require.Contains(t, result, "[non-text content omitted]")
}

func TestExtractTextFromContent(t *testing.T) {
	require.Equal(t, "", ExtractTextFromContent(nil))
	content := &genai.Content{Parts: []*genai.Part{{Text: "hello "}, {Text: "world"}}}
	require.Equal(t, "hello world", ExtractTextFromContent(content))
}

func TestExtractIntermediateData(t *testing.T) {
	data, err := ExtractIntermediateData(&evalset.IntermediateData{IntermediateResponses: [][]any{{"role", "message"}}})
	require.NoError(t, err)
	require.Contains(t, data, "role")
	require.Contains(t, data, "message")
}

func TestExtractRubrics(t *testing.T) {
	rubrics := []*llm.Rubric{
		{ID: "1", Content: &llm.RubricContent{Text: "first"}},
		{ID: "2", Content: &llm.RubricContent{Text: "second"}},
	}
	text := ExtractRubrics(rubrics)
	require.Contains(t, text, "1: first")
	require.Contains(t, text, "2: second")
	require.Equal(t, "", ExtractRubrics(nil))
}

func TestExtractKnowledgeRecallIgnoresNilResponses(t *testing.T) {
	intermediate := &evalset.IntermediateData{
		ToolResponses: []*genai.FunctionResponse{
			nil,
			{Name: "knowledge_search_with_agentic_filter", Response: nil},
		},
	}
	result, err := ExtractKnowledgeRecall(intermediate)
	require.NoError(t, err)
	require.Empty(t, result)
}
