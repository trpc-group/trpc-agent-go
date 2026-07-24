//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

type candidateUsage struct {
	round      UsageSummary
	cumulative UsageSummary
}

func buildUsageSummary(
	source *engine.RunResult,
	supplement UsageSupplement,
) (UsageSummary, error) {
	if source == nil {
		return UsageSummary{}, errors.New("PromptIter result is nil")
	}
	if err := validateUsageSupplement(source, supplement); err != nil {
		return UsageSummary{}, err
	}
	return usageSummary(
		source.Usage,
		supplement.PromptIterLatency,
		supplement.CostEstimate,
	)
}

func buildCandidateUsages(
	source *engine.RunResult,
	supplement UsageSupplement,
) (map[int]candidateUsage, error) {
	if source == nil || source.BaselineValidation == nil {
		return nil, errors.New("PromptIter result and baseline validation are required")
	}
	if err := validateUsageSupplement(source, supplement); err != nil {
		return nil, err
	}
	result := make(map[int]candidateUsage, len(source.Rounds))
	cumulativeTelemetry := source.BaselineValidation.Usage
	cumulativeLatency := source.BaselineValidation.Duration
	cumulativeCost := supplement.BaselineEstimatedCost
	for index := range source.Rounds {
		round := &source.Rounds[index]
		roundCost := supplement.RoundEstimatedCosts[round.Round]
		roundSummary, err := usageSummary(
			round.Usage, round.Duration, CostEstimate{
				EstimatedCost: roundCost,
				CostKnown:     supplement.CostKnown,
				PricingSource: supplement.PricingSource,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("round %d usage: %w", round.Round, err)
		}
		cumulativeTelemetry = promptiter.MergeUsage(cumulativeTelemetry, round.Usage)
		cumulativeLatency += round.Duration
		cumulativeCost += roundCost
		cumulativeSummary, err := usageSummary(
			cumulativeTelemetry, cumulativeLatency, CostEstimate{
				EstimatedCost: cumulativeCost,
				CostKnown:     supplement.CostKnown,
				PricingSource: supplement.PricingSource,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("round %d cumulative usage: %w", round.Round, err)
		}
		result[round.Round] = candidateUsage{round: roundSummary, cumulative: cumulativeSummary}
	}
	return result, nil
}

func validateUsageSupplement(source *engine.RunResult, supplement UsageSupplement) error {
	if supplement.PromptIterLatency < 0 {
		return errors.New("usage latency must be non-negative")
	}
	return supplement.CostBreakdown.validate(source.Rounds)
}

func (cost CostEstimate) validate() error {
	if math.IsNaN(cost.EstimatedCost) || math.IsInf(cost.EstimatedCost, 0) || cost.EstimatedCost < 0 {
		return errors.New("estimated cost must be finite and non-negative")
	}
	if !cost.CostKnown {
		if cost.EstimatedCost != 0 || strings.TrimSpace(cost.PricingSource) != "" {
			return errors.New("cost value or pricing source is set while cost is marked unknown")
		}
		return nil
	}
	if strings.TrimSpace(cost.PricingSource) == "" {
		return errors.New("known estimated cost requires a pricing source")
	}
	return nil
}

func (cost CostBreakdown) validate(rounds []engine.RoundResult) error {
	if err := cost.CostEstimate.validate(); err != nil {
		return err
	}
	values := []float64{cost.BaselineEstimatedCost}
	for round, estimate := range cost.RoundEstimatedCosts {
		if round <= 0 {
			return fmt.Errorf("round estimated cost id %d must be positive", round)
		}
		values = append(values, estimate)
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return errors.New("cost breakdown values must be finite and non-negative")
		}
	}
	if !cost.CostKnown {
		if cost.BaselineEstimatedCost != 0 || len(cost.RoundEstimatedCosts) != 0 {
			return errors.New("cost breakdown is set while cost is marked unknown")
		}
		return nil
	}
	if len(cost.RoundEstimatedCosts) != len(rounds) {
		return errors.New("known estimated cost requires one cost entry for every round")
	}
	total := cost.BaselineEstimatedCost
	for _, round := range rounds {
		roundCost, exists := cost.RoundEstimatedCosts[round.Round]
		if !exists {
			return fmt.Errorf("known estimated cost is missing round %d", round.Round)
		}
		total += roundCost
	}
	if math.Abs(total-cost.EstimatedCost) > 1e-9 {
		return fmt.Errorf(
			"baseline and round estimated costs %.12g do not match total %.12g",
			total, cost.EstimatedCost,
		)
	}
	return nil
}

func usageSummary(
	source promptiter.Usage,
	latency time.Duration,
	cost CostEstimate,
) (UsageSummary, error) {
	if err := cost.validate(); err != nil {
		return UsageSummary{}, err
	}
	if source.Calls < 0 || source.PromptTokens < 0 || source.CompletionTokens < 0 ||
		source.TotalTokens < 0 || latency < 0 {
		return UsageSummary{}, errors.New("usage counts and latency must be non-negative")
	}
	observedTokens := source.PromptTokens + source.CompletionTokens
	if observedTokens < source.PromptTokens || observedTokens < source.CompletionTokens {
		return UsageSummary{}, errors.New("usage token total overflows int64")
	}
	totalTokens := source.TotalTokens
	if totalTokens == 0 {
		totalTokens = observedTokens
	} else if totalTokens < observedTokens {
		return UsageSummary{}, errors.New("total tokens are smaller than input plus output tokens")
	}
	return UsageSummary{
		Calls: source.Calls, InputTokens: source.PromptTokens,
		OutputTokens: source.CompletionTokens, TotalTokens: totalTokens,
		CostEstimate: cost, PromptIterLatency: latency, Complete: source.Complete,
		TelemetrySource: "promptiter_engine",
	}, nil
}
