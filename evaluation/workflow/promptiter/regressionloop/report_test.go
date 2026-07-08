//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteReports(t *testing.T) {
	dir := t.TempDir()
	report := sampleReport()
	jsonPath := filepath.Join(dir, "optimization_report.json")
	mdPath := filepath.Join(dir, "optimization_report.md")

	require.NoError(t, WriteReports(report, jsonPath, mdPath))
	data, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	var decoded Report
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "test-app", decoded.Run.AppName)
	md, err := os.ReadFile(mdPath)
	require.NoError(t, err)
	assert.Contains(t, string(md), "Decision: **ACCEPT**")
	assert.Contains(t, string(md), "Validation Delta")
	assert.Contains(t, string(md), "candidate satisfies")
}

func sampleReport() *Report {
	base := evalSummary(0.6, caseResult("case", 0.6, true))
	candidate := evalSummary(0.8, caseResult("case", 0.8, true))
	deltas, summary := ComputeDeltas(base, candidate, nil)
	gate := EvaluateGate(GatePolicy{MinValidationScoreGain: 0.1, AllowNewHardFails: false, BlockCriticalRegression: true}, base, candidate, deltas, CostSummary{}, LatencySummary{})
	round := CandidateRound{Round: 1, Prompt: "better", Train: candidate, Validation: candidate, Delta: deltas, GateDecision: gate}
	return &Report{
		Run: RunMetadata{
			AppName: "test-app",
			Seed:    42,
			Runner:  RunnerConfig{Mode: RunnerModeFake},
		},
		Baseline:                EvaluationPair{Train: base, Validation: base},
		Candidates:              []CandidateRound{round},
		SelectedCandidate:       &round,
		Delta:                   DeltaReport{Summary: summary, Cases: deltas},
		GateDecision:            gate,
		FailureAttributionStats: map[string]int{"final_response_mismatch": 1},
		CostSummary:             CostSummary{Calls: 1},
		LatencySummary:          LatencySummary{TotalMS: 1},
	}
}
