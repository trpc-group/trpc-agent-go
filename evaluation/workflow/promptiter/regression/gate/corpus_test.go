//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gate

import (
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

type releaseCorpusEntry struct {
	ID                          string              `json:"id"`
	Expected                    regression.Decision `json:"expected"`
	ValidationGain              float64             `json:"validationGain"`
	MinValidationGain           float64             `json:"minValidationGain"`
	TrainAvailable              bool                `json:"trainAvailable"`
	TrainGain                   float64             `json:"trainGain"`
	MaxGeneralizationGap        float64             `json:"maxGeneralizationGap"`
	NewHardFailures             int                 `json:"newHardFailures"`
	CriticalRegressions         int                 `json:"criticalRegressions"`
	WorstRegression             float64             `json:"worstRegression"`
	MaxCaseRegression           float64             `json:"maxCaseRegression"`
	CostKnown                   bool                `json:"costKnown"`
	Cost                        float64             `json:"cost"`
	MaxCost                     float64             `json:"maxCost"`
	PromptIterAccepted          bool                `json:"promptIterAccepted"`
	RequirePromptIterAcceptance bool                `json:"requirePromptIterAcceptance"`
	ValidationIncomplete        bool                `json:"validationIncomplete"`
}

func TestFrozenReleaseDecisionCorpus(t *testing.T) {
	data, err := os.ReadFile("testdata/release_corpus.json")
	require.NoError(t, err)
	var corpus []releaseCorpusEntry
	require.NoError(t, json.Unmarshal(data, &corpus))
	require.NotEmpty(t, corpus)

	matrix := make(map[regression.Decision]map[regression.Decision]int)
	totals := make(map[regression.Decision]int)
	correct := make(map[regression.Decision]int)
	for _, entry := range corpus {
		decision, decideErr := NewPolicy().Decide(corpusGateInput(entry))
		require.NoError(t, decideErr, entry.ID)
		if matrix[entry.Expected] == nil {
			matrix[entry.Expected] = make(map[regression.Decision]int)
		}
		matrix[entry.Expected][decision.Decision]++
		totals[entry.Expected]++
		if decision.Decision == entry.Expected {
			correct[entry.Expected]++
		}
	}

	labels := make([]string, 0, len(totals))
	for expected := range totals {
		labels = append(labels, string(expected))
	}
	sort.Strings(labels)
	allCorrect := 0
	for _, label := range labels {
		expected := regression.Decision(label)
		allCorrect += correct[expected]
		t.Logf("release decision=%s correct=%d total=%d matrix=%v",
			expected, correct[expected], totals[expected], matrix[expected])
		require.GreaterOrEqual(t, float64(correct[expected])/float64(totals[expected]), .75,
			"decision %s is below minimum accuracy", expected)
	}
	require.GreaterOrEqual(t, float64(allCorrect)/float64(len(corpus)), .80)
}

func corpusGateInput(entry releaseCorpusEntry) *regression.GateInput {
	validationDelta := &regression.DeltaReport{
		Complete:            !entry.ValidationIncomplete,
		CandidateScore:      entry.ValidationGain,
		WeightedScoreDelta:  entry.ValidationGain,
		NewHardFailures:     entry.NewHardFailures,
		CriticalRegressions: entry.CriticalRegressions,
	}
	if entry.WorstRegression > 0 {
		validationDelta.Cases = []regression.CaseDelta{{
			CaseID: "boundary-case",
			Metrics: []regression.MetricDelta{{
				MetricName:     "business_quality",
				BaselineScore:  entry.WorstRegression,
				CandidateScore: 0,
			}},
		}}
	}
	var trainDelta *regression.DeltaReport
	if entry.TrainAvailable {
		trainDelta = &regression.DeltaReport{
			Complete: true, CandidateScore: entry.TrainGain, WeightedScoreDelta: entry.TrainGain,
		}
	}
	return &regression.GateInput{
		Spec: &regression.RunSpec{
			Gate: regression.GatePolicy{
				MinValidationGain:           entry.MinValidationGain,
				MaxCaseRegression:           entry.MaxCaseRegression,
				MaxGeneralizationGap:        entry.MaxGeneralizationGap,
				RequirePromptIterAcceptance: entry.RequirePromptIterAcceptance,
			},
			Budget: regression.BudgetPolicy{MaxEstimatedCost: entry.MaxCost},
		},
		PromptIterAccepted:      entry.PromptIterAccepted,
		PromptIterReason:        "frozen PromptIter outcome",
		CandidateProfileValid:   true,
		CandidateProfileChanged: true,
		CandidateValidation:     &regression.EvaluationSnapshot{Complete: !entry.ValidationIncomplete},
		TrainDelta:              trainDelta,
		ValidationDelta:         validationDelta,
		TotalUsage: regression.UsageSummary{
			Complete:     true,
			CostEstimate: regression.CostEstimate{CostKnown: entry.CostKnown, EstimatedCost: entry.Cost},
		},
	}
}
