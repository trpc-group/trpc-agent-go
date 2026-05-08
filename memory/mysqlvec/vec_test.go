//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysqlvec

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSerializeVector(t *testing.T) {
	embedding := []float64{1.0, 2.0, 3.0}
	blob := serializeVector(embedding)

	assert.Equal(t, 12, len(blob)) // 3 * 4 bytes

	// Round-trip.
	result, err := deserializeVector(blob)
	require.NoError(t, err)
	require.Len(t, result, 3)
	assert.InDelta(t, 1.0, result[0], 1e-6)
	assert.InDelta(t, 2.0, result[1], 1e-6)
	assert.InDelta(t, 3.0, result[2], 1e-6)
}

func TestSerializeVector_Empty(t *testing.T) {
	blob := serializeVector(nil)
	assert.Empty(t, blob)

	result, err := deserializeVector(blob)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestDeserializeVector_InvalidLength(t *testing.T) {
	_, err := deserializeVector([]byte{1, 2, 3}) // Not multiple of 4
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid vector blob length")
}

func TestVectorToString(t *testing.T) {
	embedding := []float64{1.0, 2.5, 3.14}
	s := vectorToString(embedding)

	assert.Contains(t, s, "[")
	assert.Contains(t, s, "]")
	assert.Contains(t, s, "1")
	assert.Contains(t, s, "2.5")
}

func TestVectorToString_Empty(t *testing.T) {
	s := vectorToString(nil)
	assert.Equal(t, "[]", s)
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a, b     []float64
		expected float64
	}{
		{
			name:     "identical vectors",
			a:        []float64{1, 0, 0},
			b:        []float64{1, 0, 0},
			expected: 1.0,
		},
		{
			name:     "orthogonal vectors",
			a:        []float64{1, 0, 0},
			b:        []float64{0, 1, 0},
			expected: 0.0,
		},
		{
			name:     "opposite vectors",
			a:        []float64{1, 0, 0},
			b:        []float64{-1, 0, 0},
			expected: -1.0,
		},
		{
			name:     "same direction different magnitude",
			a:        []float64{1, 2, 3},
			b:        []float64{2, 4, 6},
			expected: 1.0,
		},
		{
			name:     "empty vectors",
			a:        []float64{},
			b:        []float64{},
			expected: 0,
		},
		{
			name:     "zero vector",
			a:        []float64{0, 0, 0},
			b:        []float64{1, 2, 3},
			expected: 0,
		},
		{
			name:     "different lengths",
			a:        []float64{1, 2},
			b:        []float64{1, 2, 3},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cosineSimilarity(tt.a, tt.b)
			assert.InDelta(t, tt.expected, result, 1e-9)
		})
	}
}

func TestCosineSimilarity_45Degrees(t *testing.T) {
	a := []float64{1, 0}
	b := []float64{1, 1}
	result := cosineSimilarity(a, b)
	expected := 1.0 / math.Sqrt(2.0) // cos(45°) ≈ 0.7071
	assert.InDelta(t, expected, result, 1e-9)
}

func TestSerializeDeserialize_Precision(t *testing.T) {
	// Test that float64 -> float32 -> float64 round-trip preserves reasonable precision.
	embedding := []float64{0.123456789, -0.987654321, 0.0, 1.0, -1.0}
	blob := serializeVector(embedding)
	result, err := deserializeVector(blob)
	require.NoError(t, err)
	require.Len(t, result, len(embedding))

	for i := range embedding {
		// float32 has ~7 decimal digits of precision.
		assert.InDelta(t, embedding[i], result[i], 1e-6)
	}
}
