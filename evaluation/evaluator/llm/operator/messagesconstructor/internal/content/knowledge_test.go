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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestExtractKnowledgeRecall(t *testing.T) {
	intermediate := &evalset.IntermediateData{
		ToolResponses: []*model.Message{
			{
				ToolID:   "1",
				ToolName: "knowledge_search",
				Content:  "{\"documents\": [{\"text\": \"golang doc\", \"metadata\": {\"source\": \"kb\", \"lang\": \"en\"}, \"score\": 0.9}]}",
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
		ToolResponses: []*model.Message{
			{
				ToolID:   "1",
				ToolName: "other_tool",
				Content:  "{\"documents\": []}",
			},
		},
	}

	result, err := ExtractKnowledgeRecall(intermediate)
	require.NoError(t, err)
	require.Empty(t, result)
}

func TestExtractKnowledgeRecallReturnsErrorOnBadPayload(t *testing.T) {
	intermediate := &evalset.IntermediateData{
		ToolResponses: []*model.Message{
			{
				ToolID:   "1",
				ToolName: "knowledge_search",
				Content:  "{\"documents\": \"invalid\"}",
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

func TestExtractKnowledgeRecallNilResponses(t *testing.T) {
	intermediate := &evalset.IntermediateData{
		ToolResponses: []*model.Message{
			{ToolID: "1", ToolName: "knowledge_search_with_agentic_filter", Content: ""},
		},
	}
	result, err := ExtractKnowledgeRecall(intermediate)
	require.Error(t, err)
	require.Empty(t, result)
}
