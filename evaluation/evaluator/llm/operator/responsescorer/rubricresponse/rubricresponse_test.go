//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rubricresponse

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestScoreBasedOnResponseParsesBlocks(t *testing.T) {
	scorer := New()
	response := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{Content: `
ID: 1
Rubric: p1
Evidence: e1
Reason: r1
Verdict: yes

ID: 2
Rubric: p2
Evidence: e2
Reason: r2
Verdict: no
`},
			},
		},
	}

	result, err := scorer.ScoreBasedOnResponse(context.Background(), response, nil)
	require.NoError(t, err)
	require.Len(t, result.RubricScores, 2)
	assert.InDelta(t, 0.5, result.Score, 1e-9)
	assert.Equal(t, "1", result.RubricScores[0].ID)
	assert.Equal(t, "r1", result.RubricScores[0].Reason)
	assert.InDelta(t, 1.0, result.RubricScores[0].Score, 1e-9)
}

func TestScoreBasedOnResponseNoBlocks(t *testing.T) {
	scorer := New()
	_, err := scorer.ScoreBasedOnResponse(context.Background(), &model.Response{
		Choices: []model.Choice{{Message: model.Message{Content: "no rubric content"}}},
	}, nil)
	require.Error(t, err)
}

func TestScoreBasedOnResponseKeepsAllBlocks(t *testing.T) {
	scorer := New()
	response := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{Content: `
ID: 1
Rubric: alpha
Evidence: e1
Reason: r1
Verdict: yes

ID: 2
Rubric: beta
Evidence: e2
Reason: r2
Verdict: no

ID: 3
Rubric: gamma
Evidence: e3
Reason: r3
Verdict: yes
`},
		}},
	}

	result, err := scorer.ScoreBasedOnResponse(context.Background(), response, nil)
	require.NoError(t, err)
	require.Len(t, result.RubricScores, 3)
	assert.InDelta(t, 2.0/3.0, result.Score, 1e-9)
	ids := make([]string, 0, len(result.RubricScores))
	for _, s := range result.RubricScores {
		ids = append(ids, strings.TrimSpace(s.ID))
	}
	assert.Equal(t, []string{"1", "2", "3"}, ids)
}
