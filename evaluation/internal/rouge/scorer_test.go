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
	"errors"
	"os"
	"path/filepath"
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

// TestCompute_InvalidRougeType verifies that invalid ROUGE type names return an error.
func TestCompute_InvalidRougeType(t *testing.T) {
	for _, rougeType := range []string{"rouge", "rougen", "rouge0", "rouge-1"} {
		_, err := Compute(context.Background(), "a", "b", WithRougeTypes(rougeType))
		require.Error(t, err)
	}
}

// TestCompute_NilContext verifies that nil contexts return an error.
func TestCompute_NilContext(t *testing.T) {
	_, err := Compute(nil, "a", "b", WithRougeTypes("rouge1"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context is nil")
}

// TestCompute_ContextCanceled verifies that canceled contexts return the context error.
func TestCompute_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Compute(ctx, "a", "b", WithRougeTypes("rouge1"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}

// TestCompute_RougeN_MultiDigit verifies that multi-digit ROUGE-N values are accepted.
func TestCompute_RougeN_MultiDigit(t *testing.T) {
	result, err := Compute(
		context.Background(),
		"a b c d e f g h i j",
		"a b c d e f g h i j",
		WithRougeTypes("rouge10"),
	)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, result["rouge10"].Precision, 1e-12)
	assert.InDelta(t, 1.0, result["rouge10"].Recall, 1e-12)
	assert.InDelta(t, 1.0, result["rouge10"].FMeasure, 1e-12)
}

// TestCompute_EmptyRougeTypes verifies that empty rougeTypes returns an empty result without error.
func TestCompute_EmptyRougeTypes(t *testing.T) {
	result, err := Compute(context.Background(), "a", "b")
	require.NoError(t, err)
	assert.Empty(t, result)
}

// TestCompute_WithTokenizer verifies that a custom tokenizer overrides the built-in tokenizer.
func TestCompute_WithTokenizer(t *testing.T) {
	defaultScores, err := Compute(context.Background(), "a-b", "a", WithRougeTypes("rouge1"))
	require.NoError(t, err)
	assert.Greater(t, defaultScores["rouge1"].FMeasure, 0.0)

	customScores, err := Compute(
		context.Background(),
		"a-b",
		"a",
		WithRougeTypes("rouge1"),
		WithTokenizer(whitespaceTokenizer{}),
	)
	require.NoError(t, err)
	assert.InDelta(t, 0.0, customScores["rouge1"].FMeasure, 1e-12)
}

// TestCompute_Rouge1 verifies that rouge1 scoring matches expected precision, recall, and F-measure.
func TestCompute_Rouge1(t *testing.T) {
	result, err := Compute(context.Background(), "testing one two", "testing", WithRougeTypes("rouge1"))
	require.NoError(t, err)

	assert.InDelta(t, 1.0, result["rouge1"].Precision, 1e-12)
	assert.InDelta(t, 1.0/3.0, result["rouge1"].Recall, 1e-12)
	assert.InDelta(t, 0.5, result["rouge1"].FMeasure, 1e-12)
}

// TestCompute_Rouge2 verifies that rouge2 scoring matches expected precision, recall, and F-measure.
func TestCompute_Rouge2(t *testing.T) {
	result, err := Compute(context.Background(), "testing one two", "testing one", WithRougeTypes("rouge2"))
	require.NoError(t, err)

	assert.InDelta(t, 1.0, result["rouge2"].Precision, 1e-12)
	assert.InDelta(t, 0.5, result["rouge2"].Recall, 1e-12)
	assert.InDelta(t, 2.0/3.0, result["rouge2"].FMeasure, 1e-12)
}

// TestCompute_RougeL_NonConsecutive verifies that rougeL uses LCS and supports non-consecutive matches.
func TestCompute_RougeL_NonConsecutive(t *testing.T) {
	result, err := Compute(context.Background(), "testing one two", "testing two", WithRougeTypes("rougeL"))
	require.NoError(t, err)

	assert.InDelta(t, 1.0, result["rougeL"].Precision, 1e-12)
	assert.InDelta(t, 2.0/3.0, result["rougeL"].Recall, 1e-12)
	assert.InDelta(t, 4.0/5.0, result["rougeL"].FMeasure, 1e-12)
}

// TestComputeMulti_RougeAll verifies multi-reference scoring selects the max F-measure per type.
func TestComputeMulti_RougeAll(t *testing.T) {
	result, err := computeMulti(
		context.Background(),
		[]string{"first text", "first something"},
		"text first",
		WithRougeTypes("rouge1", "rouge2", "rougeL"),
	)
	require.NoError(t, err)

	assert.InDelta(t, 1.0, result["rouge1"].FMeasure, 1e-12)
	assert.InDelta(t, 0.0, result["rouge2"].FMeasure, 1e-12)
	assert.InDelta(t, 0.5, result["rougeL"].FMeasure, 1e-12)
}

// TestComputeMulti_EmptyTargets verifies that computeMulti rejects empty targets.
func TestComputeMulti_EmptyTargets(t *testing.T) {
	_, err := computeMulti(context.Background(), nil, "prediction", WithRougeTypes("rouge1"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "targets are empty")
}

// TestCompute_RougeLsum verifies rougeLsum scoring on newline-separated summaries and edge cases.
func TestCompute_RougeLsum(t *testing.T) {
	result, err := Compute(
		context.Background(),
		"w1 w2 w3 w4 w5",
		"w1 w2 w6 w7 w8\nw1 w3 w8 w9 w5",
		WithRougeTypes("rougeLsum"),
	)
	require.NoError(t, err)
	assert.InDelta(t, 0.8, result["rougeLsum"].Recall, 1e-12)
	assert.InDelta(t, 0.4, result["rougeLsum"].Precision, 1e-12)
	assert.InDelta(t, 0.5333, result["rougeLsum"].FMeasure, 1e-4)

	result, err = Compute(context.Background(), "w1 w2 w3 w4 w5", "", WithRougeTypes("rougeLsum"))
	require.NoError(t, err)
	assert.InDelta(t, 0.0, result["rougeLsum"].FMeasure, 1e-12)
	assert.InDelta(t, 0.0, result["rougeLsum"].Recall, 1e-12)
	assert.InDelta(t, 0.0, result["rougeLsum"].Precision, 1e-12)

	result, err = Compute(context.Background(), "", "w1", WithRougeTypes("rougeLsum"))
	require.NoError(t, err)
	assert.InDelta(t, 0.0, result["rougeLsum"].FMeasure, 1e-12)
	assert.InDelta(t, 0.0, result["rougeLsum"].Recall, 1e-12)
	assert.InDelta(t, 0.0, result["rougeLsum"].Precision, 1e-12)

	result, err = Compute(context.Background(), "w1 w2 w3 w4 w5", "/", WithRougeTypes("rougeLsum"))
	require.NoError(t, err)
	assert.InDelta(t, 0.0, result["rougeLsum"].FMeasure, 1e-12)
	assert.InDelta(t, 0.0, result["rougeLsum"].Recall, 1e-12)
	assert.InDelta(t, 0.0, result["rougeLsum"].Precision, 1e-12)
}

// TestScorer_RougeLsumSentenceSplitting verifies sentence splitting options for rougeLsum.
func TestScorer_RougeLsumSentenceSplitting(t *testing.T) {
	target := "First sentence.\nSecond Sentence."
	prediction := "Second sentence.\nFirst Sentence."

	result, err := Compute(context.Background(), target, prediction, WithRougeTypes("rougeLsum"), WithStemmer(true))
	require.NoError(t, err)
	assert.InDelta(t, 1.0, result["rougeLsum"].FMeasure, 1e-12)

	target = strings.ReplaceAll(target, "\n", " ")
	prediction = strings.ReplaceAll(prediction, "\n", " ")
	result, err = Compute(
		context.Background(),
		target,
		prediction,
		WithRougeTypes("rougeLsum"),
		WithStemmer(true),
		WithSplitSummaries(false),
	)
	require.NoError(t, err)
	assert.InDelta(t, 0.50, result["rougeLsum"].FMeasure, 1e-12)

	result, err = Compute(
		context.Background(),
		target,
		prediction,
		WithRougeTypes("rougeLsum"),
		WithStemmer(true),
		WithSplitSummaries(true),
	)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, result["rougeLsum"].FMeasure, 1e-12)
}

// TestScorer_Rouge155Compatibility verifies scores against rouge-1.5.5 reference testdata.
func TestScorer_Rouge155Compatibility(t *testing.T) {
	targetsText := readTestdataFile(t, filepath.Join("testdata", "target.txt"))
	predText := readTestdataFile(t, filepath.Join("testdata", "prediction.txt"))

	targets := strings.Split(strings.TrimRight(targetsText, "\n"), "\n")
	predictions := strings.Split(strings.TrimRight(predText, "\n"), "\n")
	require.Len(t, targets, 2)
	require.Len(t, predictions, 2)

	res, err := Compute(context.Background(), targets[0], predictions[0], WithRougeTypes("rouge1", "rouge2", "rougeL"))
	require.NoError(t, err)
	assert.InDelta(t, 0.40741, res["rouge1"].Recall, 1e-5)
	assert.InDelta(t, 0.68750, res["rouge1"].Precision, 1e-5)
	assert.InDelta(t, 0.51163, res["rouge1"].FMeasure, 1e-5)
	assert.InDelta(t, 0.30769, res["rouge2"].Recall, 1e-5)
	assert.InDelta(t, 0.53333, res["rouge2"].Precision, 1e-5)
	assert.InDelta(t, 0.39024, res["rouge2"].FMeasure, 1e-5)
	assert.InDelta(t, 0.40741, res["rougeL"].Recall, 1e-5)
	assert.InDelta(t, 0.68750, res["rougeL"].Precision, 1e-5)
	assert.InDelta(t, 0.51163, res["rougeL"].FMeasure, 1e-5)

	res, err = Compute(context.Background(), targets[1], predictions[1], WithRougeTypes("rouge1", "rouge2", "rougeL"))
	require.NoError(t, err)
	assert.InDelta(t, 0.40476, res["rouge1"].Recall, 1e-5)
	assert.InDelta(t, 0.65385, res["rouge1"].Precision, 1e-5)
	assert.InDelta(t, 0.50000, res["rouge1"].FMeasure, 1e-5)
	assert.InDelta(t, 0.29268, res["rouge2"].Recall, 1e-5)
	assert.InDelta(t, 0.48000, res["rouge2"].Precision, 1e-5)
	assert.InDelta(t, 0.36364, res["rouge2"].FMeasure, 1e-5)
	assert.InDelta(t, 0.40476, res["rougeL"].Recall, 1e-5)
	assert.InDelta(t, 0.65385, res["rougeL"].Precision, 1e-5)
	assert.InDelta(t, 0.50000, res["rougeL"].FMeasure, 1e-5)

	res, err = Compute(context.Background(), targets[0], predictions[0], WithRougeTypes("rouge1", "rouge2"), WithStemmer(true))
	require.NoError(t, err)
	assert.InDelta(t, 0.40741, res["rouge1"].Recall, 1e-5)
	assert.InDelta(t, 0.68750, res["rouge1"].Precision, 1e-5)
	assert.InDelta(t, 0.51163, res["rouge1"].FMeasure, 1e-5)
	assert.InDelta(t, 0.30769, res["rouge2"].Recall, 1e-5)
	assert.InDelta(t, 0.53333, res["rouge2"].Precision, 1e-5)
	assert.InDelta(t, 0.39024, res["rouge2"].FMeasure, 1e-5)

	res, err = Compute(context.Background(), targets[1], predictions[1], WithRougeTypes("rouge1", "rouge2"), WithStemmer(true))
	require.NoError(t, err)
	assert.InDelta(t, 0.42857, res["rouge1"].Recall, 1e-5)
	assert.InDelta(t, 0.69231, res["rouge1"].Precision, 1e-5)
	assert.InDelta(t, 0.52941, res["rouge1"].FMeasure, 1e-5)
	assert.InDelta(t, 0.29268, res["rouge2"].Recall, 1e-5)
	assert.InDelta(t, 0.48000, res["rouge2"].Precision, 1e-5)
	assert.InDelta(t, 0.36364, res["rouge2"].FMeasure, 1e-5)
}

// TestScorer_RougeLsumAgainstRouge155WithStemming verifies rougeLsum scoring against rouge-1.5.5 testdata with stemming.
func TestScorer_RougeLsumAgainstRouge155WithStemming(t *testing.T) {
	target := readTestdataFile(t, filepath.Join("testdata", "rouge155", "target_multi.0.txt"))
	prediction := readTestdataFile(t, filepath.Join("testdata", "rouge155", "prediction_multi.0.txt"))

	res, err := Compute(context.Background(), target, prediction, WithRougeTypes("rougeLsum"), WithStemmer(true))
	require.NoError(t, err)

	assert.InDelta(t, 0.36538, res["rougeLsum"].Recall, 1e-5)
	assert.InDelta(t, 0.66667, res["rougeLsum"].Precision, 1e-5)
	assert.InDelta(t, 0.47205, res["rougeLsum"].FMeasure, 1e-5)
}

// readTestdataFile reads a file from disk and returns its contents as a string.
func readTestdataFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
