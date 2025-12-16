//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package knowledge provides internal utilities for knowledge management.
package knowledge

import "math"

// MetricType represents the type of distance/similarity metric used in vector search.
type MetricType string

// Metric type constants.
const (
	MetricTypeL2     MetricType = "L2"     // Euclidean distance (lower is better)
	MetricTypeIP     MetricType = "IP"     // Inner product (higher is better for normalized vectors)
	MetricTypeCosine MetricType = "COSINE" // Cosine similarity (higher is better, range [-1, 1])
	MetricTypeBM25   MetricType = "BM25"   // BM25 sparse vector score (higher is better)
)

// NormalizeScore normalizes a raw score to the range [0, 1] based on the metric type.
// After normalization, higher scores always indicate better similarity.
//
// For L2 (Euclidean distance):
//   - Raw: [0, +∞), lower is better
//   - Normalized: 1 / (1 + distance), range (0, 1], higher is better
//
// For IP (Inner Product):
//   - Raw: (-∞, +∞), for normalized vectors [-1, 1], higher is better
//   - Normalized: (score + 1) / 2, range [0, 1], higher is better
//
// For COSINE:
//   - Raw: [-1, 1], higher is better
//   - Normalized: (score + 1) / 2, range [0, 1], higher is better
//
// For BM25:
//   - Raw: [0, +∞), higher is better
//   - Normalized using sigmoid: 1 / (1 + e^(-score)), range (0, 1), higher is better
func NormalizeScore(score float64, metricType MetricType) float64 {
	switch metricType {
	case MetricTypeL2:
		return normalizeL2(score)
	case MetricTypeIP:
		return normalizeIP(score)
	case MetricTypeCosine:
		return normalizeCosine(score)
	case MetricTypeBM25:
		return normalizeBM25(score)
	default:
		return score
	}
}

// normalizeL2 converts L2 distance to similarity score.
// L2 distance: [0, +∞), lower is better
// Returns: (0, 1], higher is better
// Formula: 1 / (1 + distance)
func normalizeL2(distance float64) float64 {
	if distance < 0 {
		distance = 0
	}
	return 1.0 / (1.0 + distance)
}

// normalizeIP converts inner product score to normalized similarity.
// IP score for normalized vectors: [-1, 1], higher is better
// Returns: [0, 1], higher is better
// Formula: (score + 1) / 2
func normalizeIP(score float64) float64 {
	// Clamp to [-1, 1] for normalized vectors
	if score < -1 {
		score = -1
	}
	if score > 1 {
		score = 1
	}
	return (score + 1.0) / 2.0
}

// normalizeCosine converts cosine similarity to normalized score.
// Cosine similarity: [-1, 1], higher is better
// Returns: [0, 1], higher is better
// Formula: (score + 1) / 2
func normalizeCosine(score float64) float64 {
	// Clamp to [-1, 1]
	if score < -1 {
		score = -1
	}
	if score > 1 {
		score = 1
	}
	return (score + 1.0) / 2.0
}

// normalizeBM25 converts BM25 score to normalized similarity using sigmoid.
// BM25 score: [0, +∞), higher is better
// Returns: (0, 1), higher is better
// Formula: 1 / (1 + e^(-score))
// Note: Uses scaled sigmoid for better distribution
func normalizeBM25(score float64) float64 {
	if score < 0 {
		score = 0
	}
	// Use sigmoid function: 1 / (1 + e^(-score))
	// Scale factor to spread scores better (BM25 scores are typically small)
	return 1.0 / (1.0 + math.Exp(-score))
}

// NormalizeScores normalizes a slice of scores in place.
func NormalizeScores(scores []float64, metricType MetricType) {
	for i, score := range scores {
		scores[i] = NormalizeScore(score, metricType)
	}
}

// MinMaxNormalize performs min-max normalization on a slice of scores.
// This is useful when the score range is unknown or varies significantly.
// Returns scores in range [0, 1], preserving the relative ordering.
// If all scores are equal, returns 1.0 for all.
func MinMaxNormalize(scores []float64) []float64 {
	if len(scores) == 0 {
		return scores
	}

	minScore, maxScore := scores[0], scores[0]
	for _, s := range scores {
		if s < minScore {
			minScore = s
		}
		if s > maxScore {
			maxScore = s
		}
	}

	result := make([]float64, len(scores))
	scoreRange := maxScore - minScore

	if scoreRange == 0 {
		// All scores are equal
		for i := range result {
			result[i] = 1.0
		}
		return result
	}

	for i, s := range scores {
		result[i] = (s - minScore) / scoreRange
	}
	return result
}

// InvertScores inverts scores for metrics where lower is better (like L2).
// Converts scores so that higher values indicate better similarity.
// Uses formula: 1 / (1 + score)
func InvertScores(scores []float64) []float64 {
	result := make([]float64, len(scores))
	for i, s := range scores {
		if s < 0 {
			s = 0
		}
		result[i] = 1.0 / (1.0 + s)
	}
	return result
}
