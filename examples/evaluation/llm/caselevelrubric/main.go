//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main runs a local case-level rubric evaluation example.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	dataDir   = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir = flag.String("output-dir", "./output", "Directory where evaluation results will be stored")
	modelName = flag.String("model", "gpt-5.2", "Model to use for evaluation runs")
	streaming = flag.Bool("streaming", false, "Enable streaming responses from the agent")
	evalSetID = flag.String("eval-set", "case-level-rubric-basic", "Evaluation set identifier to execute")
)

const appName = "case-level-rubric-app"

func main() {
	flag.Parse()
	ctx := context.Background()
	run := runner.NewRunner(appName, newTravelAgent(*modelName, *streaming))
	defer run.Close()
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
	reg := registry.New()
	agentEvaluator, err := evaluation.New(
		appName,
		run,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(reg),
	)
	if err != nil {
		log.Fatalf("create evaluator: %v", err)
	}
	defer func() { agentEvaluator.Close() }()
	result, err := agentEvaluator.Evaluate(ctx, *evalSetID)
	if err != nil {
		log.Fatalf("evaluate: %v", err)
	}
	printSummary(result, *outputDir)
}

func printSummary(result *evaluation.EvaluationResult, outDir string) {
	fmt.Println("✅ Case-level rubric evaluation completed with local storage")
	fmt.Printf("App: %s\n", result.AppName)
	fmt.Printf("Eval Set: %s\n", result.EvalSetID)
	fmt.Printf("Overall Status: %s\n", result.OverallStatus)
	fmt.Printf("Runs: %d\n", countRuns(result))
	for _, caseResult := range result.EvalCases {
		fmt.Printf("Case %s -> %s\n", caseResult.EvalCaseID, caseResult.OverallStatus)
		for _, metricResult := range caseResult.MetricResults {
			fmt.Printf("  Metric %s: score %.2f (threshold %.2f) => %s\n",
				metricResult.MetricName,
				metricResult.Score,
				metricResult.Threshold,
				metricResult.EvalStatus,
			)
		}
		fmt.Println()
	}
	fmt.Printf("Results saved under: %s\n", outDir)
}

func countRuns(result *evaluation.EvaluationResult) int {
	if result.EvalResult != nil && result.EvalResult.Summary != nil && result.EvalResult.Summary.NumRuns > 0 {
		return result.EvalResult.Summary.NumRuns
	}
	runs := 0
	for _, caseResult := range result.EvalCases {
		if len(caseResult.EvalCaseResults) > runs {
			runs = len(caseResult.EvalCaseResults)
		}
	}
	return runs
}
