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
	"encoding/json"
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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	dataDir   = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir = flag.String("output-dir", "./output", "Directory where evaluation results will be stored")
	modelName = flag.String("model", "deepseek-chat", "Model to use for evaluation runs")
	streaming = flag.Bool("streaming", false, "Enable streaming responses from the agent")
	numRuns   = flag.Int("runs", 1, "Number of times to repeat the evaluation loop per case")
	evalSetID = flag.String("eval-set", "math-basic", "Evaluation set identifier to execute")
)

const appName = "math-eval-app"

func main() {
	flag.Parse()
	ctx := context.Background()
	runner := runner.NewRunner(appName, newCalculatorAgent(*modelName, *streaming))
	defer runner.Close()
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
	registry := registry.New()
	callbacks := service.NewCallbacks().Register("logger", newLoggingCallback())
	agentEvaluator, err := evaluation.New(
		appName,
		runner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(registry),
		evaluation.WithNumRuns(*numRuns),
		evaluation.WithCallbacks(callbacks),
	)
	if err != nil {
		log.Fatalf("create evaluator: %v", err)
	}
	defer func() { _ = agentEvaluator.Close() }()
	result, err := agentEvaluator.Evaluate(ctx, *evalSetID)
	if err != nil {
		log.Fatalf("evaluate: %v", err)
	}
	printSummary(result, *outputDir)
}

func newLoggingCallback() *service.Callback {
	return &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			printCallbackArgs("BeforeInferenceSet", args)
			return nil, nil
		},
		AfterInferenceSet: func(ctx context.Context, args *service.AfterInferenceSetArgs) (*service.AfterInferenceSetResult, error) {
			printCallbackArgs("AfterInferenceSet", args)
			return nil, nil
		},
		BeforeInferenceCase: func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
			printCallbackArgs("BeforeInferenceCase", args)
			return nil, nil
		},
		AfterInferenceCase: func(ctx context.Context, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
			printCallbackArgs("AfterInferenceCase", args)
			return nil, nil
		},
		BeforeEvaluateSet: func(ctx context.Context, args *service.BeforeEvaluateSetArgs) (*service.BeforeEvaluateSetResult, error) {
			printCallbackArgs("BeforeEvaluateSet", args)
			return nil, nil
		},
		AfterEvaluateSet: func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
			printCallbackArgs("AfterEvaluateSet", args)
			return nil, nil
		},
		BeforeEvaluateCase: func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
			printCallbackArgs("BeforeEvaluateCase", args)
			return nil, nil
		},
		AfterEvaluateCase: func(ctx context.Context, args *service.AfterEvaluateCaseArgs) (*service.AfterEvaluateCaseResult, error) {
			printCallbackArgs("AfterEvaluateCase", args)
			return nil, nil
		},
	}
}

func printCallbackArgs(point string, args any) {
	data, err := json.Marshal(args)
	if err != nil {
		fmt.Printf("[callback %s] error: %v\n", point, err)
		return
	}
	fmt.Printf("[callback %s] args=%s\n", point, string(data))
}

func printSummary(result *evaluation.EvaluationResult, outDir string) {
	fmt.Println("✅ Evaluation completed with callbacks")
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
