//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hallucination

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestScoreBasedOnResponse(t *testing.T) {
	scorer := New()
	response := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{Content: `
ID: 1
Reason: The tool output states Cloudy.
Label: supported
Verdict: yes

ID: 2
Reason: The tool output says 18C.
Label: contradictory
Verdict: no

ID: 3
Reason: This is a greeting.
Label: not_applicable
Verdict: yes
`},
		}},
	}
	result, err := scorer.ScoreBasedOnResponse(context.Background(), response, nil)
	require.NoError(t, err)
	require.Len(t, result.RubricScores, 3)
	assert.InDelta(t, 2.0/3.0, result.Score, 1e-9)
	assert.Equal(t, "1", result.RubricScores[0].ID)
	assert.Equal(t, 1.0, result.RubricScores[0].Score)
	assert.Equal(t, 0.0, result.RubricScores[1].Score)
	assert.Contains(t, result.Reason, "[supported]")
	assert.Contains(t, result.Reason, "[contradictory]")
	assert.Contains(t, result.Reason, "[not_applicable]")
}

func TestScoreBasedOnResponseFallsBackToVerdict(t *testing.T) {
	scorer := New()
	response := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{Content: `
ID: 1
Reason: The evidence supports the sentence.
Label:
Verdict: yes
`},
		}},
	}
	result, err := scorer.ScoreBasedOnResponse(context.Background(), response, nil)
	require.NoError(t, err)
	require.Len(t, result.RubricScores, 1)
	assert.Equal(t, 1.0, result.Score)
}

func TestScoreBasedOnResponseFallsBackToVerdictNo(t *testing.T) {
	scorer := New()
	response := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{Content: `
ID:
Reason: The tool output does not support the sentence.
Label:
Verdict: no
`},
		}},
	}
	result, err := scorer.ScoreBasedOnResponse(context.Background(), response, nil)
	require.NoError(t, err)
	require.Len(t, result.RubricScores, 1)
	assert.Equal(t, "1", result.RubricScores[0].ID)
	assert.Equal(t, 0.0, result.RubricScores[0].Score)
	assert.Equal(t, 0.0, result.Score)
}

func TestScoreBasedOnResponseUnexpectedLabel(t *testing.T) {
	scorer := New()
	response := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{Content: `
ID: 1
Reason: Unknown.
Label: maybe
Verdict: yes
`},
		}},
	}
	_, err := scorer.ScoreBasedOnResponse(context.Background(), response, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected label")
}

func TestScoreBasedOnResponseRequiresBlocks(t *testing.T) {
	scorer := New()
	_, err := scorer.ScoreBasedOnResponse(context.Background(), &model.Response{
		Choices: []model.Choice{{Message: model.Message{Content: "no structured output"}}},
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no sentence blocks found")
}

func TestScoreBasedOnResponseRequiresResponse(t *testing.T) {
	scorer := New()
	_, err := scorer.ScoreBasedOnResponse(context.Background(), nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response is nil")
}

func TestScoreBasedOnResponseRequiresChoices(t *testing.T) {
	scorer := New()
	_, err := scorer.ScoreBasedOnResponse(context.Background(), &model.Response{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no choices in response")
}
