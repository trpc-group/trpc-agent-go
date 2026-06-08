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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestVerifierEvaluatorConstructMessages(t *testing.T) {
	ev := New().(*verifierEvaluator)
	messages, err := ev.ConstructMessages(context.Background(), []*evalset.Invocation{
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
	assert.Contains(t, messages[0].Content, "Candidate A")
	assert.Contains(t, messages[0].Content, "candidate A")
	assert.Contains(t, messages[0].Content, "Candidate B")
	assert.Contains(t, messages[0].Content, "candidate B")
	assert.Contains(t, messages[0].Content, "accuracy")
	assert.Contains(t, messages[0].Content, "Score Candidate A and Candidate B independently")
}

func TestVerifierEvaluatorStructuredOutput(t *testing.T) {
	ev := New().(*verifierEvaluator)
	out, err := ev.StructuredOutput(context.Background(), nil, nil, verifierMetric())
	require.NoError(t, err)
	assert.Nil(t, out)
}

func TestVerifierEvaluatorScoreBasedOnResponseUsesLogprobs(t *testing.T) {
	ev := New().(*verifierEvaluator)
	result, err := ev.ScoreBasedOnResponse(context.Background(), &model.Response{
		Choices: []model.Choice{
			{
				Message: model.NewAssistantMessage("<score_A>A</score_A>\n<score_B>T</score_B>"),
				Logprobs: &model.Logprobs{
					Content: []model.TokenLogprob{
						{Token: "analysis\n<score_A>"},
						{
							Token:   "A",
							Logprob: math.Log(0.7),
							TopLogprobs: []model.TopLogprob{
								{Token: "A", Logprob: math.Log(0.7)},
								{Token: "T", Logprob: math.Log(0.3)},
							},
						},
						{Token: "</score_A>\n<score_B>"},
						{
							Token:   "T",
							Logprob: math.Log(0.8),
							TopLogprobs: []model.TopLogprob{
								{Token: "T", Logprob: math.Log(0.8)},
								{Token: "A", Logprob: math.Log(0.2)},
							},
						},
					},
				},
			},
		},
	}, verifierMetric())
	require.NoError(t, err)
	assert.InDelta(t, 0.75, result.Score, 1e-9)
	assert.Contains(t, result.Reason, "score_A")
}

func TestVerifierEvaluatorScoreBasedOnResponseRequiresLogprobs(t *testing.T) {
	ev := New().(*verifierEvaluator)
	_, err := ev.ScoreBasedOnResponse(context.Background(), &model.Response{
		Choices: []model.Choice{{Message: model.NewAssistantMessage("<score_A>A</score_A>\n<score_B>T</score_B>")}},
	}, verifierMetric())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logprobs are missing")
}

func TestVerifierEvaluatorAggregatesThroughLLMBase(t *testing.T) {
	ev := New().(*verifierEvaluator)
	result, err := ev.AggregateInvocations(context.Background(), []*evaluator.PerInvocationResult{
		{Score: 0.75},
		{Score: 0.25},
	}, &metric.EvalMetric{Threshold: 0.5})
	require.NoError(t, err)
	assert.Equal(t, 0.5, result.OverallScore)
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
