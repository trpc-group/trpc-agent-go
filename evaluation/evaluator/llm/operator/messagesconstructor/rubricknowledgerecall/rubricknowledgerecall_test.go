//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rubricknowledgerecall

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestConstructMessagesWithKnowledge(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent: &model.Message{Content: "who?"},
		Tools: []*evalset.Tool{
			{
				ID:   "1",
				Name: "knowledge_search",
				Result: map[string]any{
					"documents": []map[string]any{
						{
							"text":  "result",
							"score": 0.9,
						},
					},
				},
			},
		},
	}
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{
				Rubrics: []*llm.Rubric{{ID: "r1", Content: &llm.RubricContent{Text: "rubric"}}},
			},
		},
	}

	messages, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, nil, evalMetric)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, model.RoleUser, messages[0].Role)
	assert.Contains(t, messages[0].Content, "result")
	assert.Contains(t, messages[0].Content, "who?")
	assert.Contains(t, messages[0].Content, "rubric")
}

func TestConstructMessagesNoKnowledgeFound(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:       &model.Message{Content: "question"},
		FinalResponse:     nil,
		InvocationID:      "id",
		CreationTimestamp: nil,
	}
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{},
		},
	}

	messages, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, nil, evalMetric)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Content, "No knowledge search results were found.")
}

func TestConstructMessagesKnowledgeError(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent: &model.Message{Content: "question"},
		Tools: []*evalset.Tool{
			{
				ID:   "1",
				Name: "knowledge_search",
				Result: map[string]any{
					"documents": "bad",
				},
			},
		},
	}
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{},
		},
	}

	_, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, nil, evalMetric)
	require.Error(t, err)
}
