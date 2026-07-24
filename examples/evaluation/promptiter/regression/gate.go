//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "fmt"

func decideGate(
	config gateConfig,
	baseline evaluationSummary,
	candidate evaluationSummary,
	delta evaluationDelta,
) gateDecision {
	decision := gateDecision{
		Reasons: make([]string, 0),
		Checks:  make([]gateCheck, 0, 5),
	}
	addCheck := func(name string, passed bool, detail string) {
		decision.Checks = append(decision.Checks, gateCheck{
			Name:   name,
			Passed: passed,
			Detail: detail,
		})
		if !passed {
			decision.Reasons = append(decision.Reasons, detail)
		}
	}

	addCheck(
		"minimum_validation_gain",
		delta.ScoreDelta+scoreEpsilon >= config.MinValidationScoreGain,
		fmt.Sprintf(
			"validation score gain %.4f must be at least %.4f",
			delta.ScoreDelta,
			config.MinValidationScoreGain,
		),
	)

	hardFailPassed := config.AllowNewHardFails || delta.NewlyFailed == 0
	addCheck(
		"no_new_hard_fails",
		hardFailPassed,
		fmt.Sprintf(
			"candidate introduced %d new hard fail(s); allowNewHardFails=%t",
			delta.NewlyFailed,
			config.AllowNewHardFails,
		),
	)

	criticalPassed, criticalDetail := checkCriticalCases(config, baseline, candidate)
	addCheck("critical_cases", criticalPassed, criticalDetail)

	costPassed := config.MaxEstimatedCostUSD <= 0 ||
		candidate.Cost.EstimatedCostUSD <= config.MaxEstimatedCostUSD+scoreEpsilon
	addCheck(
		"cost_budget",
		costPassed,
		fmt.Sprintf(
			"candidate cost budget: estimated $%.6f, limit $%.6f",
			candidate.Cost.EstimatedCostUSD,
			config.MaxEstimatedCostUSD,
		),
	)

	toolCallsPassed := config.MaxToolCalls <= 0 || candidate.Cost.ToolCalls <= config.MaxToolCalls
	addCheck(
		"tool_call_budget",
		toolCallsPassed,
		fmt.Sprintf(
			"candidate tool-call budget: used %d, limit %d",
			candidate.Cost.ToolCalls,
			config.MaxToolCalls,
		),
	)

	decision.Accepted = len(decision.Reasons) == 0
	if decision.Accepted {
		decision.Reasons = []string{"all acceptance checks passed"}
	}
	return decision
}

func checkCriticalCases(
	config gateConfig,
	baseline evaluationSummary,
	candidate evaluationSummary,
) (bool, string) {
	if len(config.CriticalCaseIDs) == 0 {
		return true, "no critical cases configured"
	}
	baselineCases, err := indexCases(baseline.Cases)
	if err != nil {
		return false, fmt.Sprintf("critical case baseline index failed: %v", err)
	}
	candidateCases, err := indexCases(candidate.Cases)
	if err != nil {
		return false, fmt.Sprintf("critical case candidate index failed: %v", err)
	}
	for _, caseID := range config.CriticalCaseIDs {
		baselineCase, baselineOK := baselineCases[caseID]
		candidateCase, candidateOK := candidateCases[caseID]
		if !baselineOK || !candidateOK {
			return false, fmt.Sprintf("critical case %q is missing from an evaluation result", caseID)
		}
		drop := baselineCase.Score - candidateCase.Score
		if drop > config.MaxCriticalScoreDrop+scoreEpsilon {
			return false, fmt.Sprintf(
				"critical case %q score dropped by %.4f, limit %.4f",
				caseID,
				drop,
				config.MaxCriticalScoreDrop,
			)
		}
	}
	return true, fmt.Sprintf(
		"%d critical case(s) stayed within the %.4f score-drop limit",
		len(config.CriticalCaseIDs),
		config.MaxCriticalScoreDrop,
	)
}
