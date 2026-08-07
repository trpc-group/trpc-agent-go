package main

import (
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/attribution"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/gates"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/pipeline"
)

func TestAttribution(t *testing.T) {
	tests := []struct {
		name         string
		caseID       string
		expectedResp string
		actualResp   string
		errStr       string
		wantCategory attribution.Category
	}{
		{
			name:         "Passed Case",
			caseID:       "c1",
			expectedResp: "Paris",
			actualResp:   "Paris",
			errStr:       "",
			wantCategory: attribution.None,
		},
		{
			name:         "Route Error",
			caseID:       "c2",
			expectedResp: "50",
			actualResp:   "Error",
			errStr:       "Router dispatched request to translation agent",
			wantCategory: attribution.RouteError,
		},
		{
			name:         "Tool Argument Error",
			caseID:       "c3",
			expectedResp: "100",
			actualResp:   "Error",
			errStr:       "passed string 'one hundred' to argument int",
			wantCategory: attribution.ToolArgumentError,
		},
		{
			name:         "Tool Execution Error",
			caseID:       "c4",
			expectedResp: "Success",
			actualResp:   "Failed",
			errStr:       "tool execution failed with status 500",
			wantCategory: attribution.ToolCallError,
		},
		{
			name:         "Knowledge Recall Hedging",
			caseID:       "c5",
			expectedResp: "299,792,458",
			actualResp:   "I am not sure, but it is around 300k",
			errStr:       "",
			wantCategory: attribution.KnowledgeRecallInsufficient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attr := attribution.AttributeFailure(tt.caseID, tt.expectedResp, tt.actualResp, tt.errStr)
			if attr.Category != tt.wantCategory {
				t.Errorf("AttributeFailure() category = %v, want %v", attr.Category, tt.wantCategory)
			}
		})
	}
}

func TestGateEvaluation(t *testing.T) {
	cfg := gates.Config{
		MinValScoreGain:  0.10,
		AllowNewHardFail: false,
		KeyCaseIDs:       []string{"key_01"},
		MaxCostBudgetUSD: 0.50,
	}

	t.Run("Score gain satisfied", func(t *testing.T) {
		deltas := []gates.CaseDelta{
			{CaseID: "c1", BaselineScore: 0.5, CandidateScore: 0.8, ScoreDelta: 0.3, BaselinePass: false, CandidatePass: true},
		}
		decision := gates.EvaluateCandidate(cfg, 0.5, 0.8, deltas, 0.1)
		if !decision.Accepted {
			t.Errorf("expected accepted candidate, got rejected: %s", decision.Reason)
		}
	})

	t.Run("Hard fail rejected", func(t *testing.T) {
		deltas := []gates.CaseDelta{
			{CaseID: "c1", BaselineScore: 1.0, CandidateScore: 0.0, ScoreDelta: -1.0, BaselinePass: true, CandidatePass: false},
		}
		decision := gates.EvaluateCandidate(cfg, 0.5, 0.8, deltas, 0.1)
		if decision.Accepted {
			t.Errorf("expected rejected candidate due to hard fail, got accepted")
		}
		if decision.GateName != "HardFailGuard" {
			t.Errorf("expected gate HardFailGuard, got %s", decision.GateName)
		}
	})

	t.Run("Key case degradation rejected", func(t *testing.T) {
		deltas := []gates.CaseDelta{
			{CaseID: "key_01", BaselineScore: 0.9, CandidateScore: 0.7, ScoreDelta: -0.2, BaselinePass: true, CandidatePass: true},
		}
		decision := gates.EvaluateCandidate(cfg, 0.5, 0.8, deltas, 0.1)
		if decision.Accepted {
			t.Errorf("expected rejected candidate due to key case degradation, got accepted")
		}
		if decision.GateName != "KeyCaseGuard" {
			t.Errorf("expected gate KeyCaseGuard, got %s", decision.GateName)
		}
	})

	t.Run("Cost budget exceeded", func(t *testing.T) {
		deltas := []gates.CaseDelta{
			{CaseID: "c1", BaselineScore: 0.5, CandidateScore: 0.8, ScoreDelta: 0.3, BaselinePass: false, CandidatePass: true},
		}
		decision := gates.EvaluateCandidate(cfg, 0.5, 0.8, deltas, 0.9)
		if decision.Accepted {
			t.Errorf("expected rejected candidate due to cost budget, got accepted")
		}
		if decision.GateName != "CostBudgetGuard" {
			t.Errorf("expected gate CostBudgetGuard, got %s", decision.GateName)
		}
	})
}

func TestEndToEndPipeline(t *testing.T) {
	tempDir := t.TempDir()

	cfg := pipeline.Config{
		TrainSetPath: "data/train_evalset.json",
		ValSetPath:   "data/val_evalset.json",
		Mode:         "fake_deterministic",
		OutputDir:    tempDir,
		GateConfig: gates.Config{
			MinValScoreGain:  0.10,
			AllowNewHardFail: false,
			KeyCaseIDs:       []string{"val_opt_03"},
			MaxCostBudgetUSD: 0.50,
		},
	}

	p := pipeline.New(cfg)
	report, err := p.Run()
	if err != nil {
		t.Fatalf("Pipeline.Run() failed: %v", err)
	}

	if report.BaselineValScore <= 0 {
		t.Errorf("expected positive baseline val score, got %f", report.BaselineValScore)
	}

	if !report.OverallAccepted {
		t.Errorf("expected overall accepted to be true")
	}

	// Verify report files created
	jsonPath := filepath.Join(tempDir, "optimization_report.json")
	if _, err := os.Stat(jsonPath); os.IsNotExist(err) {
		t.Errorf("optimization_report.json was not created")
	}

	mdPath := filepath.Join(tempDir, "optimization_report.md")
	if _, err := os.Stat(mdPath); os.IsNotExist(err) {
		t.Errorf("optimization_report.md was not created")
	}
}
