//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pipeline

import "fmt"

// AcceptanceGates evaluates all configured gates for one candidate prompt.
// It returns an AcceptanceDecision that the pipeline uses to decide whether
// to commit the candidate to the next round.
//
// Gates are evaluated in order; any gate failure is appended to Reasons so
// operators can inspect why a candidate was rejected.
type AcceptanceGates struct {
	Config AcceptanceGateConfig
}

// NewAcceptanceGates builds gates from a user config.
func NewAcceptanceGates(cfg AcceptanceGateConfig) *AcceptanceGates {
	return &AcceptanceGates{Config: cfg}
}

// Evaluate runs all gates for the candidate vs baseline comparison.
func (g *AcceptanceGates) Evaluate(
	baselineScore, candidateScore float64,
	deltas []CaseDelta,
	newHardFailCount, keyCaseDegradeCount int,
	cost CostEstimate,
) *AcceptanceDecision {
	d := &AcceptanceDecision{
		Accepted:            true,
		ScoreDelta:          candidateScore - baselineScore,
		PerCaseDelta:        deltas,
		HardFailNewCount:    newHardFailCount,
		KeyCaseDegradeCount: keyCaseDegradeCount,
		Cost:                cost.EstimatedCostUSD,
		Reasons:             make([]string, 0, 4),
	}

	// Gate 1: minimum overall validation score gain.
	if d.ScoreDelta >= g.Config.MinValidationScoreGain {
		d.Reasons = append(d.Reasons,
			fmt.Sprintf("score_delta=%+.4f meets min_score_gain=%+.4f",
				d.ScoreDelta, g.Config.MinValidationScoreGain))
	} else {
		d.Accepted = false
		d.Reasons = append(d.Reasons,
			fmt.Sprintf("score_delta=%+.4f below min_score_gain=%+.4f (REJECT)",
				d.ScoreDelta, g.Config.MinValidationScoreGain))
	}

	// Gate 2: no new hard fail unless explicitly allowed.
	if !g.Config.AllowNewHardFail {
		if newHardFailCount == 0 {
			d.Reasons = append(d.Reasons,
				fmt.Sprintf("no newly introduced hard fails (count=%d)", newHardFailCount))
		} else {
			d.Accepted = false
			d.Reasons = append(d.Reasons,
				fmt.Sprintf("introduced %d new hard fail(s); gate AllowNewHardFail=false (REJECT)",
					newHardFailCount))
		}
	} else {
		d.Reasons = append(d.Reasons,
			fmt.Sprintf("gate AllowNewHardFail=true; observed %d new hard fails",
				newHardFailCount))
	}

	// Gate 3: key cases must not degrade.
	if len(g.Config.KeyCaseIDs) == 0 {
		// When no explicit key case IDs are provided, auto-detect cases labelled
		// "hardfail_guard" (the default anchor case provided by this pipeline).
		if keyCaseDegradeCount == 0 {
			d.Reasons = append(d.Reasons,
				fmt.Sprintf("no implicit key case degradations (count=%d)", keyCaseDegradeCount))
		} else {
			d.Accepted = false
			d.Reasons = append(d.Reasons,
				fmt.Sprintf("%d implicit key case(s) degraded; gate requires KeyCaseNoDegrade (REJECT)",
					keyCaseDegradeCount))
		}
	} else {
		explicitDegrade := 0
		for _, id := range g.Config.KeyCaseIDs {
			for _, delta := range deltas {
				if delta.EvalCaseID == id && delta.ScoreDelta < -0.0001 {
					explicitDegrade++
				}
			}
		}
		if explicitDegrade == 0 {
			d.Reasons = append(d.Reasons,
				fmt.Sprintf("no explicit key case degradations for %d ids", len(g.Config.KeyCaseIDs)))
		} else {
			d.Accepted = false
			d.Reasons = append(d.Reasons,
				fmt.Sprintf("%d explicit key case(s) degraded (REJECT)", explicitDegrade))
		}
	}

	// Gate 4: cost within budget (only when budget > 0).
	if g.Config.MaxCostBudget > 0 {
		if cost.EstimatedCostUSD <= g.Config.MaxCostBudget+1e-9 {
			d.Reasons = append(d.Reasons,
				fmt.Sprintf("round cost $%.5f within budget $%.5f",
					cost.EstimatedCostUSD, g.Config.MaxCostBudget))
		} else {
			d.Accepted = false
			d.Reasons = append(d.Reasons,
				fmt.Sprintf("round cost $%.5f exceeds budget $%.5f (REJECT)",
					cost.EstimatedCostUSD, g.Config.MaxCostBudget))
		}
	} else {
		d.Reasons = append(d.Reasons, "cost budget disabled (MaxCostBudget=0)")
	}

	return d
}
