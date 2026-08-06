//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Command promptiter_regression_loop runs the Evaluation + Optimization closed
// loop (baseline evaluation -> failure attribution -> PromptIter optimization ->
// validation regression -> acceptance gate -> audit report) with a deterministic
// fake model, so the whole pipeline works without any real model API key.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"path/filepath"
)

func main() {
	var (
		configPath = flag.String("config", "data/headline-card-app/promptiter.json", "Path to the pipeline configuration file")
		outputJSON = flag.String("output-json", "output/optimization_report.json", "Path of the generated JSON report")
		outputMD   = flag.String("output-md", "output/optimization_report.md", "Path of the generated Markdown report")
	)
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	pr, err := RunPipeline(context.Background(), cfg)
	if err != nil {
		log.Fatalf("run pipeline: %v", err)
	}

	report := BuildReport(pr)
	if err := WriteJSONReport(report, *outputJSON); err != nil {
		log.Fatalf("write JSON report: %v", err)
	}
	if err := WriteMarkdownReport(report, *outputMD); err != nil {
		log.Fatalf("write Markdown report: %v", err)
	}

	printSummary(pr)
	fmt.Printf("reports written to %s and %s\n", *outputJSON, *outputMD)
}

// printSummary prints a concise console summary of the pipeline outcome.
func printSummary(pr *PipelineResult) {
	baselineScore := 0.0
	if pr.BaselineValidation != nil {
		baselineScore = pr.BaselineValidation.OverallScore
	}
	fmt.Printf("\n=== Evaluation + Optimization 回归闭环 ===\n")
	fmt.Printf("RunID:        %s\n", pr.RunID)
	fmt.Printf("Baseline:     训练 %.3f,验证 %.3f\n", scoreOf(pr.BaselineTrain), baselineScore)
	for _, round := range pr.Rounds {
		fmt.Printf("Round %d:     训练 %.3f,验证 %.3f,engine accepted=%v,gate accepted=%v\n",
			round.Round, scoreOf(round.Train), scoreOf(round.Validation),
			round.EngineAccepted, round.GateAccepted)
	}
	fmt.Printf("Final:        %s\n", pr.Recommendation)
	fmt.Printf("Cost:         %d model calls,%d ms\n", pr.ModelCalls, pr.LatencyMs)
	fmt.Printf("Report:       %s\n", filepath.Join("output", "optimization_report.json"))
}
