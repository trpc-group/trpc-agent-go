//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package verifierpairwise

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestConstructMessages(t *testing.T) {
	constructor := New()
	messages, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{
		{
			UserContent:   messagePtr(model.NewUserMessage("question")),
			FinalResponse: messagePtr(model.NewAssistantMessage("candidate A")),
		},
	}, []*evalset.Invocation{
		{
			FinalResponse: messagePtr(model.NewAssistantMessage("candidate B")),
		},
	}, verifierMetric())
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, model.RoleUser, messages[0].Role)
	assert.Contains(t, messages[0].Content, "Candidate A")
	assert.Contains(t, messages[0].Content, "candidate A")
	assert.Contains(t, messages[0].Content, "Candidate B")
	assert.Contains(t, messages[0].Content, "candidate B")
	assert.Contains(t, messages[0].Content, "accuracy")
	assert.Contains(t, messages[0].Content, "Score Candidate A and Candidate B independently")
	assert.Contains(t, messages[0].Content, "<score_A>LETTER_A_TO_T</score_A>")
	assert.Contains(t, messages[0].Content, "<score_B>LETTER_A_TO_T</score_B>")
	assert.Contains(t, messages[0].Content, "every earlier letter is better than every later letter")
	assert.Contains(t, messages[0].Content, "- B-D =")
	assert.Contains(t, messages[0].Content, "- E-G =")
}

func TestConstructMessagesRequiresRubrics(t *testing.T) {
	constructor := New()
	metric := verifierMetric()
	metric.Criterion.LLMJudge.Rubrics = nil
	_, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{
		{
			UserContent:   messagePtr(model.NewUserMessage("question")),
			FinalResponse: messagePtr(model.NewAssistantMessage("candidate A")),
		},
	}, []*evalset.Invocation{
		{
			FinalResponse: messagePtr(model.NewAssistantMessage("candidate B")),
		},
	}, metric)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm judge rubrics are required")
}

func TestConstructMessagesRejectsInvalidInputs(t *testing.T) {
	constructor := New()
	_, err := constructor.ConstructMessages(context.Background(), nil, nil, verifierMetric())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "actuals is empty")
	_, err = constructor.ConstructMessages(context.Background(), []*evalset.Invocation{{}}, nil, verifierMetric())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expecteds is empty")
	_, err = constructor.ConstructMessages(context.Background(), []*evalset.Invocation{{}}, []*evalset.Invocation{{}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval metric is nil")
	_, err = constructor.ConstructMessages(context.Background(), []*evalset.Invocation{{}}, []*evalset.Invocation{{}}, &metric.EvalMetric{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm judge criterion is required")
	_, err = constructor.ConstructMessages(context.Background(), []*evalset.Invocation{nil}, []*evalset.Invocation{{}}, verifierMetric())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "actual invocation is nil")
	_, err = constructor.ConstructMessages(context.Background(), []*evalset.Invocation{{}}, []*evalset.Invocation{nil}, verifierMetric())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected invocation is nil")
	_, err = constructor.ConstructMessages(context.Background(), []*evalset.Invocation{{}}, []*evalset.Invocation{{}}, verifierMetric())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected final response is required")
}

func verifierMetric() *metric.EvalMetric {
	return &metric.EvalMetric{
		Threshold: 0.5,
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Rubrics: []*criterionllm.Rubric{
					{
						ID:      "accuracy",
						Content: &criterionllm.RubricContent{Text: "Prefer the more accurate answer."},
					},
				},
			},
		},
	}
}

func messagePtr(message model.Message) *model.Message {
	return &message
}
