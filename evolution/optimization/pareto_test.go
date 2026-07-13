//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimization

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScoreMatrixParetoSelectionUsesInstanceCoverage(t *testing.T) {
	cases := []Case{{ID: "case-1"}, {ID: "case-2"}, {ID: "case-3"}}
	matrix := newScoreMatrix(cases)
	addScores(t, matrix, cases, "candidate-a", []float64{1, 1, 0.2})
	addScores(t, matrix, cases, "candidate-b", []float64{0.2, 0.2, 1})
	addScores(t, matrix, cases, "candidate-c", []float64{1, 1, 0})

	fronts := matrix.removeCoverageDominated(matrix.instanceFronts())
	assert.Equal(t, map[string]bool{"candidate-a": true}, fronts["case-1"])
	assert.Equal(t, map[string]bool{"candidate-a": true}, fronts["case-2"])
	assert.Equal(t, map[string]bool{"candidate-b": true}, fronts["case-3"])

	rng := rand.New(rand.NewSource(42))
	counts := map[string]int{}
	for i := 0; i < 600; i++ {
		selected, err := matrix.selectParent(rng)
		require.NoError(t, err)
		counts[selected]++
	}
	assert.Greater(t, counts["candidate-a"], counts["candidate-b"])
	assert.Zero(t, counts["candidate-c"])
}

func TestScoreMatrixParetoSelectionIsDeterministic(t *testing.T) {
	cases := []Case{{ID: "case-1"}, {ID: "case-2"}}
	matrix := newScoreMatrix(cases)
	addScores(t, matrix, cases, "candidate-a", []float64{1, 0})
	addScores(t, matrix, cases, "candidate-b", []float64{0, 1})

	left := rand.New(rand.NewSource(7))
	right := rand.New(rand.NewSource(7))
	for i := 0; i < 50; i++ {
		leftID, err := matrix.selectParent(left)
		require.NoError(t, err)
		rightID, err := matrix.selectParent(right)
		require.NoError(t, err)
		assert.Equal(t, leftID, rightID)
	}
}

func addScores(
	t *testing.T,
	matrix *scoreMatrix,
	cases []Case,
	candidateID string,
	scores []float64,
) {
	t.Helper()
	evaluations := make([]Evaluation, len(cases))
	for index, item := range cases {
		evaluations[index] = Evaluation{CaseID: item.ID, Score: scores[index]}
	}
	batch, err := newEvaluationBatch(cases, evaluations)
	require.NoError(t, err)
	require.NoError(t, matrix.add(candidateID, batch))
}
