//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package rouge implements ROUGE scoring for text evaluation.
package rouge

// Score holds ROUGE precision, recall and F-measure.
type Score struct {
	// Precision is the fraction of predicted units that match the reference in range [0, 1].
	Precision float64
	// Recall is the fraction of reference units that are matched by the prediction in range [0, 1].
	Recall float64
	// FMeasure is the harmonic mean of precision and recall in range [0, 1].
	FMeasure float64
}

// fMeasure computes the harmonic mean of precision and recall.
func fMeasure(precision, recall float64) float64 {
	if precision+recall > 0 {
		return 2 * precision * recall / (precision + recall)
	}
	return 0
}
