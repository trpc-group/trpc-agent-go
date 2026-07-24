//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestUsageBuildersRejectMissingEngineEvidence(t *testing.T) {
	_, err := buildUsageSummary(nil, UsageSupplement{})
	require.ErrorContains(t, err, "PromptIter result is nil")
	_, err = buildCandidateUsages(nil, UsageSupplement{})
	require.ErrorContains(t, err, "result and baseline validation are required")
	_, err = buildCandidateUsages(&engine.RunResult{}, UsageSupplement{})
	require.ErrorContains(t, err, "result and baseline validation are required")
}

func TestCostBreakdownRequiresExactPerRoundAccounting(t *testing.T) {
	rounds := []engine.RoundResult{{Round: 1}, {Round: 2}}
	tests := []struct {
		name string
		cost CostBreakdown
		want string
	}{
		{name: "invalid round id", cost: CostBreakdown{RoundEstimatedCosts: map[int]float64{0: 1}}, want: "must be positive"},
		{name: "non finite baseline", cost: CostBreakdown{BaselineEstimatedCost: math.NaN()}, want: "finite and non-negative"},
		{name: "unknown cost has breakdown", cost: CostBreakdown{BaselineEstimatedCost: 1}, want: "marked unknown"},
		{name: "known cost missing entries", cost: CostBreakdown{CostEstimate: CostEstimate{CostKnown: true, PricingSource: "table"}}, want: "one cost entry for every round"},
		{name: "known cost missing round", cost: CostBreakdown{CostEstimate: CostEstimate{CostKnown: true, PricingSource: "table", EstimatedCost: 2}, RoundEstimatedCosts: map[int]float64{1: 1, 3: 1}}, want: "missing round 2"},
		{name: "known cost total mismatch", cost: CostBreakdown{CostEstimate: CostEstimate{CostKnown: true, PricingSource: "table", EstimatedCost: 4}, BaselineEstimatedCost: 1, RoundEstimatedCosts: map[int]float64{1: 1, 2: 1}}, want: "do not match total"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorContains(t, test.cost.validate(rounds), test.want)
		})
	}
}

func TestUsageSummaryRejectsInvalidAndOverflowingTelemetry(t *testing.T) {
	validCost := CostEstimate{}
	tests := []struct {
		name    string
		usage   promptiter.Usage
		latency time.Duration
		want    string
	}{
		{name: "negative calls", usage: promptiter.Usage{Calls: -1}, want: "must be non-negative"},
		{name: "negative latency", latency: -time.Second, want: "must be non-negative"},
		{name: "token overflow", usage: promptiter.Usage{PromptTokens: math.MaxInt64, CompletionTokens: 1}, want: "overflows int64"},
		{name: "contradictory total", usage: promptiter.Usage{PromptTokens: 8, CompletionTokens: 5, TotalTokens: 12}, want: "smaller than input plus output"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := usageSummary(test.usage, test.latency, validCost)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestBuildCandidateUsagesSeparatesRoundAndCumulativeTotals(t *testing.T) {
	source := &engine.RunResult{
		BaselineValidation: &engine.EvaluationResult{
			Duration: time.Second,
			Usage:    promptiter.Usage{Calls: 1, PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3, Complete: true},
		},
		Rounds: []engine.RoundResult{
			{Round: 1, Duration: 2 * time.Second, Usage: promptiter.Usage{Calls: 2, PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5, Complete: true}},
			{Round: 2, Duration: 3 * time.Second, Usage: promptiter.Usage{Calls: 3, PromptTokens: 4, CompletionTokens: 3, TotalTokens: 7, Complete: true}},
		},
	}
	supplement := UsageSupplement{CostBreakdown: CostBreakdown{
		CostEstimate:          CostEstimate{CostKnown: true, EstimatedCost: .6, PricingSource: "fixture"},
		BaselineEstimatedCost: .1,
		RoundEstimatedCosts:   map[int]float64{1: .2, 2: .3},
	}}
	result, err := buildCandidateUsages(source, supplement)
	require.NoError(t, err)
	assert.Equal(t, 2, result[1].round.Calls)
	assert.Equal(t, 3, result[1].cumulative.Calls)
	assert.Equal(t, int64(8), result[1].cumulative.TotalTokens)
	assert.Equal(t, 3*time.Second, result[1].cumulative.PromptIterLatency)
	assert.InDelta(t, .3, result[1].cumulative.EstimatedCost, 1e-9)
	assert.Equal(t, 6, result[2].cumulative.Calls)
	assert.Equal(t, int64(15), result[2].cumulative.TotalTokens)
	assert.Equal(t, 6*time.Second, result[2].cumulative.PromptIterLatency)
	assert.InDelta(t, .6, result[2].cumulative.EstimatedCost, 1e-9)
}
