//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "fmt"

// DecideGate applies regression, hard-fail, critical-case, and budget checks.
func DecideGate(cfg GateConfig, delta DeltaSummary, cost CostSummary) GateDecision {
	decision := GateDecision{Accepted: true}
	if delta.ScoreDelta < cfg.MinValidationGain {
		decision.Accepted = false
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"validation score gain %.4f is below threshold %.4f",
			delta.ScoreDelta,
			cfg.MinValidationGain,
		))
	}
	if delta.NewlyFailed > cfg.MaxNewHardFails {
		decision.Accepted = false
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"new hard fails %d exceed limit %d",
			delta.NewlyFailed,
			cfg.MaxNewHardFails,
		))
	}
	if cfg.RejectCriticalRegression && delta.CriticalRegressed > 0 {
		decision.Accepted = false
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"%d critical validation case(s) regressed",
			delta.CriticalRegressed,
		))
	}
	if cfg.MaxCalls > 0 && cost.TotalCalls > cfg.MaxCalls {
		decision.Accepted = false
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"total calls %d exceed budget %d",
			cost.TotalCalls,
			cfg.MaxCalls,
		))
	}
	if cfg.MaxEstimatedUSD > 0 && cost.EstimatedUSD > cfg.MaxEstimatedUSD {
		decision.Accepted = false
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"estimated cost %.6f exceeds budget %.6f",
			cost.EstimatedUSD,
			cfg.MaxEstimatedUSD,
		))
	}
	if decision.Accepted {
		decision.Reasons = append(decision.Reasons, "candidate passed validation score, hard-fail, critical-case, and budget gates")
	}
	return decision
}
