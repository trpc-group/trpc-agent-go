//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rubricscores

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestScoreBasedOnResponseParsesRubricScores(t *testing.T) {
	scorer := New()

	result, err := scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{
		"rubricScores":[
			{"id":"1","score":1,"reason":"Correct."},
			{"id":"2","score":0,"reason":"Missing evidence."}
		]
	}`), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0.5, result.Score)
	assert.Len(t, result.RubricScores, 2)
	assert.Equal(t, "Correct.\nMissing evidence.", result.Reason)
}

func TestScoreBasedOnResponseValidatesRubricScoreIDs(t *testing.T) {
	scorer := New()
	evalMetric := metricWithRubrics("1", "2")
	result, err := scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{
		"rubricScores":[
			{"id":"1","score":1,"reason":"Correct."},
			{"id":"2","score":0,"reason":"Missing evidence."}
		]
	}`), evalMetric)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0.5, result.Score)
	_, err = scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{
		"rubricScores":[
			{"id":"1","score":1,"reason":"Correct."},
			{"id":"1","score":0,"reason":"Duplicate."}
		]
	}`), evalMetric)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate rubric score id "1"`)
	_, err = scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{
		"rubricScores":[
			{"id":"1","score":1,"reason":"Correct."},
			{"id":"3","score":0,"reason":"Unknown."}
		]
	}`), evalMetric)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unexpected rubric score id "3"`)
	_, err = scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{
		"rubricScores":[
			{"id":"1","score":1,"reason":"Correct."}
		]
	}`), evalMetric)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing rubric score id")
}

func TestScoreBasedOnResponseRejectsInvalidRubricScores(t *testing.T) {
	scorer := New()
	_, err := scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"rubricScores":[]}`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rubricScores is empty")
	_, err = scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"rubricScores":[{"id":"","score":1,"reason":"bad"}]}`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rubric score id is empty")
	_, err = scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"rubricScores":[{"id":"1","reason":"bad"}]}`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rubric score is required")
	_, err = scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"rubricScores":[{"id":"1","score":1}]}`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rubric score reason is required")
	_, err = scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"rubricScores":[{"id":"1","score":2,"reason":"bad"}]}`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rubric score must be between 0 and 1")
	_, err = scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`not-json`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal response json")
}

func makeResponse(content string) *model.Response {
	return &model.Response{
		Choices: []model.Choice{{Message: model.Message{Content: content}}},
	}
}

func metricWithRubrics(ids ...string) *metric.EvalMetric {
	rubrics := make([]*llm.Rubric, 0, len(ids))
	for _, id := range ids {
		rubrics = append(rubrics, &llm.Rubric{
			ID:      id,
			Content: &llm.RubricContent{Text: "rubric " + id},
		})
	}
	return &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{Rubrics: rubrics},
		},
	}
}
