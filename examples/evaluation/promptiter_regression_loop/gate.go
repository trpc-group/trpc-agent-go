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
)

const defaultBootstrapResamples = 10000

// EvaluateGate applies all score, safety, stability, confidence, and budget checks to a comparison.
func EvaluateGate(comparison Comparison, config GateConfig) (GateResult, error) {
	if len(comparison.Deltas) == 0 {
		return GateResult{}, errors.New("comparison has no case deltas")
	}
	if err := validateGateConfig(comparison, config); err != nil {
		return GateResult{}, err
	}
	resamples := config.BootstrapResamples
	if resamples == 0 {
		resamples = defaultBootstrapResamples
	}
	deltas := make([]float64, len(comparison.Deltas))
	var newHardFailures, criticalRegressions int
	for i, delta := range comparison.Deltas {
		deltas[i] = delta.ScoreDelta
		if delta.NewHardFailure {
			newHardFailures++
		}
		if delta.CriticalRegression {
			criticalRegressions++
		}
	}
	interval, err := PairedBootstrap90(deltas, config.BootstrapSeed, resamples)
	if err != nil {
		return GateResult{}, fmt.Errorf("paired bootstrap: %w", err)
	}

	checks := []GateCheck{
		newGateCheck("minimum_score_gain", comparison.MeanScoreGain >= config.MinScoreGain,
			comparison.MeanScoreGain, config.MinScoreGain, ">=", "candidate validation mean must improve by the configured minimum"),
		newGateCheck("no_new_hard_failure", newHardFailures == 0,
			float64(newHardFailures), 0, "==", "candidate must not introduce a hard failure"),
		newGateCheck("critical_cases_do_not_regress", criticalRegressions == 0,
			float64(criticalRegressions), 0, "==", "critical case score and Pass^k stability must not regress"),
		newGateCheck("pass_power_k_does_not_regress",
			comparison.CandidatePassPowerKRate >= comparison.BaselinePassPowerKRate,
			comparison.CandidatePassPowerKRate, comparison.BaselinePassPowerKRate, ">=",
			fmt.Sprintf("candidate Pass^%d rate must be at least the baseline rate", comparison.PassK)),
		newGateCheck("bootstrap_ci_lower_bound", interval.Lower >= 0,
			interval.Lower, 0, ">=", "paired bootstrap 90% confidence interval lower bound must be non-negative"),
	}
	checks = append(checks,
		budgetCheck("calls_budget", comparison.Usage.Calls, config.MaxCalls),
		budgetCheck("tokens_budget", comparison.Usage.Tokens(), config.MaxTokens),
		costBudgetCheck(comparison.Usage.CostCNY, config.MaxCostCNY),
	)

	result := GateResult{Accepted: true, ConfidenceInterval: interval, Checks: checks}
	for _, check := range checks {
		if !check.Passed {
			result.Accepted = false
			result.FailedChecks = append(result.FailedChecks, check.Name)
		}
	}
	return result, nil
}

func validateGateConfig(comparison Comparison, config GateConfig) error {
	if config.PassK <= 0 {
		return errors.New("gate passK must be positive")
	}
	if comparison.PassK != config.PassK {
		return fmt.Errorf("gate passK %d differs from comparison passK %d", config.PassK, comparison.PassK)
	}
	if math.IsNaN(config.MinScoreGain) || math.IsInf(config.MinScoreGain, 0) {
		return errors.New("minimum score gain must be finite")
	}
	if config.BootstrapResamples < 0 {
		return errors.New("bootstrap resamples cannot be negative")
	}
	if config.MaxCalls < 0 || config.MaxTokens < 0 || config.MaxCostCNY < 0 ||
		math.IsNaN(config.MaxCostCNY) || math.IsInf(config.MaxCostCNY, 0) {
		return errors.New("gate budgets cannot be negative or non-finite")
	}
	return nil
}

func newGateCheck(name string, passed bool, observed, threshold float64, operator, detail string) GateCheck {
	return GateCheck{Name: name, Passed: passed, Observed: observed, Threshold: threshold, Operator: operator, Detail: detail}
}

func budgetCheck(name string, observed, maximum int) GateCheck {
	if maximum == 0 {
		return newGateCheck(name, true, float64(observed), 0, "disabled", "budget is disabled")
	}
	return newGateCheck(name, observed <= maximum, float64(observed), float64(maximum), "<=", "usage must remain within budget")
}

func costBudgetCheck(observed, maximum float64) GateCheck {
	if maximum == 0 {
		return newGateCheck("cost_budget_cny", true, observed, 0, "disabled", "budget is disabled")
	}
	return newGateCheck("cost_budget_cny", observed <= maximum, observed, maximum, "<=", "cost must remain within budget")
}
