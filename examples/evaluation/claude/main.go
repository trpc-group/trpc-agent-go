//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main implements an evaluation example for grading Claude CLI outputs.
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
	appName   = flag.String("app-name", "claude-eval-app", "App name used to locate evalset/metrics under data-dir")
	dataDir   = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir = flag.String("output-dir", "./output", "Directory where evaluation results will be stored")
	evalSetID = flag.String("eval-set", "claude-mcp-basic", "Evaluation set identifier to execute")
	claudeBin = flag.String("claude-bin", "claude", "Path to the claude executable")
	saveLog   = flag.Bool("claude-save-log", true, "Save claude stdout/stderr under output-dir")
)

// main runs the configured evaluation set against the Claude CLI agent.
func main() {
	flag.Parse()
	ctx := context.Background()

	ag, err := newClaudeAgent(claudeAgentConfig{
		ClaudeBin:     *claudeBin,
		SaveClaudeLog: *saveLog,
		OutputDir:     *outputDir,
	})
	if err != nil {
		log.Fatalf("create claude agent: %v", err)
	}

	runner := runner.NewRunner(*appName, ag)
	defer runner.Close()

	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
	reg := registry.New()

	agentEvaluator, err := evaluation.New(
		*appName,
		runner,
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

// printSummary prints a human-readable evaluation summary to stdout.
func printSummary(result *evaluation.EvaluationResult, outDir string) {
	fmt.Println("✅ Claude output evaluation completed with local storage")
	fmt.Printf("App: %s\n", result.AppName)
	fmt.Printf("Eval Set: %s\n", result.EvalSetID)
	fmt.Printf("Overall Status: %s\n", result.OverallStatus)

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
