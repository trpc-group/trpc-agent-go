//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evaluation

import (
	"math"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
)

func chooseBig(n, k int64) *big.Int {
	if k < 0 || k > n {
		return big.NewInt(0)
	}
	if k == 0 || k == n {
		return big.NewInt(1)
	}
	// Symmetry: C(n,k) == C(n,n-k)
	if k > n-k {
		k = n - k
	}
	res := big.NewInt(1)
	for i := int64(1); i <= k; i++ {
		res.Mul(res, big.NewInt(n-k+i))
		res.Div(res, big.NewInt(i))
	}
	return res
}

func expectedPassAtK(n, c, k int) float64 {
	// pass@k = 1 - C(n-c,k) / C(n,k)
	num := new(big.Rat).SetInt(chooseBig(int64(n-c), int64(k)))
	den := new(big.Rat).SetInt(chooseBig(int64(n), int64(k)))
	if den.Sign() == 0 {
		return 0
	}
	ratio := new(big.Rat).Quo(num, den)
	one := big.NewRat(1, 1)
	exp := new(big.Rat).Sub(one, ratio)
	f, _ := exp.Float64()
	return f
}

func TestPassAtK_ValidatesArgs(t *testing.T) {
	_, err := PassAtK(-1, 0, 1)
	require.Error(t, err)
	_, err = PassAtK(1, 0, 0)
	require.Error(t, err)
	_, err = PassAtK(1, -1, 1)
	require.Error(t, err)
	_, err = PassAtK(1, 2, 1)
	require.Error(t, err)
	_, err = PassAtK(1, 0, 2)
	require.Error(t, err)
}

func TestPassAtK_EdgeCases(t *testing.T) {
	got, err := PassAtK(10, 0, 1)
	require.NoError(t, err)
	require.Equal(t, 0.0, got)

	got, err = PassAtK(10, 10, 1)
	require.NoError(t, err)
	require.Equal(t, 1.0, got)

	// n-c < k => guaranteed at least one success.
	got, err = PassAtK(5, 4, 2) // failures=1, pick 2 => impossible
	require.NoError(t, err)
	require.Equal(t, 1.0, got)
}

func TestPassAtK_KnownSmallValues(t *testing.T) {
	cases := []struct {
		n, c, k int
	}{
		{n: 5, c: 1, k: 1},
		{n: 5, c: 1, k: 2},
		{n: 10, c: 3, k: 1},
		{n: 10, c: 3, k: 5},
		{n: 20, c: 7, k: 3},
	}
	for _, tc := range cases {
		got, err := PassAtK(tc.n, tc.c, tc.k)
		require.NoError(t, err)
		want := expectedPassAtK(tc.n, tc.c, tc.k)
		require.InEpsilon(t, want, got, 1e-12)
	}
}

func TestPassHatK_ValidatesArgs(t *testing.T) {
	_, err := PassHatK(0, 0, 1)
	require.Error(t, err)
	_, err = PassHatK(1, 0, 0)
	require.Error(t, err)
	_, err = PassHatK(1, -1, 1)
	require.Error(t, err)
	_, err = PassHatK(1, 2, 1)
	require.Error(t, err)
}

func TestPassHatK_EdgeCasesAndKnownValue(t *testing.T) {
	got, err := PassHatK(10, 0, 3)
	require.NoError(t, err)
	require.Equal(t, 0.0, got)

	got, err = PassHatK(10, 10, 3)
	require.NoError(t, err)
	require.Equal(t, 1.0, got)

	// p = 5/10 = 0.5 => p^2 = 0.25
	got, err = PassHatK(10, 5, 2)
	require.NoError(t, err)
	require.InDelta(t, 0.25, got, 1e-12)
}

func TestParsePassNC_NilAndMissingFields(t *testing.T) {
	_, _, err := ParsePassNC(nil)
	require.Error(t, err)

	_, _, err = ParsePassNC(&EvaluationResult{})
	require.Error(t, err)

	_, _, err = ParsePassNC(&EvaluationResult{EvalResult: &evalresult.EvalSetResult{}})
	require.Error(t, err)

	_, _, err = ParsePassNC(&EvaluationResult{EvalResult: &evalresult.EvalSetResult{
		Summary: &evalresult.EvalSetResultSummary{},
	}})
	require.Error(t, err)
}

func TestParsePassNC_OK(t *testing.T) {
	res := &EvaluationResult{
		EvalResult: &evalresult.EvalSetResult{
			Summary: &evalresult.EvalSetResultSummary{
				NumRuns: 4,
				RunStatusCounts: &evalresult.EvalStatusCounts{
					Passed: 2,
					Failed: 2,
				},
			},
		},
	}
	n, c, err := ParsePassNC(res)
	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.Equal(t, 2, c)
}

func TestPassAtK_IsProbability(t *testing.T) {
	got, err := PassAtK(100, 1, 1)
	require.NoError(t, err)
	require.GreaterOrEqual(t, got, 0.0)
	require.LessOrEqual(t, got, 1.0)
	require.False(t, math.IsNaN(got))
}
