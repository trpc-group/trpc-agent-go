//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

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
	// dataDir points to the directory containing evaluation set and metric files.
	dataDir = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	// outputDir points to the directory where evaluation results will be stored.
	outputDir = flag.String("output-dir", "./output", "Directory where evaluation results will be stored")
	// modelName selects which LLM model is used by the agent.
	modelName = flag.String("model", "gpt-5.2", "Model to use for evaluation runs")
	// streaming enables streaming responses from the agent.
	streaming = flag.Bool("streaming", false, "Enable streaming responses from the agent")
	// evalSetID selects which EvalSet ID should be executed.
	evalSetID = flag.String("eval-set", "rouge-basic", "Evaluation set identifier to execute")
	// numRuns controls how many times each evaluation case is repeated.
	numRuns = flag.Int("runs", 1, "Number of times to repeat the evaluation loop per case")
)

// appName is used as the evaluation app identifier for this example.
const appName = "rouge-app"

// main runs the ROUGE evaluation example.
func main() {
	flag.Parse()
	ctx := context.Background()
	r := runner.NewRunner(appName, newRougeAgent(*modelName, *streaming))
	defer r.Close()
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
	reg := registry.New()
	agentEvaluator, err := evaluation.New(
		appName,
		r,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(reg),
		evaluation.WithNumRuns(*numRuns),
	)
	if err != nil {
		log.Fatalf("create evaluator: %v", err)
	}
	defer agentEvaluator.Close()
	result, err := agentEvaluator.Evaluate(ctx, *evalSetID)
	if err != nil {
		log.Fatalf("evaluate: %v", err)
	}
	printSummary(result, *outputDir)
}

// printSummary prints a short human-readable evaluation summary.
func printSummary(result *evaluation.EvaluationResult, outDir string) {
	fmt.Println("✅ Evaluation completed with ROUGE criterion")
	fmt.Printf("App: %s\n", result.AppName)
	fmt.Printf("Eval Set: %s\n", result.EvalSetID)
	fmt.Printf("Overall Status: %s\n", result.OverallStatus)
	runs := 0
	if len(result.EvalCases) > 0 {
		runs = len(result.EvalCases[0].EvalCaseResults)
	}
	fmt.Printf("Runs: %d\n", runs)
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
