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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/score"
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
	require.NotNil(t, result.Value)
	assert.Equal(t, score.KindNumeric, result.Value.Kind)
	require.NotNil(t, result.Value.Numeric)
	assert.InDelta(t, result.Score, *result.Value.Numeric, 1e-9)
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

func TestScoreFromLogprobsReportsTagSpecificErrors(t *testing.T) {
	_, _, err := scoreFromLogprobs(&model.Response{
		Choices: []model.Choice{
			{
				Logprobs: &model.Logprobs{
					Content: []model.TokenLogprob{{Token: "<score_B>"}, {Token: "A", Logprob: 0}},
				},
			},
		},
	}, defaultGranularity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "score_A")

	_, _, err = scoreFromLogprobs(&model.Response{
		Choices: []model.Choice{
			{
				Logprobs: &model.Logprobs{
					Content: []model.TokenLogprob{{Token: "<score_A>"}, {Token: "A", Logprob: 0}},
				},
			},
		},
	}, defaultGranularity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "score_B")
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

func TestScoreForTagFromLogprobsRejectsInvalidPrefixedScoreToken(t *testing.T) {
	_, err := scoreForTagFromLogprobs([]model.TokenLogprob{
		{Token: "analysis\n<score_A"},
		{
			Token:       ">not-score",
			TopLogprobs: []model.TopLogprob{{Token: ">also-not-score", Logprob: 0}},
		},
	}, scoreATag, defaultGranularity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "score token logprobs")
}

func TestScoreForTagFromLogprobsRejectsMissingData(t *testing.T) {
	_, err := scoreForTagFromLogprobs(nil, scoreATag, defaultGranularity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
	_, err = scoreForTagFromLogprobs([]model.TokenLogprob{{Token: "analysis"}}, scoreATag, defaultGranularity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "score tag is missing")
	_, err = scoreForTagFromLogprobs([]model.TokenLogprob{{Token: scoreATag}}, scoreATag, defaultGranularity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "score token is missing")
	_, err = scoreForTagFromLogprobs([]model.TokenLogprob{
		{Token: scoreATag},
		{Token: "not-score"},
	}, scoreATag, defaultGranularity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "score token logprobs")
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

	_, err = scoreForTagFromLogprobs([]model.TokenLogprob{
		{Token: "<score_A>"},
		{
			Token:   "A",
			Logprob: math.NaN(),
		},
	}, scoreATag, defaultGranularity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NaN")
}

func TestScoreTokenRejectsWrappedToken(t *testing.T) {
	_, ok := scoreTokenIndex(`"A"`, defaultGranularity)
	assert.False(t, ok)
	_, ok = scoreTokenIndex("A,", defaultGranularity)
	assert.False(t, ok)
	_, ok = scoreTokenIndexAfterPrefix("B", "A", defaultGranularity)
	assert.False(t, ok)
	assert.Nil(t, scoreTokenDistributionFromTokenPrefix(model.TokenLogprob{Token: "A"}, 2, defaultGranularity))
	_, ok = scoreForIndex(-1, defaultGranularity)
	assert.False(t, ok)
	_, ok = scoreForIndex(0, 1)
	assert.False(t, ok)
	_, ok = scoreTokenIndex("U", defaultGranularity)
	assert.False(t, ok)
	dist := scoreTokenDistributionFromToken(model.TokenLogprob{
		Token: "not-score",
		TopLogprobs: []model.TopLogprob{
			{Token: "also-not-score", Logprob: 0},
			{Token: "T", Logprob: 0},
		},
	}, defaultGranularity)
	require.NotNil(t, dist)
	assert.Contains(t, dist.logprobs, defaultGranularity-1)
	assert.Equal(t, 0.0, pairwisePreferenceScore(0, 2))
	assert.Equal(t, 1.0, pairwisePreferenceScore(2, 0))
}
