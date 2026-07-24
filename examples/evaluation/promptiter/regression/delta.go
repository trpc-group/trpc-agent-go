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
	"sort"
)

const scoreEpsilon = 1e-9

func compareEvaluations(
	baseline evaluationSummary,
	candidate evaluationSummary,
) (evaluationDelta, error) {
	baselineCases, err := indexCases(baseline.Cases)
	if err != nil {
		return evaluationDelta{}, fmt.Errorf("index baseline cases: %w", err)
	}
	candidateCases, err := indexCases(candidate.Cases)
	if err != nil {
		return evaluationDelta{}, fmt.Errorf("index candidate cases: %w", err)
	}
	if len(baselineCases) != len(candidateCases) {
		return evaluationDelta{}, fmt.Errorf(
			"case count mismatch: baseline=%d candidate=%d",
			len(baselineCases),
			len(candidateCases),
		)
	}

	caseIDs := make([]string, 0, len(baselineCases))
	for caseID := range baselineCases {
		if _, ok := candidateCases[caseID]; !ok {
			return evaluationDelta{}, fmt.Errorf("candidate result is missing case %q", caseID)
		}
		caseIDs = append(caseIDs, caseID)
	}
	sort.Strings(caseIDs)

	delta := evaluationDelta{
		ScoreDelta: roundScore(candidate.Score - baseline.Score),
		Cases:      make([]caseDelta, 0, len(caseIDs)),
	}
	for _, caseID := range caseIDs {
		baselineCase := baselineCases[caseID]
		candidateCase := candidateCases[caseID]
		item := caseDelta{
			CaseID:          caseID,
			BaselineScore:   baselineCase.Score,
			CandidateScore:  candidateCase.Score,
			ScoreDelta:      roundScore(candidateCase.Score - baselineCase.Score),
			BaselinePassed:  baselineCase.Passed,
			CandidatePassed: candidateCase.Passed,
		}
		switch {
		case !baselineCase.Passed && candidateCase.Passed:
			item.Class = caseNewlyPassed
			delta.NewlyPassed++
		case baselineCase.Passed && !candidateCase.Passed:
			item.Class = caseNewlyFailed
			delta.NewlyFailed++
		case item.ScoreDelta > scoreEpsilon:
			item.Class = caseImproved
			delta.Improved++
		case item.ScoreDelta < -scoreEpsilon:
			item.Class = caseRegressed
			delta.Regressed++
		default:
			item.Class = caseUnchanged
			delta.Unchanged++
		}
		delta.Cases = append(delta.Cases, item)
	}
	return delta, nil
}

func indexCases(cases []caseEvaluation) (map[string]caseEvaluation, error) {
	index := make(map[string]caseEvaluation, len(cases))
	for _, evalCase := range cases {
		if evalCase.CaseID == "" {
			return nil, errors.New("case id is empty")
		}
		if _, exists := index[evalCase.CaseID]; exists {
			return nil, fmt.Errorf("duplicate case id %q", evalCase.CaseID)
		}
		index[evalCase.CaseID] = evalCase
	}
	return index, nil
}

func roundScore(value float64) float64 {
	return math.Round(value*1e9) / 1e9
}
