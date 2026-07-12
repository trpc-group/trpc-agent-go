// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package regressionloop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestGenerateReport(t *testing.T) {
	ctx := &PipelineContext{
		Config: &Config{
			Mode: "fake",
			Seed: 12345,
		},
		BaselineTrain:  &engine.EvaluationResult{OverallScore: 0.6},
		BaselineVal:    &engine.EvaluationResult{OverallScore: 0.65},
		CandidateTrain: &engine.EvaluationResult{OverallScore: 0.75},
		CandidateVal:   &engine.EvaluationResult{OverallScore: 0.8},
		GateDecision: &GateDecision{
			Result:            GateResultAccept,
			Stage:             "security_gate",
			ScoreDelta:        0.15,
			BaselineScore:     0.65,
			CandidateScore:    0.8,
			AcceptanceReasons: []string{"score gain meets threshold"},
		},
		CaseDeltas: []CaseDelta{
			{EvalCaseID: "case1", DeltaType: DeltaNewlyPassed, BaselineScore: 0.0, CandidateScore: 1.0},
			{EvalCaseID: "case2", DeltaType: DeltaScoreUp, BaselineScore: 0.5, CandidateScore: 0.8},
		},
		Attributions: []AttributionResult{
			{Category: AttributionResponseMismatch},
			{Category: AttributionToolCallError},
		},
		Candidates: []CandidateInfo{
			{Round: 1, ValidationScore: 0.7, Accepted: false},
			{Round: 2, ValidationScore: 0.8, Accepted: true},
		},
		TotalCost:      10.50,
		TotalCalls:     100,
		TotalLatencyMS: 5000,
	}

	report := GenerateReport(ctx)
	assert.Equal(t, "fake", report.RunMeta.Mode)
	assert.Equal(t, int64(12345), report.RunMeta.Seed)
	assert.Equal(t, 0.6, report.BaselineTrainScore)
	assert.Equal(t, 0.65, report.BaselineValScore)
	assert.Equal(t, 0.75, report.CandidateTrainScore)
	assert.Equal(t, 0.8, report.CandidateValScore)
	assert.InDelta(t, 0.15, report.ScoreDeltaTrain, 0.0001)
	assert.InDelta(t, 0.15, report.ScoreDeltaVal, 0.0001)
	assert.Equal(t, GateResultAccept, report.GateDecision.Result)
	assert.Len(t, report.CaseDeltas, 2)
	assert.Len(t, report.AttributionSummary, 2)
	assert.Len(t, report.Candidates, 2)
	assert.Equal(t, 10.50, report.TotalCost)
	assert.Equal(t, 100, report.TotalCalls)
	assert.Equal(t, int64(5000), report.TotalLatencyMS)
}

func TestWriteReports(t *testing.T) {
	tmpDir := t.TempDir()

	ctx := &PipelineContext{
		Config: &Config{
			Mode: "fake",
			Seed: 12345,
		},
		BaselineVal:  &engine.EvaluationResult{OverallScore: 0.6},
		CandidateVal: &engine.EvaluationResult{OverallScore: 0.8},
		GateDecision: &GateDecision{
			Result:         GateResultAccept,
			Stage:          "security_gate",
			ScoreDelta:     0.2,
			BaselineScore:  0.6,
			CandidateScore: 0.8,
		},
	}

	report := GenerateReport(ctx)
	err := WriteReports(report, tmpDir)
	assert.NoError(t, err)

	jsonPath := filepath.Join(tmpDir, "optimization_report.json")
	mdPath := filepath.Join(tmpDir, "optimization_report.md")

	assert.FileExists(t, jsonPath)
	assert.FileExists(t, mdPath)

	jsonContent, err := os.ReadFile(jsonPath)
	assert.NoError(t, err)
	assert.Contains(t, string(jsonContent), "gateDecision")

	mdContent, err := os.ReadFile(mdPath)
	assert.NoError(t, err)
	assert.Contains(t, string(mdContent), "# Optimization Report")
	assert.Contains(t, string(mdContent), "Candidate Accepted")
}

func TestSaveAuditTrail(t *testing.T) {
	tmpDir := t.TempDir()

	ctx := &PipelineContext{
		Config: &Config{
			Mode: "fake",
			Seed: 12345,
		},
		GateDecision: &GateDecision{
			Result: GateResultAccept,
		},
		Attributions: []AttributionResult{
			{Category: AttributionResponseMismatch},
		},
	}

	err := SaveAuditTrail(ctx, tmpDir)
	assert.NoError(t, err)

	auditDir := filepath.Join(tmpDir, "audit")
	assert.DirExists(t, auditDir)

	runMetaPath := filepath.Join(auditDir, "run_meta.json")
	gateDecisionPath := filepath.Join(auditDir, "gate_decision.json")
	attributionsPath := filepath.Join(auditDir, "attributions.json")

	assert.FileExists(t, runMetaPath)
	assert.FileExists(t, gateDecisionPath)
	assert.FileExists(t, attributionsPath)
}

func TestWriteReportsEmptyDirectory(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "test_report_empty")
	os.RemoveAll(tmpDir)

	ctx := &PipelineContext{
		Config: &Config{
			Mode: "fake",
			Seed: 12345,
		},
		BaselineVal:  &engine.EvaluationResult{OverallScore: 0.6},
		CandidateVal: &engine.EvaluationResult{OverallScore: 0.8},
		GateDecision: &GateDecision{
			Result:         GateResultAccept,
			Stage:          "security_gate",
			ScoreDelta:     0.2,
			BaselineScore:  0.6,
			CandidateScore: 0.8,
		},
	}

	report := GenerateReport(ctx)
	err := WriteReports(report, tmpDir)
	assert.NoError(t, err)
	assert.DirExists(t, tmpDir)
}
