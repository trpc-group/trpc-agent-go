//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package singlescore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestScoreBasedOnResponseParsesStructuredJSON(t *testing.T) {
	scorer := New()

	result, err := scorer.ScoreBasedOnResponse(context.Background(), makeResponse("```json\n{\"score\":0.75,\"reason\":\"Looks good.\"}\n```"), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0.75, result.Score)
	assert.Equal(t, "Looks good.", result.Reason)
}

func TestScoreBasedOnResponseRejectsOutOfRangeScore(t *testing.T) {
	scorer := New()
	_, err := scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"score":1.2,"reason":"too high"}`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "score must be between 0 and 1")
}

func TestScoreBasedOnResponseRejectsMissingRequiredFields(t *testing.T) {
	scorer := New()
	_, err := scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"reason":"missing score"}`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "score is required")
	_, err = scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"score":0.2}`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reason is required")
}

func TestScoreBasedOnResponseRejectsInvalidJSON(t *testing.T) {
	scorer := New()

	_, err := scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`not-json`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal response json")
}

func makeResponse(content string) *model.Response {
	return &model.Response{
		Choices: []model.Choice{{Message: model.Message{Content: content}}},
	}
}
