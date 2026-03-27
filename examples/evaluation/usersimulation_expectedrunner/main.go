//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates evaluation with usersimulation and expected runner together.
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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/usersimulation"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	dataDir                 = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir               = flag.String("output-dir", "./output", "Directory where evaluation results will be stored")
	modelName               = flag.String("model", "gpt-5.4", "Model to use for the candidate agent")
	expectedModelName       = flag.String("expected-model", "", "Model to use for the expected runner. Defaults to -model")
	simulatorModelName      = flag.String("simulator-model", "", "Model to use for the simulator agent. Defaults to -model")
	judgeModelName          = flag.String("judge-model", "", "Model to use for the judge runner. Defaults to -expected-model when set, otherwise -model")
	expectedReasoningEffort = flag.String("expected-reasoning-effort", "medium", "Reasoning effort for the expected runner. Set an empty string to disable the override")
	judgeReasoningEffort    = flag.String("judge-reasoning-effort", "medium", "Reasoning effort for the judge runner. Set an empty string to disable the override")
	streaming               = flag.Bool("streaming", false, "Enable streaming responses from the candidate agent")
	evalSetID               = flag.String("eval-set", "business-trip-expected-runner", "Evaluation set identifier to execute")
)

const appName = "usersimulation_expectedrunner_app"

func main() {
	flag.Parse()
	ctx := context.Background()
	expectedModel := firstNonEmpty(*expectedModelName, *modelName)
	simulatorModel := firstNonEmpty(*simulatorModelName, *modelName)
	judgeModel := firstNonEmpty(*judgeModelName, expectedModel, *modelName)
	actualRunner := runner.NewRunner(appName, newCandidateTravelAgent(*modelName, *streaming))
	defer actualRunner.Close()
	expectedRunner := runner.NewRunner(appName+"-expected", newReferenceTravelAgent(expectedModel, *expectedReasoningEffort))
	defer expectedRunner.Close()
	simRunner := runner.NewRunner(appName+"-sim", newSimulatorAgent(simulatorModel))
	defer simRunner.Close()
	judgeRunner := runner.NewRunner(appName+"-judge", newJudgeAgent(judgeModel, *judgeReasoningEffort))
	defer judgeRunner.Close()
	userSimulator, err := usersimulation.New(simRunner)
	if err != nil {
		log.Fatalf("create user simulator: %v", err)
	}
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
	registry := registry.New()
	agentEvaluator, err := evaluation.New(
		appName,
		actualRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(registry),
		evaluation.WithExpectedRunner(expectedRunner),
		evaluation.WithJudgeRunner(judgeRunner),
		evaluation.WithUserSimulator(userSimulator),
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
	fmt.Println("✅ UserSimulation + ExpectedRunner evaluation completed with local storage")
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
