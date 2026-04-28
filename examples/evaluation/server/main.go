//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"flag"
	"log"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sevaluation "trpc.group/trpc-go/trpc-agent-go/server/evaluation"
)

var (
	addr      = flag.String("addr", ":8080", "Listen address for the evaluation server")
	basePath  = flag.String("base-path", "/evaluation", "Base path exposed by the evaluation server")
	dataDir   = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir = flag.String("output-dir", "./output", "Directory where evaluation results will be stored")
	modelName = flag.String("model", "deepseek-v4-flash", "Model to use for evaluation runs")
	streaming = flag.Bool("streaming", false, "Enable streaming responses from the agent")
)

const appName = "math-eval-app"

func main() {
	flag.Parse()
	agentRunner := runner.NewRunner(appName, newCalculatorAgent(*modelName, *streaming))
	defer agentRunner.Close()
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
	registry := registry.New()
	agentEvaluator, err := evaluation.New(
		appName,
		agentRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(registry),
	)
	if err != nil {
		log.Fatalf("create agent evaluator: %v", err)
	}
	defer func() {
		if err := agentEvaluator.Close(); err != nil {
			log.Printf("close agent evaluator: %v", err)
		}
	}()
	server, err := sevaluation.New(
		sevaluation.WithAppName(appName),
		sevaluation.WithBasePath(*basePath),
		sevaluation.WithAgentEvaluator(agentEvaluator),
		sevaluation.WithEvalSetManager(evalSetManager),
		sevaluation.WithEvalResultManager(evalResultManager),
	)
	if err != nil {
		log.Fatalf("create evaluation server: %v", err)
	}
	log.Printf("Evaluation server listening on %s%s", *addr, server.BasePath())
	if err := http.ListenAndServe(*addr, server.Handler()); err != nil {
		log.Fatalf("listen and serve: %v", err)
	}
}
