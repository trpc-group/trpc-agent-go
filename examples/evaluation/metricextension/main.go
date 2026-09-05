//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"flag"
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
	evalSet   = flag.String("eval-set", defaultEvalSetID, "Evaluation set identifier to execute")
	modelName = flag.String("model", "deepseek-v4-flash", "Model to use for evaluation runs")
	streaming = flag.Bool("streaming", false, "Enable streaming responses from the agent")
	numRuns   = flag.Int("runs", 1, "Number of times to repeat the evaluation loop per case")
)

const (
	appName                     = "metric-extension-app"
	defaultEvalSetID            = "support-policy-basic"
	supportAgentName            = "support-agent"
	responsePolicyEvaluatorName = "support_response_policy"
)

func main() {
	flag.Parse()
	ctx := context.Background()
	run := runner.NewRunner(appName, newSupportAgent(*modelName, *streaming))
	defer run.Close()
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
	reg := registry.New()
	if err := reg.Register(responsePolicyEvaluatorName, responsePolicyEvaluator{}); err != nil {
		log.Fatalf("register response policy evaluator: %v", err)
	}
	agentEvaluator, err := evaluation.New(
		appName,
		run,
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
	result, err := agentEvaluator.Evaluate(ctx, *evalSet)
	if err != nil {
		log.Fatalf("evaluate: %v", err)
	}
	printSummary(result, *outputDir)
}
