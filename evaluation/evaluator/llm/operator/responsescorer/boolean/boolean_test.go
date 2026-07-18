//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package boolean

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/score"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestScoreBasedOnResponseParsesBooleanResult(t *testing.T) {
	scorer := New()
	result, err := scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"passed":true,"reason":"ok"}`), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1.0, result.Score)
	assert.Equal(t, "ok", result.Reason)
	require.NotNil(t, result.Value)
	assert.Equal(t, score.KindBoolean, result.Value.Kind)
	require.NotNil(t, result.Value.Boolean)
	assert.True(t, *result.Value.Boolean)
	result, err = scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"passed":false,"reason":"bad"}`), nil)
	require.NoError(t, err)
	assert.Equal(t, 0.0, result.Score)
	require.NotNil(t, result.Value)
	require.NotNil(t, result.Value.Boolean)
	assert.False(t, *result.Value.Boolean)
}

func TestScoreBasedOnResponseRejectsMissingBooleanFields(t *testing.T) {
	scorer := New()
	_, err := scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"reason":"missing passed"}`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "passed is required")
	_, err = scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"passed":true}`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reason is required")
}

func makeResponse(content string) *model.Response {
	return &model.Response{
		Choices: []model.Choice{{Message: model.Message{Content: content}}},
	}
}
