//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package rouge defines ROUGE scoring criteria.
package rouge

import (
	"context"
	"fmt"

	irouge "trpc.group/trpc-go/trpc-agent-go/evaluation/internal/rouge"
)

// Tokenizer tokenizes text into a list of tokens.
type Tokenizer = irouge.Tokenizer

// RougeCriterion configures ROUGE scoring for evaluation.
type RougeCriterion struct {
	// Ignore skips ROUGE scoring when true.
	Ignore bool `json:"ignore,omitempty"`
	// RougeType selects the ROUGE variant and must be "rougeN" where N is a positive integer such as "rouge1" or "rouge2", "rougeL", or "rougeLsum".
	RougeType string `json:"rougeType,omitempty"`
	// Measure selects which component is used as the primary score and defaults to "f1" when unset.
	Measure RougeMeasure `json:"measure,omitempty"`
	// Threshold defines the minimum score requirement for each measure.
	Threshold Score `json:"threshold,omitempty"`
	// UseStemmer enables Porter stemming for the built-in tokenizer and is ignored when a custom tokenizer is used.
	UseStemmer bool `json:"useStemmer,omitempty"`
	// SplitSummaries splits summaries into sentences for rougeLsum and is ignored for other rouge types.
	SplitSummaries bool `json:"splitSummaries,omitempty"`
	// Tokenizer overrides the built-in tokenization when provided.
	Tokenizer Tokenizer `json:"-"`
}

// RougeMeasure selects which ROUGE component should be used as a scalar score.
type RougeMeasure string

const (
	// RougeMeasureF1 uses the F1 score.
	RougeMeasureF1 RougeMeasure = "f1"
	// RougeMeasurePrecision uses the precision score.
	RougeMeasurePrecision RougeMeasure = "precision"
	// RougeMeasureRecall uses the recall score.
	RougeMeasureRecall RougeMeasure = "recall"
)

// Score holds ROUGE precision, recall and F1.
type Score struct {
	// Precision is the fraction of predicted units that match the reference in range [0, 1].
	Precision float64 `json:"precision,omitempty"`
	// Recall is the fraction of reference units that are matched by the prediction in range [0, 1].
	Recall float64 `json:"recall,omitempty"`
	// F1 is the harmonic mean of precision and recall in range [0, 1].
	F1 float64 `json:"f1,omitempty"`
}

// MatchResult holds ROUGE scoring output for a single comparison.
type MatchResult struct {
	// RougeType is the configured ROUGE variant name.
	RougeType string
	// Measure is the score component used for Value.
	Measure RougeMeasure
	// Value is the scalar score selected by Measure.
	Value float64
	// Score holds the full precision/recall/F1 values.
	Score Score
	// Passed reports whether the computed scores meet the configured thresholds.
	Passed bool
}

// Reason formats the scoring output for display.
func (r MatchResult) Reason() string {
	return fmt.Sprintf("%s %s=%.6f precision=%.6f recall=%.6f f1=%.6f",
		r.RougeType, r.Measure, r.Value, r.Score.Precision, r.Score.Recall, r.Score.F1)
}

// Match computes ROUGE scores between target and prediction based on the configured options.
func (c *RougeCriterion) Match(ctx context.Context, target, prediction string) (*MatchResult, error) {
	if c == nil {
		return nil, fmt.Errorf("rouge criterion is nil")
	}
	if c.Ignore {
		return &MatchResult{
			RougeType: c.RougeType,
			Measure:   RougeMeasureF1,
			Value:     1.0,
			Score:     Score{Precision: 1.0, Recall: 1.0, F1: 1.0},
			Passed:    true,
		}, nil
	}
	if c.RougeType == "" {
		return nil, fmt.Errorf("rouge criterion requires rougeType")
	}
	measure := c.Measure
	if measure == "" {
		measure = RougeMeasureF1
	}
	switch measure {
	case RougeMeasureF1, RougeMeasurePrecision, RougeMeasureRecall:
	default:
		return nil, fmt.Errorf("unsupported rouge measure: %s", measure)
	}
	computeOpt := []irouge.Option{
		irouge.WithRougeTypes(c.RougeType),
		irouge.WithStemmer(c.UseStemmer),
		irouge.WithSplitSummaries(c.SplitSummaries),
	}
	if c.Tokenizer != nil {
		computeOpt = append(computeOpt, irouge.WithTokenizer(c.Tokenizer))
	}

	scores, err := irouge.Compute(ctx, target, prediction, computeOpt...)
	if err != nil {
		return nil, err
	}
	s, ok := scores[c.RougeType]
	if !ok {
		return nil, fmt.Errorf("missing rouge score for type: %s", c.RougeType)
	}
	score := Score{Precision: s.Precision, Recall: s.Recall, F1: s.FMeasure}
	var value float64
	switch measure {
	case RougeMeasureF1:
		value = score.F1
	case RougeMeasurePrecision:
		value = score.Precision
	case RougeMeasureRecall:
		value = score.Recall
	default:
		return nil, fmt.Errorf("unsupported rouge measure: %s", measure)
	}
	passed := score.Precision >= c.Threshold.Precision &&
		score.Recall >= c.Threshold.Recall &&
		score.F1 >= c.Threshold.F1
	return &MatchResult{
		RougeType: c.RougeType,
		Measure:   measure,
		Value:     value,
		Score:     score,
		Passed:    passed,
	}, nil
}
