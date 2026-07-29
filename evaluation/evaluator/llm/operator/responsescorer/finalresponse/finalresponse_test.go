//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package finalresponse

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scorepkg "trpc.group/trpc-go/trpc-agent-go/evaluation/score"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestScoreBasedOnResponse(t *testing.T) {
	scorer := New()
	ctx := context.Background()

	score, err := scorer.ScoreBasedOnResponse(ctx, makeResponse(`reasoning: ok
is_the_agent_response_valid: VALID`), nil)
	require.NoError(t, err)
	assert.Equal(t, 1.0, score.Score)
	require.NotNil(t, score.Value)
	assert.Equal(t, scorepkg.KindNumeric, score.Value.Kind)
	require.NotNil(t, score.Value.Numeric)
	assert.Equal(t, 1.0, *score.Value.Numeric)

	score, err = scorer.ScoreBasedOnResponse(ctx, makeResponse(`reasoning: bad
is_the_agent_response_valid: invalid`), nil)
	require.NoError(t, err)
	assert.Equal(t, 0.0, score.Score)
	require.NotNil(t, score.Value)
	assert.Equal(t, scorepkg.KindNumeric, score.Value.Kind)
	require.NotNil(t, score.Value.Numeric)
	assert.Equal(t, 0.0, *score.Value.Numeric)

	_, err = scorer.ScoreBasedOnResponse(ctx, &model.Response{}, nil)
	require.Error(t, err)

	_, err = scorer.ScoreBasedOnResponse(ctx, makeResponse(""), nil)
	require.Error(t, err)
}

func TestExtractReasoningAndLabelText(t *testing.T) {
	content := `
reasoning: something
is_the_agent_response_valid: invalid
`
	reason, label, err := extractReasoningAndLabel(content)
	require.NoError(t, err)
	assert.Equal(t, "something", reason)
	assert.Equal(t, "invalid", label)
}

func makeResponse(content string) *model.Response {
	return &model.Response{
		Choices: []model.Choice{
			{Message: model.Message{Content: content}},
		},
	}
}
