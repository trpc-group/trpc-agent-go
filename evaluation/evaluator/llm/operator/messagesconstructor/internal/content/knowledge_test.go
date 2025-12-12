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

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
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
	require.Contains(t, result, "knowledge_search")
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
