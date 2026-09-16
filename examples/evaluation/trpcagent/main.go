//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main runs a tRPC-Agent remote evaluation client.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	trpcagentrunner "trpc.group/trpc-go/trpc-agent-go/runner/trpcagent"
)

const (
	appName   = "trpcagent-travel-agent"
	modelName = "gpt-5.2"
)

var (
	target    = flag.String("target", "http://localhost:8081", "Target of the remote tRPC-Agent service")
	basePath  = flag.String("base-path", "/trpc-agent/v1/apps", "Base path of the remote tRPC-Agent service")
	dataDir   = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir = flag.String("output-dir", "./output", "Directory where evaluation results will be stored")
	evalSetID = flag.String("eval-set", "travel-advice-basic", "Evaluation set identifier to execute")
)

func main() {
	flag.Parse()
	ctx := context.Background()
	remoteRunner, err := trpcagentrunner.New(appName, trpcagentrunner.WithTarget(*target), trpcagentrunner.WithBasePath(*basePath))
	if err != nil {
		log.Fatalf("create tRPC-Agent runner: %v", err)
	}
	defer remoteRunner.Close()
	judgeRunner := runner.NewRunner(appName+"-judge", newJudgeAgent())
	defer judgeRunner.Close()
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
	reg := registry.New()
	evalOptions := []evaluation.Option{
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(reg),
		evaluation.WithJudgeRunner(judgeRunner),
		evaluation.WithRunDetailsEnabled(true),
		evaluation.WithRunOptions(agent.WithExecutionTraceEnabled(true)),
	}
	agentEvaluator, err := evaluation.New(appName, remoteRunner, evalOptions...)
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
	fmt.Println("tRPC-Agent remote evaluation completed")
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
			fmt.Printf("  Metric %s: score %.2f (threshold %.2f) => %s\n", metricResult.MetricName, metricResult.Score, metricResult.Threshold, metricResult.EvalStatus)
		}
		fmt.Println()
	}
	fmt.Printf("Results saved under: %s\n", outDir)
}

func newJudgeAgent() agent.Agent {
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0),
		Stream:      false,
	}
	return llmagent.New(
		appName+"-judge",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction("Follow the provided evaluation instructions exactly and return only the requested judge output."),
		llmagent.WithDescription("Judge agent used by the tRPC-Agent evaluation example."),
		llmagent.WithGenerationConfig(genCfg),
	)
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}
