//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rouge

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// whitespaceTokenizer tokenizes text by splitting on whitespace without normalization.
type whitespaceTokenizer struct{}

// Tokenize splits text on whitespace without normalization.
func (whitespaceTokenizer) Tokenize(text string) []string {
	return strings.Fields(text)
}

// TestRougeCriterion_Match_DefaultMeasure verifies default measure selection and scoring values.
func TestRougeCriterion_Match_DefaultMeasure(t *testing.T) {
	c := &RougeCriterion{RougeType: "rouge1"}
	result, err := c.Match(context.Background(), "testing one two", "testing")
	require.NoError(t, err)
	assert.Equal(t, RougeMeasureF1, result.Measure)
	assert.InDelta(t, 1.0, result.Score.Precision, 1e-12)
	assert.InDelta(t, 1.0/3.0, result.Score.Recall, 1e-12)
	assert.InDelta(t, 0.5, result.Score.F1, 1e-12)
	assert.InDelta(t, 0.5, result.Value, 1e-12)
	assert.Contains(t, result.Reason(), "rouge1")
}

// TestRougeCriterion_Match_PrecisionMeasure verifies that precision can be selected as the scalar score.
func TestRougeCriterion_Match_PrecisionMeasure(t *testing.T) {
	c := &RougeCriterion{RougeType: "rouge1", Measure: RougeMeasurePrecision}
	result, err := c.Match(context.Background(), "testing one two", "testing")
	require.NoError(t, err)
	assert.Equal(t, RougeMeasurePrecision, result.Measure)
	assert.InDelta(t, 1.0, result.Value, 1e-12)
}

// TestRougeCriterion_Match_UnsupportedMeasureError verifies that unsupported measures return an error.
func TestRougeCriterion_Match_UnsupportedMeasureError(t *testing.T) {
	c := &RougeCriterion{RougeType: "rouge1", Measure: RougeMeasure("p")}
	_, err := c.Match(context.Background(), "testing one two", "testing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported rouge measure")
}

// TestRougeCriterion_Match_EmptyRougeTypeError verifies that an empty ROUGE type returns an error.
func TestRougeCriterion_Match_EmptyRougeTypeError(t *testing.T) {
	c := &RougeCriterion{}
	_, err := c.Match(context.Background(), "a", "b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rougeType")
}

// TestRougeCriterion_Match_WithTokenizer verifies that custom tokenizer overrides the built-in tokenizer.
func TestRougeCriterion_Match_WithTokenizer(t *testing.T) {
	defaultScores, err := (&RougeCriterion{RougeType: "rouge1"}).Match(context.Background(), "a-b", "a")
	require.NoError(t, err)
	assert.Greater(t, defaultScores.Value, 0.0)

	customScores, err := (&RougeCriterion{RougeType: "rouge1", Tokenizer: whitespaceTokenizer{}}).Match(
		context.Background(),
		"a-b",
		"a",
	)
	require.NoError(t, err)
	assert.InDelta(t, 0.0, customScores.Value, 1e-12)
}

// TestRougeCriterion_Match_PassedFlag verifies that pass/fail decisions respect the configured measure threshold.
func TestRougeCriterion_Match_PassedFlag(t *testing.T) {
	c := &RougeCriterion{
		RougeType:  "rouge1",
		Measure:    RougeMeasureF1,
		Threshold:  Score{F1: 0.6},
		UseStemmer: false,
	}
	result, err := c.Match(context.Background(), "testing one two", "testing")
	require.NoError(t, err)
	assert.InDelta(t, 0.5, result.Value, 1e-12)
	assert.False(t, result.Passed)
}

// TestRougeCriterion_Match_PassedFlagPrecision verifies that precision thresholds can be enforced.
func TestRougeCriterion_Match_PassedFlagPrecision(t *testing.T) {
	c := &RougeCriterion{
		RougeType: "rouge1",
		Measure:   RougeMeasurePrecision,
		Threshold: Score{Precision: 0.9},
	}
	result, err := c.Match(context.Background(), "testing one two", "testing")
	require.NoError(t, err)
	assert.InDelta(t, 1.0, result.Value, 1e-12)
	assert.True(t, result.Passed)
}

// TestRougeCriterion_Match_PassedFlag_AllThresholds verifies that all configured thresholds are enforced.
func TestRougeCriterion_Match_PassedFlag_AllThresholds(t *testing.T) {
	c := &RougeCriterion{
		RougeType: "rouge1",
		Measure:   RougeMeasurePrecision,
		Threshold: Score{Precision: 0.9, F1: 0.6},
	}
	result, err := c.Match(context.Background(), "testing one two", "testing")
	require.NoError(t, err)
	assert.InDelta(t, 1.0, result.Value, 1e-12)
	assert.False(t, result.Passed)
}

// TestRougeCriterion_Match_NilCriterionError verifies that a nil criterion returns an error.
func TestRougeCriterion_Match_NilCriterionError(t *testing.T) {
	var c *RougeCriterion
	_, err := c.Match(context.Background(), "a", "b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

// TestRougeCriterion_Match_Ignore verifies that Ignore short-circuits scoring with a perfect pass.
func TestRougeCriterion_Match_Ignore(t *testing.T) {
	c := &RougeCriterion{
		Ignore:    true,
		RougeType: "rouge1",
		Measure:   RougeMeasurePrecision,
	}
	result, err := c.Match(context.Background(), "testing one two", "testing")
	require.NoError(t, err)
	assert.Equal(t, RougeMeasureF1, result.Measure)
	assert.InDelta(t, 1.0, result.Value, 1e-12)
	assert.InDelta(t, 1.0, result.Score.Precision, 1e-12)
	assert.InDelta(t, 1.0, result.Score.Recall, 1e-12)
	assert.InDelta(t, 1.0, result.Score.F1, 1e-12)
	assert.True(t, result.Passed)
}

// TestRougeCriterion_Match_RecallMeasure verifies that recall can be selected as the scalar score.
func TestRougeCriterion_Match_RecallMeasure(t *testing.T) {
	c := &RougeCriterion{RougeType: "rouge1", Measure: RougeMeasureRecall}
	result, err := c.Match(context.Background(), "testing one two", "testing")
	require.NoError(t, err)
	assert.Equal(t, RougeMeasureRecall, result.Measure)
	assert.InDelta(t, 1.0/3.0, result.Value, 1e-12)
}

// TestRougeCriterion_Match_UseStemmer verifies that stemming can increase matches with the built-in tokenizer.
func TestRougeCriterion_Match_UseStemmer(t *testing.T) {
	noStem, err := (&RougeCriterion{RougeType: "rouge1"}).Match(context.Background(), "the friends", "friend")
	require.NoError(t, err)
	assert.InDelta(t, 0.0, noStem.Score.F1, 1e-12)

	withStem, err := (&RougeCriterion{RougeType: "rouge1", UseStemmer: true}).Match(
		context.Background(),
		"the friends",
		"friend",
	)
	require.NoError(t, err)
	assert.Greater(t, withStem.Score.F1, noStem.Score.F1)
}
