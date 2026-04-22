//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main runs a local llm_judge_template evaluation example.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

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

type runOptions struct {
	DataDir   string
	OutputDir string
	ModelName string
	EvalSetID string
	Streaming bool
}

type exampleEvaluator interface {
	Evaluate(ctx context.Context, evalSetID string, opt ...evaluation.Option) (*evaluation.EvaluationResult, error)
	Close() error
}

type evaluatorFactory func(appName string, opts runOptions) (exampleEvaluator, error)

var (
	dataDir   = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir = flag.String("output-dir", "./output", "Directory where evaluation results are stored")
	modelName = flag.String("model", "gpt-5.2", "Model to use for both the agent and the judge")
	streaming = flag.Bool("streaming", false, "Enable streaming responses from the agent")
	evalSetID = flag.String("eval-set", "template-basic", "Evaluation set identifier to execute")
)

const appName = "template-eval-app"

func main() {
	flag.Parse()
	err := runExample(context.Background(), newLocalEvaluator, runOptions{
		DataDir:   *dataDir,
		OutputDir: *outputDir,
		ModelName: *modelName,
		EvalSetID: *evalSetID,
		Streaming: *streaming,
	})
	if err != nil {
		log.Fatal(err)
	}
}

func runExample(ctx context.Context, factory evaluatorFactory, opts runOptions) error {
	restoreModelName := setModelNameEnv(opts.ModelName)
	defer restoreModelName()
	agentEvaluator, err := factory(appName, opts)
	if err != nil {
		return fmt.Errorf("create evaluator: %w", err)
	}
	defer func() { _ = agentEvaluator.Close() }()
	result, err := agentEvaluator.Evaluate(ctx, opts.EvalSetID)
	if err != nil {
		return fmt.Errorf("evaluate: %w", err)
	}
	printSummary(result, opts.OutputDir)
	return nil
}

func setModelNameEnv(modelName string) func() {
	if modelName == "" {
		return func() {}
	}
	originalValue, existed := os.LookupEnv("MODEL_NAME")
	_ = os.Setenv("MODEL_NAME", modelName)
	return func() {
		if existed {
			_ = os.Setenv("MODEL_NAME", originalValue)
			return
		}
		_ = os.Unsetenv("MODEL_NAME")
	}
}

func newLocalEvaluator(appName string, opts runOptions) (exampleEvaluator, error) {
	run := runner.NewRunner(appName, newQAAgent(opts.ModelName, opts.Streaming))
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(opts.DataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(opts.DataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(opts.OutputDir))
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
		_ = run.Close()
		return nil, err
	}
	return &closableEvaluator{
		AgentEvaluator: agentEvaluator,
		runner:         run,
	}, nil
}

func printSummary(result *evaluation.EvaluationResult, outDir string) {
	fmt.Println("✅ Template evaluation completed with local storage")
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

type closableEvaluator struct {
	evaluation.AgentEvaluator
	runner runner.Runner
}

func (e *closableEvaluator) Close() error {
	if err := e.AgentEvaluator.Close(); err != nil {
		_ = e.runner.Close()
		return err
	}
	return e.runner.Close()
}
