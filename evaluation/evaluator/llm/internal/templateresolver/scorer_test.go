//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package templateresolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveResponseScorer(t *testing.T) {
	scorer, err := ResolveResponseScorer(ResponseScorerSingleScoreName)
	require.NoError(t, err)
	assert.NotNil(t, scorer)

	scorer, err = ResolveResponseScorer(ResponseScorerRubricScoresName)
	require.NoError(t, err)
	assert.NotNil(t, scorer)

	output, err := StructuredOutput(ResponseScorerSingleScoreName)
	require.NoError(t, err)
	require.NotNil(t, output)
	require.NotNil(t, output.JSONSchema)
	assert.Equal(t, "single_score_result", output.JSONSchema.Name)
	assert.Equal(t, "A score and a concise reason for the evaluation result.", output.JSONSchema.Description)

	output, err = StructuredOutput(ResponseScorerRubricScoresName)
	require.NoError(t, err)
	require.NotNil(t, output)
	require.NotNil(t, output.JSONSchema)
	assert.Equal(t, "rubric_scores_result", output.JSONSchema.Name)
	assert.Equal(t, "Per-rubric scores and concise reasons for the evaluation result.", output.JSONSchema.Description)
}

func TestResolveResponseScorerRejectsUnknownName(t *testing.T) {
	_, err := ResolveResponseScorer("missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported response scorer "missing"`)

	_, err = StructuredOutput("missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported response scorer "missing"`)
}
