//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package gates provides multi-dimensional acceptance policies for evaluating candidate prompts.
package gates

import (
	"fmt"
)

// Config defines the thresholds for candidate acceptance.
type Config struct {
	MinValScoreGain  float64  `json:"min_val_score_gain"`
	AllowNewHardFail bool     `json:"allow_new_hard_fail"`
	KeyCaseIDs       []string `json:"key_case_ids"`
	MaxCostBudgetUSD float64  `json:"max_cost_budget_usd"`
}

// CaseDelta records the score and pass state changes for a single test case.
type CaseDelta struct {
	CaseID         string  `json:"case_id"`
	BaselineScore  float64 `json:"baseline_score"`
	CandidateScore float64 `json:"candidate_score"`
	ScoreDelta     float64 `json:"score_delta"`
	BaselinePass   bool    `json:"baseline_pass"`
	CandidatePass  bool    `json:"candidate_pass"`
	Transition     string  `json:"transition"` // "improved", "degraded", "unchanged", "new_hard_fail"
}

// GateDecision holds the output decision of candidate evaluation.
type GateDecision struct {
	Accepted      bool        `json:"accepted"`
	GateName      string      `json:"gate_name"`
	Reason        string      `json:"reason"`
	ValScoreDelta float64     `json:"val_score_delta"`
	NewHardFails  int         `json:"new_hard_fails"`
	CaseDeltas    []CaseDelta `json:"case_deltas"`
}

// EvaluateCandidate checks if the candidate evaluation results satisfy all acceptance gates.
func EvaluateCandidate(cfg Config, baselineScore, candidateScore float64, deltas []CaseDelta, currentCost float64) GateDecision {
	scoreGain := candidateScore - baselineScore
	decision := GateDecision{
		Accepted:      true,
		ValScoreDelta: scoreGain,
		CaseDeltas:    deltas,
	}

	// 1. Check Cost Budget
	if cfg.MaxCostBudgetUSD > 0 && currentCost > cfg.MaxCostBudgetUSD {
		decision.Accepted = false
		decision.GateName = "CostBudgetGuard"
		decision.Reason = fmt.Sprintf("Pipeline cost ($%.4f) exceeded max budget ($%.4f)", currentCost, cfg.MaxCostBudgetUSD)
		return decision
	}

	// 2. Check Hard Fail Guard & Key Cases
	newHardFails := 0
	keyCaseMap := make(map[string]bool)
	for _, k := range cfg.KeyCaseIDs {
		keyCaseMap[k] = true
	}

	for _, d := range deltas {
		if d.BaselinePass && !d.CandidatePass {
			newHardFails++
			if !cfg.AllowNewHardFail {
				decision.Accepted = false
				decision.GateName = "HardFailGuard"
				decision.Reason = fmt.Sprintf("Candidate introduced new hard fail on case '%s'", d.CaseID)
				decision.NewHardFails = newHardFails
				return decision
			}
		}

		if keyCaseMap[d.CaseID] && (d.ScoreDelta < 0 || (d.BaselinePass && !d.CandidatePass)) {
			decision.Accepted = false
			decision.GateName = "KeyCaseGuard"
			decision.Reason = fmt.Sprintf("Key case '%s' degraded (score delta: %.2f)", d.CaseID, d.ScoreDelta)
			return decision
		}
	}
	decision.NewHardFails = newHardFails

	// 3. Check Minimum Score Gain
	if scoreGain < cfg.MinValScoreGain {
		decision.Accepted = false
		decision.GateName = "MinValidationScoreGain"
		decision.Reason = fmt.Sprintf("Validation score gain (+%.4f) below required threshold (+%.4f)", scoreGain, cfg.MinValScoreGain)
		return decision
	}

	decision.GateName = "AllGatesPassed"
	decision.Reason = fmt.Sprintf("Candidate passed all gates with validation score gain +%.4f", scoreGain)
	return decision
}
