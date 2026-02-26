//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package passhatk provides Pass^k evaluator for consistency evaluation.
// Based on τ-bench methodology: Pass^k = C(c,k) / C(n,k).
// This measures the probability that ALL k samples are consistent.
package passhatk

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/benchmark/summary/trpc-agent-go-impl/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/summary/trpc-agent-go-impl/evaluation/evaluator/comparator"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Default k values for Pass^k evaluation.
const (
	DefaultK1 = 1
	DefaultK2 = 2
	DefaultK4 = 4
)

// PassHatKEvaluator evaluates consistency using Pass^k metric (τ-bench style).
// Pass^k = C(c,k) / C(n,k) where c = success count, n = total runs.
// This measures the probability that ALL k samples are consistent.
type PassHatKEvaluator struct {
	name       string
	kValues    []int
	comparator comparator.ConversationComparator
	threshold  float64
}

// Option configures PassHatKEvaluator.
type Option func(*PassHatKEvaluator)

// WithName sets the evaluator name.
func WithName(name string) Option {
	return func(e *PassHatKEvaluator) {
		e.name = name
	}
}

// WithKValues sets the k values for Pass^k evaluation.
func WithKValues(kValues []int) Option {
	return func(e *PassHatKEvaluator) {
		e.kValues = kValues
	}
}

// WithThreshold sets the threshold for consistency determination.
func WithThreshold(threshold float64) Option {
	return func(e *PassHatKEvaluator) {
		e.threshold = threshold
	}
}

// New creates a new PassHatKEvaluator.
func New(
	comp comparator.ConversationComparator,
	opts ...Option,
) *PassHatKEvaluator {
	e := &PassHatKEvaluator{
		name:       "pass_hat_k",
		kValues:    []int{DefaultK1, DefaultK2, DefaultK4},
		comparator: comp,
		threshold:  0.7,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Name returns the evaluator name.
func (e *PassHatKEvaluator) Name() string {
	return e.name
}

// Description returns the evaluator description.
func (e *PassHatKEvaluator) Description() string {
	return fmt.Sprintf(
		"Pass^k evaluator (τ-bench) with k values %v, threshold %.2f",
		e.kValues, e.threshold,
	)
}

// Evaluate implements Evaluator interface for single-run evaluation.
func (e *PassHatKEvaluator) Evaluate(
	ctx context.Context,
	actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric,
) (*evaluator.EvaluateResult, error) {
	if len(actuals) == 0 || len(expecteds) == 0 {
		return &evaluator.EvaluateResult{
			OverallScore:  0,
			OverallStatus: status.EvalStatusNotEvaluated,
		}, nil
	}
	consistent, score, err := e.comparator.IsConversationConsistent(
		ctx, expecteds, actuals,
	)
	if err != nil {
		return nil, fmt.Errorf("consistency check failed: %w", err)
	}
	evalStatus := status.EvalStatusFailed
	if consistent {
		evalStatus = status.EvalStatusPassed
	}
	return &evaluator.EvaluateResult{
		OverallScore:  score,
		OverallStatus: evalStatus,
		Details: map[string]any{
			"consistent": consistent,
			"score":      score,
		},
	}, nil
}

// EvaluateMultiRun evaluates multiple runs using Pass^k metric (τ-bench style).
// Pass^k = C(c,k) / C(n,k) measures probability ALL k samples are consistent.
// baselineRuns: results from baseline mode (full history).
// testRuns: results from test mode (summary mode).
func (e *PassHatKEvaluator) EvaluateMultiRun(
	ctx context.Context,
	baselineRuns, testRuns [][]*evalset.Invocation,
	evalMetric *metric.EvalMetric,
) (*evaluator.EvaluateResult, error) {
	if len(baselineRuns) == 0 || len(testRuns) == 0 {
		return &evaluator.EvaluateResult{
			OverallScore:  0,
			OverallStatus: status.EvalStatusNotEvaluated,
		}, nil
	}
	// Use first baseline run as reference.
	baseline := baselineRuns[0]
	n := len(testRuns)
	// Count successful (consistent) runs.
	c := 0
	consistencyScores := make([]float64, 0, n)
	for i, testRun := range testRuns {
		consistent, score, err := e.comparator.IsConversationConsistent(
			ctx, baseline, testRun,
		)
		if err != nil {
			return nil, fmt.Errorf("consistency check for run %d failed: %w", i, err)
		}
		consistencyScores = append(consistencyScores, score)
		if consistent {
			c++
		}
	}
	// Calculate Pass^k for each k value using τ-bench formula.
	// Pass^k = C(c,k) / C(n,k).
	passHatK := make(map[int]float64)
	for _, k := range e.kValues {
		passHatK[k] = calculatePassHatK(n, c, k)
	}
	// Calculate average consistency score.
	var totalScore float64
	for _, s := range consistencyScores {
		totalScore += s
	}
	avgScore := totalScore / float64(n)
	// Calculate variance for stability analysis.
	variance := calculateVariance(consistencyScores)
	// Determine overall status based on Pass^1.
	evalStatus := status.EvalStatusFailed
	if passHatK[DefaultK1] >= e.threshold {
		evalStatus = status.EvalStatusPassed
	}
	return &evaluator.EvaluateResult{
		OverallScore:  avgScore,
		OverallStatus: evalStatus,
		Details: map[string]any{
			"pass_hat_1":         passHatK[DefaultK1],
			"pass_hat_2":         passHatK[DefaultK2],
			"pass_hat_4":         passHatK[DefaultK4],
			"pass_hat_k":         passHatK,
			"k_values":           e.kValues,
			"total_runs":         n,
			"success_count":      c,
			"consistency_scores": consistencyScores,
			"variance":           variance,
			"avg_score":          avgScore,
		},
	}, nil
}

// calculatePassHatK computes Pass^k = C(c,k) / C(n,k).
// n = total runs, c = success count, k = target count.
// This is the probability that ALL k samples are successful.
func calculatePassHatK(n, c, k int) float64 {
	if k > n || k > c {
		return 0.0
	}
	if k == 0 {
		return 1.0
	}
	// C(c,k) / C(n,k) = [c! / (k!(c-k)!)] / [n! / (k!(n-k)!)]
	//                 = [c! * (n-k)!] / [(c-k)! * n!]
	// Compute incrementally to avoid overflow.
	result := 1.0
	for i := range k {
		result *= float64(c-i) / float64(n-i)
	}
	return result
}

func calculateVariance(scores []float64) float64 {
	if len(scores) == 0 {
		return 0
	}
	var sum float64
	for _, s := range scores {
		sum += s
	}
	mean := sum / float64(len(scores))
	var varianceSum float64
	for _, s := range scores {
		diff := s - mean
		varianceSum += diff * diff
	}
	return varianceSum / float64(len(scores))
}
