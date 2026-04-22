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

	output, err := StructuredOutput(ResponseScorerRubricScoresName)
	require.NoError(t, err)
	require.NotNil(t, output)
	require.NotNil(t, output.JSONSchema)
	assert.Equal(t, "rubric_scores_result", output.JSONSchema.Name)
}

func TestResolveResponseScorerRejectsUnknownName(t *testing.T) {
	_, err := ResolveResponseScorer("missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported response scorer "missing"`)

	_, err = StructuredOutput("missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported response scorer "missing"`)
}
