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
	"strings"
)

// CompareCases builds a deterministic paired comparison. Both sides must contain the same case IDs,
// critical flags, and at least passK runs per case. Pass^k succeeds only when the first k runs all pass.
func CompareCases(baseline, candidate []CaseEvaluation, passK int) (Comparison, error) {
	if passK <= 0 {
		return Comparison{}, errors.New("passK must be positive")
	}
	baselineByID, err := indexCases("baseline", baseline, passK)
	if err != nil {
		return Comparison{}, err
	}
	candidateByID, err := indexCases("candidate", candidate, passK)
	if err != nil {
		return Comparison{}, err
	}
	if len(baselineByID) != len(candidateByID) {
		return Comparison{}, fmt.Errorf("case set size differs: baseline=%d candidate=%d", len(baselineByID), len(candidateByID))
	}

	ids := make([]string, 0, len(baselineByID))
	for id := range baselineByID {
		if _, ok := candidateByID[id]; !ok {
			return Comparison{}, fmt.Errorf("candidate is missing case %q", id)
		}
		ids = append(ids, id)
	}
	for id := range candidateByID {
		if _, ok := baselineByID[id]; !ok {
			return Comparison{}, fmt.Errorf("baseline is missing case %q", id)
		}
	}
	sort.Strings(ids)

	comparison := Comparison{PassK: passK, Deltas: make([]CaseDelta, 0, len(ids))}
	var baselineStable, candidateStable int
	for _, id := range ids {
		baseCase, candidateCase := baselineByID[id], candidateByID[id]
		if baseCase.Critical != candidateCase.Critical {
			return Comparison{}, fmt.Errorf("case %q has inconsistent critical flag", id)
		}
		baseMean, candidateMean := meanScore(baseCase.Runs), meanScore(candidateCase.Runs)
		basePowerK := passesPowerK(baseCase.Runs, passK)
		candidatePowerK := passesPowerK(candidateCase.Runs, passK)
		if basePowerK {
			baselineStable++
		}
		if candidatePowerK {
			candidateStable++
		}
		delta := CaseDelta{
			ID: id, Critical: baseCase.Critical,
			BaselineMeanScore: baseMean, CandidateMeanScore: candidateMean,
			ScoreDelta:       candidateMean - baseMean,
			BaselinePassRate: passRate(baseCase.Runs), CandidatePassRate: passRate(candidateCase.Runs),
			BaselinePassPowerK: basePowerK, CandidatePassPowerK: candidatePowerK,
			NewHardFailure: !hasHardFailure(baseCase.Runs) && hasHardFailure(candidateCase.Runs),
		}
		delta.CriticalRegression = delta.Critical && (delta.ScoreDelta < 0 || (basePowerK && !candidatePowerK))
		comparison.Deltas = append(comparison.Deltas, delta)
		comparison.BaselineMeanScore += baseMean
		comparison.CandidateMeanScore += candidateMean
		comparison.Usage = comparison.Usage.Add(sumUsage(baseCase.Runs)).Add(sumUsage(candidateCase.Runs))
	}
	count := float64(len(ids))
	comparison.BaselineMeanScore /= count
	comparison.CandidateMeanScore /= count
	comparison.MeanScoreGain = comparison.CandidateMeanScore - comparison.BaselineMeanScore
	comparison.BaselinePassPowerKRate = float64(baselineStable) / count
	comparison.CandidatePassPowerKRate = float64(candidateStable) / count
	return comparison, nil
}

func indexCases(side string, cases []CaseEvaluation, passK int) (map[string]CaseEvaluation, error) {
	if len(cases) == 0 {
		return nil, fmt.Errorf("%s cases are empty", side)
	}
	result := make(map[string]CaseEvaluation, len(cases))
	for _, evalCase := range cases {
		evalCase.ID = strings.TrimSpace(evalCase.ID)
		if evalCase.ID == "" {
			return nil, fmt.Errorf("%s contains an empty case ID", side)
		}
		if _, ok := result[evalCase.ID]; ok {
			return nil, fmt.Errorf("%s contains duplicate case ID %q", side, evalCase.ID)
		}
		if len(evalCase.Runs) < passK {
			return nil, fmt.Errorf("%s case %q has %d runs, need at least %d", side, evalCase.ID, len(evalCase.Runs), passK)
		}
		for runIndex, run := range evalCase.Runs {
			if math.IsNaN(run.Score) || math.IsInf(run.Score, 0) {
				return nil, fmt.Errorf("%s case %q run %d has a non-finite score", side, evalCase.ID, runIndex)
			}
			if run.Usage.Calls < 0 || run.Usage.InputTokens < 0 || run.Usage.OutputTokens < 0 ||
				run.Usage.CostCNY < 0 || math.IsNaN(run.Usage.CostCNY) || math.IsInf(run.Usage.CostCNY, 0) {
				return nil, fmt.Errorf("%s case %q run %d has invalid usage", side, evalCase.ID, runIndex)
			}
		}
		result[evalCase.ID] = evalCase
	}
	return result, nil
}

func meanScore(runs []CaseRun) float64 {
	var total float64
	for _, run := range runs {
		total += run.Score
	}
	return total / float64(len(runs))
}

func passRate(runs []CaseRun) float64 {
	var passed int
	for _, run := range runs {
		if run.Passed && !run.HardFailure {
			passed++
		}
	}
	return float64(passed) / float64(len(runs))
}

func passesPowerK(runs []CaseRun, k int) bool {
	for i := 0; i < k; i++ {
		if !runs[i].Passed || runs[i].HardFailure {
			return false
		}
	}
	return true
}

func hasHardFailure(runs []CaseRun) bool {
	for _, run := range runs {
		if run.HardFailure {
			return true
		}
	}
	return false
}

func sumUsage(runs []CaseRun) Usage {
	var result Usage
	for _, run := range runs {
		result = result.Add(run.Usage)
	}
	return result
}
