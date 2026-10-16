//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
)

const bootstrapConfidence = 0.90

// PairedBootstrap90 returns a deterministic percentile bootstrap confidence interval for the paired
// mean score delta. The same deltas, seed, and resample count always produce the same interval.
func PairedBootstrap90(deltas []float64, seed int64, resamples int) (ConfidenceInterval, error) {
	if len(deltas) == 0 {
		return ConfidenceInterval{}, errors.New("paired bootstrap requires at least one delta")
	}
	if resamples <= 0 {
		return ConfidenceInterval{}, errors.New("bootstrap resamples must be positive")
	}
	for i, delta := range deltas {
		if math.IsNaN(delta) || math.IsInf(delta, 0) {
			return ConfidenceInterval{}, fmt.Errorf("delta %d is not finite", i)
		}
	}

	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // Reproducibility, not cryptography, is required.
	means := make([]float64, resamples)
	for sample := range means {
		var total float64
		for range deltas {
			total += deltas[rng.Intn(len(deltas))]
		}
		means[sample] = total / float64(len(deltas))
	}
	sort.Float64s(means)
	alpha := (1 - bootstrapConfidence) / 2
	return ConfidenceInterval{
		Confidence: bootstrapConfidence,
		Lower:      percentile(means, alpha),
		Upper:      percentile(means, 1-alpha),
	}, nil
}

// percentile computes the linearly interpolated Type-7 sample quantile used by common statistics tools.
func percentile(sortedValues []float64, probability float64) float64 {
	if probability <= 0 || len(sortedValues) == 1 {
		return sortedValues[0]
	}
	if probability >= 1 {
		return sortedValues[len(sortedValues)-1]
	}
	position := probability * float64(len(sortedValues)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	weight := position - float64(lower)
	return sortedValues[lower]*(1-weight) + sortedValues[upper]*weight
}
