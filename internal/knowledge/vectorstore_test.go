//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package knowledge

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeScore(t *testing.T) {
	tests := []struct {
		name       string
		score      float64
		metricType MetricType
		want       float64
	}{
		// L2 distance tests
		{
			name:       "L2 zero distance (identical vectors)",
			score:      0,
			metricType: MetricTypeL2,
			want:       1.0,
		},
		{
			name:       "L2 distance 1",
			score:      1,
			metricType: MetricTypeL2,
			want:       0.5,
		},
		{
			name:       "L2 large distance",
			score:      99,
			metricType: MetricTypeL2,
			want:       0.01,
		},
		{
			name:       "L2 negative distance (clamped to 0)",
			score:      -1,
			metricType: MetricTypeL2,
			want:       1.0,
		},

		// IP (Inner Product) tests
		{
			name:       "IP perfect match",
			score:      1,
			metricType: MetricTypeIP,
			want:       1.0,
		},
		{
			name:       "IP orthogonal",
			score:      0,
			metricType: MetricTypeIP,
			want:       0.5,
		},
		{
			name:       "IP opposite",
			score:      -1,
			metricType: MetricTypeIP,
			want:       0.0,
		},
		{
			name:       "IP clamped above 1",
			score:      1.5,
			metricType: MetricTypeIP,
			want:       1.0,
		},
		{
			name:       "IP clamped below -1",
			score:      -1.5,
			metricType: MetricTypeIP,
			want:       0.0,
		},

		// COSINE tests
		{
			name:       "COSINE perfect match",
			score:      1,
			metricType: MetricTypeCosine,
			want:       1.0,
		},
		{
			name:       "COSINE orthogonal",
			score:      0,
			metricType: MetricTypeCosine,
			want:       0.5,
		},
		{
			name:       "COSINE opposite",
			score:      -1,
			metricType: MetricTypeCosine,
			want:       0.0,
		},
		{
			name:       "COSINE partial similarity",
			score:      0.5,
			metricType: MetricTypeCosine,
			want:       0.75,
		},

		// BM25 tests
		{
			name:       "BM25 zero score",
			score:      0,
			metricType: MetricTypeBM25,
			want:       0.5,
		},
		{
			name:       "BM25 high score",
			score:      5,
			metricType: MetricTypeBM25,
			want:       1.0 / (1.0 + math.Exp(-5)),
		},
		{
			name:       "BM25 negative score (clamped to 0)",
			score:      -1,
			metricType: MetricTypeBM25,
			want:       0.5,
		},

		// Unknown metric type
		{
			name:       "unknown metric type returns original score",
			score:      0.75,
			metricType: "UNKNOWN",
			want:       0.75,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeScore(tt.score, tt.metricType)
			assert.InDelta(t, tt.want, got, 1e-9)
		})
	}
}

func TestNormalizeScores(t *testing.T) {
	tests := []struct {
		name       string
		scores     []float64
		metricType MetricType
		want       []float64
	}{
		{
			name:       "L2 multiple scores",
			scores:     []float64{0, 1, 4},
			metricType: MetricTypeL2,
			want:       []float64{1.0, 0.5, 0.2},
		},
		{
			name:       "COSINE multiple scores",
			scores:     []float64{1, 0, -1},
			metricType: MetricTypeCosine,
			want:       []float64{1.0, 0.5, 0.0},
		},
		{
			name:       "empty scores",
			scores:     []float64{},
			metricType: MetricTypeL2,
			want:       []float64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scores := make([]float64, len(tt.scores))
			copy(scores, tt.scores)
			NormalizeScores(scores, tt.metricType)
			for i := range scores {
				assert.InDelta(t, tt.want[i], scores[i], 1e-9)
			}
		})
	}
}

func TestMinMaxNormalize(t *testing.T) {
	tests := []struct {
		name   string
		scores []float64
		want   []float64
	}{
		{
			name:   "normal range",
			scores: []float64{0, 50, 100},
			want:   []float64{0.0, 0.5, 1.0},
		},
		{
			name:   "negative to positive range",
			scores: []float64{-10, 0, 10},
			want:   []float64{0.0, 0.5, 1.0},
		},
		{
			name:   "all equal scores",
			scores: []float64{5, 5, 5},
			want:   []float64{1.0, 1.0, 1.0},
		},
		{
			name:   "single score",
			scores: []float64{42},
			want:   []float64{1.0},
		},
		{
			name:   "empty scores",
			scores: []float64{},
			want:   []float64{},
		},
		{
			name:   "two scores",
			scores: []float64{0, 1},
			want:   []float64{0.0, 1.0},
		},
		{
			name:   "reverse order",
			scores: []float64{100, 50, 0},
			want:   []float64{1.0, 0.5, 0.0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MinMaxNormalize(tt.scores)
			assert.Equal(t, len(tt.want), len(got))
			for i := range got {
				assert.InDelta(t, tt.want[i], got[i], 1e-9)
			}
		})
	}
}

func TestInvertScores(t *testing.T) {
	tests := []struct {
		name   string
		scores []float64
		want   []float64
	}{
		{
			name:   "zero score",
			scores: []float64{0},
			want:   []float64{1.0},
		},
		{
			name:   "score of 1",
			scores: []float64{1},
			want:   []float64{0.5},
		},
		{
			name:   "multiple scores",
			scores: []float64{0, 1, 3, 9},
			want:   []float64{1.0, 0.5, 0.25, 0.1},
		},
		{
			name:   "negative score (clamped to 0)",
			scores: []float64{-5},
			want:   []float64{1.0},
		},
		{
			name:   "empty scores",
			scores: []float64{},
			want:   []float64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InvertScores(tt.scores)
			assert.Equal(t, len(tt.want), len(got))
			for i := range got {
				assert.InDelta(t, tt.want[i], got[i], 1e-9)
			}
		})
	}
}

func TestNormalizeL2(t *testing.T) {
	// L2: 1 / (1 + distance)
	assert.InDelta(t, 1.0, normalizeL2(0), 1e-9)
	assert.InDelta(t, 0.5, normalizeL2(1), 1e-9)
	assert.InDelta(t, 1.0/3.0, normalizeL2(2), 1e-9)
	assert.InDelta(t, 0.1, normalizeL2(9), 1e-9)
}

func TestNormalizeIP(t *testing.T) {
	// IP: (score + 1) / 2
	assert.InDelta(t, 1.0, normalizeIP(1), 1e-9)
	assert.InDelta(t, 0.5, normalizeIP(0), 1e-9)
	assert.InDelta(t, 0.0, normalizeIP(-1), 1e-9)
	assert.InDelta(t, 0.75, normalizeIP(0.5), 1e-9)
}

func TestNormalizeCosine(t *testing.T) {
	// COSINE: (score + 1) / 2
	assert.InDelta(t, 1.0, normalizeCosine(1), 1e-9)
	assert.InDelta(t, 0.5, normalizeCosine(0), 1e-9)
	assert.InDelta(t, 0.0, normalizeCosine(-1), 1e-9)
}

func TestNormalizeBM25(t *testing.T) {
	// BM25: 1 / (1 + e^(-score))
	assert.InDelta(t, 0.5, normalizeBM25(0), 1e-9)
	assert.Greater(t, normalizeBM25(1), 0.5)
	assert.Less(t, normalizeBM25(1), 1.0)
	assert.Greater(t, normalizeBM25(10), 0.99)
}
