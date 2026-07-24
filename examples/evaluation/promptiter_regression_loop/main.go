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
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	appName         = "promptiter-regression-loop"
	agentName       = "support-agent"
	targetSurfaceID = agentName + "#instruction"
	reportJSON      = "optimization_report.json"
	reportMarkdown  = "optimization_report.md"
)

func main() {
	dataDir := flag.String("data-dir", "./data", "directory containing native eval sets and loop configuration")
	outputDir := flag.String("output-dir", "./output", "directory for optimization reports")
	flag.Parse()
	if _, err := run(context.Background(), *dataDir, *outputDir); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, dataDir, outputDir string) (*regression.Report, error) {
	files := regression.InputFiles{
		TrainEvalSet:      filepath.Join(dataDir, "train.evalset.json"),
		ValidationEvalSet: filepath.Join(dataDir, "validation.evalset.json"),
		Metrics:           filepath.Join(dataDir, "metrics.json"),
		BaselinePrompt:    filepath.Join(dataDir, "baseline_prompt.txt"),
		PromptIterConfig:  filepath.Join(dataDir, "promptiter.json"),
		RegressionConfig:  filepath.Join(dataDir, "regression.json"),
	}
	config, err := regression.LoadRunConfig(ctx, appName, files)
	if err != nil {
		return nil, err
	}
	if config.PromptIter.TargetSurfaceIDs[0] != targetSurfaceID {
		return nil, fmt.Errorf(
			"configured target surface %q does not match example surface %q",
			config.PromptIter.TargetSurfaceIDs[0],
			targetSurfaceID,
		)
	}
	if err := regression.BindRuntime(config, regression.RuntimeConfig{
		Engine: "native-promptiter-deterministic",
		Seed:   config.Seed,
		Model: map[string]any{
			"name":        "deterministic-support-model-v1",
			"temperature": 0,
			"apiKeys":     false,
		},
		Evaluator: map[string]any{
			"appName":   appName,
			"name":      deterministicEvaluatorName,
			"numRuns":   1,
			"traceMode": "native_execution_trace",
			"caseCounts": map[string]any{
				"train":             len(config.Train.CaseIDs),
				"heldoutValidation": len(config.Validation.CaseIDs),
			},
		},
		FakeEngine: map[string]any{
			"backwarder": "loss-hint-gradient-v1",
			"aggregator": "stable-merge-v1",
			"optimizer":  "current-profile-seeded-remediation-v1",
		},
	}); err != nil {
		return nil, err
	}

	evalSetManager, metricManager, err := regression.NewInputManagers(files, config)
	if err != nil {
		return nil, err
	}
	defer evalSetManager.Close()
	defer metricManager.Close()
	meter := regression.NewUsageMeter()
	candidateAgent := newDeterministicAgent(meter)
	baseRunner := runner.NewRunner(appName, candidateAgent)
	annotatedRunner := &routeAnnotatingRunner{delegate: baseRunner}
	evaluatorRegistry := registry.New()
	if err := evaluatorRegistry.Register(
		deterministicEvaluatorName,
		&deterministicQualityEvaluator{},
	); err != nil {
		annotatedRunner.Close()
		return nil, err
	}
	agentEvaluator, err := evaluation.New(
		appName,
		annotatedRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalresultinmemory.New()),
		evaluation.WithRegistry(evaluatorRegistry),
		evaluation.WithNumRuns(1),
		evaluation.WithRunOptions(agent.WithDisableResponseUsageTracking(true)),
	)
	if err != nil {
		annotatedRunner.Close()
		return nil, fmt.Errorf("create native evaluator: %w", err)
	}
	defer agentEvaluator.Close()

	structure, err := astructure.Export(ctx, candidateAgent)
	if err != nil {
		return nil, fmt.Errorf("export candidate structure: %w", err)
	}
	profileEvaluator, err := regression.NewProfileEvaluator(regression.ProfileEvaluatorConfig{
		AppName:        appName,
		AgentEvaluator: agentEvaluator,
		EvalSetManager: evalSetManager,
		MetricManager:  metricManager,
		Structure:      structure,
		ResourceMeter:  meter,
	})
	if err != nil {
		return nil, err
	}
	nativeEngine, err := engine.New(
		ctx,
		engine.WithAgent(candidateAgent),
		engine.WithAgentEvaluator(agentEvaluator),
		engine.WithBackwarder(&deterministicBackwarder{seed: config.Seed, meter: meter}),
		engine.WithAggregator(&deterministicAggregator{meter: meter}),
		engine.WithOptimizer(&deterministicOptimizer{seed: config.Seed, meter: meter}),
	)
	if err != nil {
		return nil, fmt.Errorf("create native PromptIter engine: %w", err)
	}
	pipeline, err := regression.New(
		nativeEngine,
		profileEvaluator,
		regression.WithResourceMeter(meter),
	)
	if err != nil {
		return nil, err
	}
	report, err := pipeline.Run(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := regression.WriteArtifacts(
		report,
		filepath.Join(outputDir, config.Output.JSON),
		filepath.Join(outputDir, config.Output.Markdown),
	); err != nil {
		return nil, err
	}
	validationScore := "unavailable"
	if report.BaselineValidation != nil {
		validationScore = fmt.Sprintf("%.3f", report.BaselineValidation.OverallScore)
	}
	fmt.Fprintf(
		os.Stdout,
		"status=%s released=%s candidates=%d validation=%s\n",
		report.Status,
		report.ReleasedProfile.Hash,
		len(report.Candidates),
		validationScore,
	)
	return report, nil
}

func newDeterministicAgent(meter *regression.UsageMeter) *llmagent.LLMAgent {
	lookup := function.NewFunctionTool(
		lookupOrder,
		function.WithName("lookup_order"),
		function.WithDescription("Look up an order using its exact orderId."),
	)
	search := function.NewFunctionTool(
		searchWeb,
		function.WithName("search_web"),
		function.WithDescription("Search the public web."),
	)
	return llmagent.New(
		agentName,
		llmagent.WithModel(&deterministicSupportModel{meter: meter}),
		llmagent.WithInstruction("STATIC PLACEHOLDER: overridden by the evaluated PromptIter profile."),
		llmagent.WithDescription("Deterministic support agent used by the no-key regression example."),
		llmagent.WithTools([]tool.Tool{lookup, search}),
	)
}

type orderArguments struct {
	OrderID string `json:"orderId"`
}

type orderResult struct {
	OrderID string `json:"orderId"`
	Status  string `json:"status"`
}

func lookupOrder(_ context.Context, arguments orderArguments) (orderResult, error) {
	if arguments.OrderID == "" {
		return orderResult{}, errors.New("orderId is empty")
	}
	return orderResult{OrderID: arguments.OrderID, Status: "shipped"}, nil
}

func searchWeb(_ context.Context, arguments orderArguments) (orderResult, error) {
	return orderResult{OrderID: arguments.OrderID, Status: "unverified"}, nil
}
