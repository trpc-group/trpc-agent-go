//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the regression loop framework's failure attribution,
// enhanced acceptance gates, and audit reporting capabilities.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/regressionloop"
)

var (
	outputDir = flag.String("output-dir", "./output", "Directory for reports")
)

func main() {
	flag.Parse()

	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output dir: %v", err)
	}

	log.Println("=== Failure Attribution Demo ===")
	demoAttribution()

	log.Println("\n=== Acceptance Gate Demo ===")
	demoGate()

	log.Println("\n=== Generating Audit Report ===")
	report := generateDemoReport()

	jsonPath := filepath.Join(*outputDir, "optimization_report.json")
	mdPath := filepath.Join(*outputDir, "optimization_report.md")

	if err := regressionloop.WriteJSONReport(jsonPath, report); err != nil {
		log.Fatalf("Failed to write JSON report: %v", err)
	}
	if err := regressionloop.WriteMarkdownReport(mdPath, report); err != nil {
		log.Fatalf("Failed to write Markdown report: %v", err)
	}

	log.Printf("Reports written to:")
	log.Printf("  JSON:     %s", jsonPath)
	log.Printf("  Markdown: %s", mdPath)

	if report.GateDecision != nil {
		if report.GateDecision.Accepted {
			log.Printf("ACCEPTED: %s", report.GateDecision.Summary)
		} else {
			log.Printf("REJECTED: %s", report.GateDecision.Summary)
		}
	}
}

func demoAttribution() {
	results := []regressionloop.CaseEvalResult{
		{
			EvalSetID: "train", EvalCaseID: "case-1",
			Metrics: []regressionloop.MetricInfo{
				{MetricName: "tool_trajectory_avg_score", Score: 0.3, Status: string(status.EvalStatusFailed), Reason: "agent called wrong tool"},
			},
		},
		{
			EvalSetID: "train", EvalCaseID: "case-2",
			Metrics: []regressionloop.MetricInfo{
				{MetricName: "final_response_avg_score", Score: 0.2, Status: string(status.EvalStatusFailed), Reason: "response contains hallucinated information"},
			},
		},
		{
			EvalSetID: "train", EvalCaseID: "case-3",
			Metrics: []regressionloop.MetricInfo{
				{MetricName: "custom_metric", Score: 0.4, Status: string(status.EvalStatusFailed), Reason: "missing argument in tool call"},
			},
		},
	}

	attributions := regressionloop.AttributeFailures(results)
	summary := regressionloop.SummarizeAttributions(attributions)

	fmt.Printf("Total failures: %d\n", len(attributions))
	for _, s := range summary {
		fmt.Printf("  %s: %d case(s)\n", s.Category, s.Count)
	}
}

func demoGate() {
	trainBaseline := regressionloop.EvalRunSummary{OverallScore: 0.60, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}
	trainCandidate := regressionloop.EvalRunSummary{OverallScore: 0.85, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}
	valBaseline := regressionloop.EvalRunSummary{OverallScore: 0.70, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}
	valCandidate := regressionloop.EvalRunSummary{OverallScore: 0.65, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}

	cfg := regressionloop.GateConfig{MinScoreGain: 0.02, NoNewHardFailures: true, OverfitThreshold: 0.05}
	decision := regressionloop.EvaluateGate(cfg, valBaseline, valCandidate, &trainBaseline, &trainCandidate)

	fmt.Printf("Decision: %s\n", decision.Summary)
	fmt.Printf("Overfitting: %v\n", decision.OverfittingDetected)
}

func generateDemoReport() *regressionloop.OptimizationReport {
	cfg := regressionloop.PipelineConfig{
		GateConfig: regressionloop.GateConfig{
			MinScoreGain: 0.02, NoNewHardFailures: true, OverfitThreshold: 0.05, MaxCostBudget: 10.0, CriticalCaseIDs: []string{"val-stable"},
		},
		MaxRounds: 2, TrainEvalSets: []string{"train"}, ValEvalSets: []string{"validation"}, ModelConfig: "demo",
	}

	baselineRun := regressionloop.EvalRunSummary{
		OverallScore: 0.65,
		CaseScores:   map[string]float64{"train/c1": 0.50, "train/c2": 0.90, "train/c3": 0.55},
		CaseStatuses: map[string]string{"train/c1": "failed", "train/c2": "passed", "train/c3": "failed"},
		CaseCount:    3, PassCount: 1, FailCount: 2,
	}
	candidateRun := regressionloop.EvalRunSummary{
		OverallScore: 0.82,
		CaseScores:   map[string]float64{"train/c1": 0.85, "train/c2": 0.88, "train/c3": 0.73},
		CaseStatuses: map[string]string{"train/c1": "passed", "train/c2": "passed", "train/c3": "passed"},
		CaseCount:    3, PassCount: 3, FailCount: 0,
	}

	trainBaseline := &regressionloop.EvalRunSummary{OverallScore: 0.60, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}
	trainCandidate := &regressionloop.EvalRunSummary{OverallScore: 0.85, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}

	attributions := []regressionloop.FailureAttribution{
		{EvalSetID: "train", EvalCaseID: "c1", MetricName: "tool_trajectory_avg_score", Category: regressionloop.FailureToolCallError, Reason: "wrong tool", Score: 0.3, Explanation: "metric name match"},
		{EvalSetID: "train", EvalCaseID: "c3", MetricName: "custom_metric", Category: regressionloop.FailureToolArgumentError, Reason: "missing argument", Score: 0.4, Explanation: "keyword match"},
	}

	rounds := []regressionloop.RoundAudit{
		{
			Round: 1, TrainScore: 0.75, ValidationScore: 0.78, Accepted: true,
			AcceptanceReason:    "score delta 0.13 >= threshold 0.02",
			FailureAttributions: regressionloop.SummarizeAttributions(attributions),
			PatchesApplied:      []regressionloop.PatchAudit{{SurfaceID: "agent/instruction", NewValue: "Improved tool selection...", Reason: "Fix tool call errors"}},
		},
		{
			Round: 2, TrainScore: 0.82, ValidationScore: 0.82, Accepted: true,
			AcceptanceReason: "score delta 0.04 >= threshold 0.02",
			PatchesApplied:   []regressionloop.PatchAudit{{SurfaceID: "agent/instruction", NewValue: "Fixed argument formatting...", Reason: "Fix argument errors"}},
		},
	}

	costSummary := regressionloop.CostSummary{TotalTokens: 15000, EstimatedCost: 0.15, TotalLatencyMs: 45000, RoundsRun: 2}

	return regressionloop.BuildOptimizationReport(
		cfg,
		&regressionloop.RoundSummary{OverallScore: baselineRun.OverallScore, TotalCases: 3, PassCount: 1, FailCount: 2},
		&regressionloop.RoundSummary{OverallScore: candidateRun.OverallScore, TotalCases: 3, PassCount: 3, FailCount: 0},
		baselineRun, candidateRun, trainBaseline, trainCandidate,
		attributions, rounds, costSummary,
	)
}
