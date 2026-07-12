//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func ComputeDeltas(baseline, candidate *engine.EvaluationResult) []CaseDelta {
	baselineMap := buildCaseMap(baseline)
	candidateMap := buildCaseMap(candidate)

	caseIDs := make(map[string]struct{})
	for id := range baselineMap {
		caseIDs[id] = struct{}{}
	}
	for id := range candidateMap {
		caseIDs[id] = struct{}{}
	}

	var deltas []CaseDelta
	for id := range caseIDs {
		baselineCase, baselineOK := baselineMap[id]
		candidateCase, candidateOK := candidateMap[id]

		if !baselineOK {
			deltas = append(deltas, CaseDelta{
				EvalCaseID:      id,
				DeltaType:       DeltaMissing,
				BaselinePassed:  false,
				CandidatePassed: isCasePassed(candidateCase),
				CandidateScore:  getCaseScore(candidateCase),
			})
			continue
		}

		if !candidateOK {
			deltas = append(deltas, CaseDelta{
				EvalCaseID:      id,
				DeltaType:       DeltaMissing,
				BaselineScore:   getCaseScore(baselineCase),
				BaselinePassed:  isCasePassed(baselineCase),
				CandidatePassed: false,
			})
			continue
		}

		delta := computeCaseDelta(baselineCase, candidateCase)
		deltas = append(deltas, delta)
	}

	return deltas
}

func buildCaseMap(result *engine.EvaluationResult) map[string]engine.CaseResult {
	m := make(map[string]engine.CaseResult)
	if result == nil {
		return m
	}

	for _, evalSet := range result.EvalSets {
		for _, caseResult := range evalSet.Cases {
			m[caseResult.EvalCaseID] = caseResult
		}
	}

	return m
}

func computeCaseDelta(baseline, candidate engine.CaseResult) CaseDelta {
	baselineScore := getCaseScore(baseline)
	candidateScore := getCaseScore(candidate)
	scoreDelta := candidateScore - baselineScore

	baselinePassed := isCasePassed(baseline)
	candidatePassed := isCasePassed(candidate)

	deltaType := classifyDeltaType(baselinePassed, candidatePassed, scoreDelta)

	return CaseDelta{
		EvalCaseID:      baseline.EvalCaseID,
		EvalSetID:       baseline.EvalSetID,
		BaselineScore:   baselineScore,
		CandidateScore:  candidateScore,
		ScoreDelta:      scoreDelta,
		BaselinePassed:  baselinePassed,
		CandidatePassed: candidatePassed,
		DeltaType:       deltaType,
	}
}

func getCaseScore(caseResult engine.CaseResult) float64 {
	if len(caseResult.Metrics) == 0 {
		return 0.0
	}

	total := 0.0
	count := 0
	for _, metric := range caseResult.Metrics {
		total += metric.Score
		count++
	}

	if count == 0 {
		return 0.0
	}

	return total / float64(count)
}

func isCasePassed(caseResult engine.CaseResult) bool {
	for _, metric := range caseResult.Metrics {
		if metric.Status == status.EvalStatusFailed {
			return false
		}
	}
	return true
}

func classifyDeltaType(baselinePassed, candidatePassed bool, scoreDelta float64) DeltaType {
	switch {
	case !baselinePassed && candidatePassed:
		return DeltaNewlyPassed
	case baselinePassed && !candidatePassed:
		return DeltaNewlyFailed
	case scoreDelta > 0.0001:
		return DeltaScoreUp
	case scoreDelta < -0.0001:
		return DeltaScoreDown
	default:
		return DeltaUnchanged
	}
}

func GetDeltaSummary(deltas []CaseDelta) map[string]int {
	summary := make(map[string]int)
	for _, delta := range deltas {
		summary[string(delta.DeltaType)]++
	}
	return summary
}

func CountNewlyFailed(deltas []CaseDelta) int {
	count := 0
	for _, delta := range deltas {
		if delta.DeltaType == DeltaNewlyFailed {
			count++
		}
	}
	return count
}

func CountRegressedCases(deltas []CaseDelta) int {
	count := 0
	for _, delta := range deltas {
		if delta.DeltaType == DeltaNewlyFailed || delta.DeltaType == DeltaScoreDown {
			count++
		}
	}
	return count
}
