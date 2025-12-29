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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

func TestExtractKnowledgeRecall(t *testing.T) {
	tools := []*evalset.Tool{
		{
			ID:   "1",
			Name: "knowledge_search",
			Result: map[string]any{
				"documents": []*tool.DocumentResult{
					{
						Text: "golang doc",
						Metadata: map[string]any{
							"source": "kb",
							"lang":   "en",
						},
						Score: 0.9,
					},
				},
			},
		},
	}

	result, err := ExtractKnowledgeRecall(tools)
	require.NoError(t, err)
	require.Contains(t, result, `"documents"`)
	require.Contains(t, result, "golang doc")
}

func TestExtractKnowledgeRecallIgnoresNonKnowledgeTools(t *testing.T) {
	tools := []*evalset.Tool{
		{
			ID:   "1",
			Name: "other_tool",
			Result: map[string]any{
				"documents": []*tool.DocumentResult{},
			},
		},
	}
	result, err := ExtractKnowledgeRecall(tools)
	require.NoError(t, err)
	require.Empty(t, result)
}

func TestExtractKnowledgeRecallReturnsErrorOnBadPayload(t *testing.T) {
	tools := []*evalset.Tool{
		{
			ID:   "1",
			Name: "knowledge_search",
			Result: map[string]any{
				"documents": "invalid",
			},
		},
	}

	_, err := ExtractKnowledgeRecall(tools)
	require.Error(t, err)
}

func TestExtractKnowledgeRecallEmptyInput(t *testing.T) {
	result, err := ExtractKnowledgeRecall(nil)
	require.NoError(t, err)
	require.Empty(t, result)

	result, err = ExtractKnowledgeRecall([]*evalset.Tool{})
	require.NoError(t, err)
	require.Empty(t, result)
}

func TestExtractKnowledgeRecallNilResponses(t *testing.T) {
	tools := []*evalset.Tool{
		{
			ID:     "1",
			Name:   "knowledge_search_with_agentic_filter",
			Result: map[string]any{},
		},
	}
	result, err := ExtractKnowledgeRecall(tools)
	require.NoError(t, err)
	require.NotEmpty(t, result)
	assert.Contains(t, result, `"documents":null`)
}
