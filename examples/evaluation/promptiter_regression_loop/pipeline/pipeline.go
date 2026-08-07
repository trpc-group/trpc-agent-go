//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package pipeline orchestrates the Evaluation + Optimization closed-loop workflow.
package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/attribution"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/gates"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/reporter"
)

// TestCase defines an evaluation case in the evalset.
type TestCase struct {
	CaseID            string  `json:"case_id"`
	Name              string  `json:"name"`
	Prompt            string  `json:"prompt"`
	TargetSurface     string  `json:"target_surface"`
	ExpectedResponse  string  `json:"expected_response"`
	Category          string  `json:"category"`
	BaselineResponse  string  `json:"baseline_response"`
	BaselineScore     float64 `json:"baseline_score"`
	BaselinePass      bool    `json:"baseline_pass"`
	CandidateResponse string  `json:"candidate_response"`
	CandidateScore    float64 `json:"candidate_score"`
	CandidatePass     bool    `json:"candidate_pass"`
}

// Config represents pipeline configuration inputs.
type Config struct {
	TrainSetPath string `json:"train_set_path"`
	ValSetPath   string `json:"val_set_path"`
	Mode         string `json:"mode"`
	OutputDir    string `json:"output_dir"`
	GateConfig   gates.Config
}

// Pipeline orchestrates baseline eval, failure attribution, candidate iteration, and report generation.
type Pipeline struct {
	cfg Config
}

// New creates a new Pipeline instance.
func New(cfg Config) *Pipeline {
	return &Pipeline{cfg: cfg}
}

// Run executes the closed-loop pipeline.
func (p *Pipeline) Run() (*reporter.AuditReport, error) {
	trainCases, err := loadCases(p.cfg.TrainSetPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load train evalset: %w", err)
	}

	valCases, err := loadCases(p.cfg.ValSetPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load val evalset: %w", err)
	}

	// 1. Calculate Baseline Scores
	baselineTrainScore := calculateScore(trainCases, true)
	baselineValScore := calculateScore(valCases, true)

	// 2. Perform Baseline Failure Attribution
	var baselineAttributions []attribution.FailureDetail
	for _, tc := range trainCases {
		if !tc.BaselinePass {
			errStr := ""
			if tc.Category != "none" {
				errStr = fmt.Sprintf("Error category: %s", tc.Category)
			}
			attr := attribution.AttributeFailure(tc.CaseID, tc.ExpectedResponse, tc.BaselineResponse, errStr)
			baselineAttributions = append(baselineAttributions, attr)
		}
	}

	// 3. Iterative PromptIter Rounds (Simulated deterministic or trace mode)
	bestValScore := baselineValScore
	overallAccepted := false
	var roundSummaries []reporter.RoundSummary

	// Round 1: System Prompt Optimization (Optimizable Success)
	r1Deltas := []gates.CaseDelta{
		{CaseID: "val_opt_01", BaselineScore: 0.6, CandidateScore: 1.0, ScoreDelta: 0.4, BaselinePass: false, CandidatePass: true, Transition: "improved"},
		{CaseID: "val_opt_02", BaselineScore: 0.95, CandidateScore: 0.95, ScoreDelta: 0.0, BaselinePass: true, CandidatePass: true, Transition: "unchanged"},
		{CaseID: "val_opt_03", BaselineScore: 1.0, CandidateScore: 1.0, ScoreDelta: 0.0, BaselinePass: true, CandidatePass: true, Transition: "unchanged"},
	}
	r1ValScore := (1.0 + 0.95 + 1.0) / 3.0 // 0.9833
	r1Gate := gates.EvaluateCandidate(p.cfg.GateConfig, baselineValScore, r1ValScore, r1Deltas, 0.02)

	roundSummaries = append(roundSummaries, reporter.RoundSummary{
		RoundIndex:    1,
		TargetSurface: "system_prompt",
		ProposedPatch: "Added explicit constraint to output direct factual answers without hedging.",
		TrainScore:    1.0,
		ValScore:      r1ValScore,
		GateDecision:  r1Gate,
		Attributions:  baselineAttributions,
	})

	if r1Gate.Accepted {
		bestValScore = r1ValScore
		overallAccepted = true
	}

	// Round 2: Tool Description Optimization (Ineffective Gain)
	// Compare candidate round 2 against previous accepted baseline (bestValScore = 0.9833)
	r2Deltas := []gates.CaseDelta{
		{CaseID: "val_opt_01", BaselineScore: 1.0, CandidateScore: 0.6, ScoreDelta: -0.4, BaselinePass: true, CandidatePass: false, Transition: "degraded"},
		{CaseID: "val_opt_02", BaselineScore: 0.95, CandidateScore: 0.95, ScoreDelta: 0.0, BaselinePass: true, CandidatePass: true, Transition: "unchanged"},
		{CaseID: "val_opt_03", BaselineScore: 1.0, CandidateScore: 1.0, ScoreDelta: 0.0, BaselinePass: true, CandidatePass: true, Transition: "unchanged"},
	}
	r2ValScore := (0.6 + 0.95 + 1.0) / 3.0 // 0.85
	r2Gate := gates.EvaluateCandidate(p.cfg.GateConfig, bestValScore, r2ValScore, r2Deltas, 0.04)

	roundSummaries = append(roundSummaries, reporter.RoundSummary{
		RoundIndex:    2,
		TargetSurface: "tool_desc_calc",
		ProposedPatch: "Updated tool description to specify numeric parameter types explicitly.",
		TrainScore:    1.0,
		ValScore:      r2ValScore,
		GateDecision:  r2Gate,
		Attributions:  []attribution.FailureDetail{},
	})

	// Round 3: Router Prompt Optimization (Overfitting Trap - Val Degrades)
	// Compare candidate round 3 against previous accepted baseline (bestValScore = 0.9833)
	r3Deltas := []gates.CaseDelta{
		{CaseID: "val_opt_01", BaselineScore: 1.0, CandidateScore: 1.0, ScoreDelta: 0.0, BaselinePass: true, CandidatePass: true, Transition: "unchanged"},
		{CaseID: "val_opt_02", BaselineScore: 0.95, CandidateScore: 0.0, ScoreDelta: -0.95, BaselinePass: true, CandidatePass: false, Transition: "new_hard_fail"},
		{CaseID: "val_opt_03", BaselineScore: 1.0, CandidateScore: 1.0, ScoreDelta: 0.0, BaselinePass: true, CandidatePass: true, Transition: "unchanged"},
	}
	r3ValScore := (1.0 + 0.0 + 1.0) / 3.0 // 0.6667
	r3Gate := gates.EvaluateCandidate(p.cfg.GateConfig, bestValScore, r3ValScore, r3Deltas, 0.06)

	r3Attributions := []attribution.FailureDetail{
		attribution.AttributeFailure("val_opt_02", "Hola [Logged]", "Error: Router sent translation request to calculator agent", "route_error"),
	}

	roundSummaries = append(roundSummaries, reporter.RoundSummary{
		RoundIndex:    3,
		TargetSurface: "router_prompt",
		ProposedPatch: "Overly aggressive routing rule favoring calculator agent.",
		TrainScore:    1.0,
		ValScore:      r3ValScore,
		GateDecision:  r3Gate,
		Attributions:  r3Attributions,
	})

	// 4. Final Recommendation
	recommendation := "Candidate Round 1 (system_prompt patch) passed all acceptance gates with validation gain +0.1333 and zero hard fails. Promoted to production."
	if !overallAccepted {
		recommendation = "No candidate prompt satisfied all acceptance gates. Retaining baseline prompt."
	}

	report := reporter.AuditReport{
		Timestamp:           time.Now().UTC().Format(time.RFC3339),
		Mode:                p.cfg.Mode,
		BaselineTrainScore:  baselineTrainScore,
		BaselineValScore:    baselineValScore,
		BestValScore:        bestValScore,
		OverallAccepted:     overallAccepted,
		TotalCostUSD:        0.06,
		TotalDurationSec:    0.15,
		Rounds:              roundSummaries,
		FinalRecommendation: recommendation,
	}

	// 5. Generate Audit Artifacts
	if err := reporter.GenerateReports(p.cfg.OutputDir, report); err != nil {
		return nil, fmt.Errorf("failed to write audit reports: %w", err)
	}

	return &report, nil
}

func loadCases(path string) ([]TestCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []TestCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, err
	}
	return cases, nil
}

func calculateScore(cases []TestCase, isBaseline bool) float64 {
	if len(cases) == 0 {
		return 0.0
	}
	total := 0.0
	for _, tc := range cases {
		if isBaseline {
			total += tc.BaselineScore
		} else {
			total += tc.CandidateScore
		}
	}
	return total / float64(len(cases))
}
