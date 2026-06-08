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

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestScoreBasedOnResponseUsesLogprobs(t *testing.T) {
	scorer := New()
	result, err := scorer.ScoreBasedOnResponse(context.Background(), &model.Response{
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
	}, nil)
	require.NoError(t, err)
	assert.InDelta(t, 0.75, result.Score, 1e-9)
	assert.Contains(t, result.Reason, "score_A")
}

func TestScoreBasedOnResponseRequiresLogprobs(t *testing.T) {
	scorer := New()
	_, err := scorer.ScoreBasedOnResponse(context.Background(), &model.Response{
		Choices: []model.Choice{{Message: model.NewAssistantMessage("Analysis.\n<score_A>A</score_A>\n<score_B>T</score_B>")}},
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logprobs are missing")
}

func TestScoreForTagFromLogprobsUsesTokenAfterTag(t *testing.T) {
	score, err := scoreForTagFromLogprobs([]model.TokenLogprob{
		{
			Token: "not-score",
			TopLogprobs: []model.TopLogprob{
				{Token: "A", Logprob: 0},
			},
		},
		{Token: " <score_A>"},
		{
			Token:   " K ",
			Logprob: math.Log(0.6),
			TopLogprobs: []model.TopLogprob{
				{Token: "J", Logprob: math.Log(0.4)},
				{Token: "K", Logprob: math.Log(0.6)},
			},
		},
	}, scoreATag, defaultGranularity)
	require.NoError(t, err)
	assert.InDelta(t, 0.4947, score, 1e-3)
}

func TestScoreForTagFromLogprobsUsesScoreInsideTagClosingToken(t *testing.T) {
	score, err := scoreForTagFromLogprobs([]model.TokenLogprob{
		{Token: "analysis\n<score_A"},
		{
			Token:   ">K",
			Logprob: math.Log(0.6),
			TopLogprobs: []model.TopLogprob{
				{Token: ">J", Logprob: math.Log(0.4)},
				{Token: ">K", Logprob: math.Log(0.6)},
			},
		},
	}, scoreATag, defaultGranularity)
	require.NoError(t, err)
	assert.InDelta(t, 0.4947, score, 1e-3)
}

func TestScoreForTagFromLogprobsRejectsInvalidLogprobs(t *testing.T) {
	_, err := scoreForTagFromLogprobs([]model.TokenLogprob{
		{Token: "<score_A>"},
		{
			Token:   "A",
			Logprob: math.Inf(-1),
			TopLogprobs: []model.TopLogprob{
				{Token: "A", Logprob: math.Inf(-1)},
			},
		},
	}, scoreATag, defaultGranularity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "negative infinity")
}

func TestScoreTokenRejectsWrappedToken(t *testing.T) {
	_, ok := scoreTokenIndex(`"A"`, defaultGranularity)
	assert.False(t, ok)
	_, ok = scoreTokenIndex("A,", defaultGranularity)
	assert.False(t, ok)
}
