//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates an auditable PromptIter regression loop without API keys.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression/adapter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression/artifact"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression/attribution"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression/delta"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression/gate"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	supportAgentName = "support-agent"
	appName          = "promptiter-regression-support"
	baseInstruction  = "You are a customer-support agent."
)

func main() {
	scenario := flag.String("scenario", "success", "deterministic optimizer scenario: success, no-effect, or overfit")
	runID := flag.String("run-id", "", "unique immutable run identifier; generated when empty")
	output := flag.String("output", "output", "artifact output directory")
	config := flag.String("config", "promptiter/regressionloop/data", "directory containing eval sets, metrics, optimizer config, and baseline prompt")
	flag.Parse()
	resolvedRunID := *runID
	if resolvedRunID == "" {
		resolvedRunID = fmt.Sprintf("fake-%s-%s", *scenario, time.Now().UTC().Format("20060102T150405.000000000Z"))
	}
	result, files, err := run(context.Background(), *scenario, resolvedRunID, *output, *config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("run=%s decision=%s selected=%s artifacts=%d\n",
		result.RunID, result.Decision, result.SelectedCandidateID, len(files))
}

func run(
	ctx context.Context,
	scenario string,
	runID string,
	output string,
	configDir string,
) (*regression.RunResult, []artifact.File, error) {
	if err := validateScenario(scenario); err != nil {
		return nil, nil, err
	}
	loaded, err := loadInputs(configDir)
	if err != nil {
		return nil, nil, err
	}
	runtime, err := newExampleRuntime(ctx, loaded, scenario)
	if err != nil {
		return nil, nil, err
	}
	defer runtime.close()
	started := time.Now()
	promptIterResult, err := runtime.promptIter.Run(ctx, buildEngineRequest(loaded, runtime, scenario))
	if err != nil {
		return nil, nil, fmt.Errorf("run PromptIter: %w", err)
	}
	usage, err := summarizeUsage(promptIterResult, runtime.agent, time.Since(started))
	if err != nil {
		return nil, nil, err
	}
	result, err := auditResult(ctx, loaded, scenario, runID, runtime.targetSurfaceID, promptIterResult, usage)
	if err != nil {
		return result, nil, err
	}
	files, err := writeArtifacts(ctx, output, result)
	return result, files, err
}

func validateScenario(scenario string) error {
	switch scenario {
	case "success", "no-effect", "overfit":
		return nil
	default:
		return fmt.Errorf("unsupported scenario %q", scenario)
	}
}

type exampleRuntime struct {
	agent           *supportAgent
	agentRunner     runner.Runner
	agentEvaluator  evaluation.AgentEvaluator
	promptIter      engine.Engine
	targetSurfaceID string
	baselineProfile *promptiter.Profile
}

func newExampleRuntime(
	ctx context.Context,
	loaded *inputs,
	scenario string,
) (*exampleRuntime, error) {
	agentInstance := newSupportAgent(baseInstruction)
	structure, err := astructure.Export(ctx, agentInstance)
	if err != nil {
		return nil, fmt.Errorf("export agent structure: %w", err)
	}
	targetSurfaceID := astructure.SurfaceID(supportAgentName, astructure.SurfaceTypeInstruction)
	baselineProfile := &promptiter.Profile{
		StructureID: structure.StructureID,
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: targetSurfaceID,
			Value:     astructure.SurfaceValue{Text: stringPointer(loaded.baselinePrompt)},
		}},
	}
	agentRunner := runner.NewRunner(appName, agentInstance)
	agentEvaluator, err := newAgentEvaluator(ctx, loaded, agentRunner)
	if err != nil {
		agentRunner.Close()
		return nil, err
	}
	promptIter, err := newPromptIter(ctx, loaded, scenario, agentInstance, agentEvaluator)
	if err != nil {
		agentEvaluator.Close()
		agentRunner.Close()
		return nil, err
	}
	return &exampleRuntime{
		agent: agentInstance, agentRunner: agentRunner, agentEvaluator: agentEvaluator,
		promptIter: promptIter, targetSurfaceID: targetSurfaceID, baselineProfile: baselineProfile,
	}, nil
}

func newAgentEvaluator(
	ctx context.Context,
	loaded *inputs,
	agentRunner runner.Runner,
) (evaluation.AgentEvaluator, error) {
	evalSetManager := evalsetinmemory.New()
	metricManager := metricinmemory.New()
	if err := populateEvaluationAssets(ctx, loaded, evalSetManager, metricManager); err != nil {
		return nil, err
	}
	evaluatorRegistry := registry.New()
	if err := evaluatorRegistry.Register(contractEvaluatorName, contractEvaluator{}); err != nil {
		return nil, fmt.Errorf("register deterministic evaluator: %w", err)
	}
	result, err := evaluation.New(
		appName,
		agentRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalresultinmemory.New()),
		evaluation.WithRegistry(evaluatorRegistry),
	)
	if err != nil {
		return nil, fmt.Errorf("create Evaluation Service: %w", err)
	}
	return result, nil
}

func populateEvaluationAssets(
	ctx context.Context,
	loaded *inputs,
	evalSetManager evalset.Manager,
	metricManager metric.Manager,
) error {
	for _, evalSetID := range []string{loaded.train.EvalSetID, loaded.validation.EvalSetID} {
		if _, err := evalSetManager.Create(ctx, appName, evalSetID); err != nil {
			return fmt.Errorf("create eval set %q: %w", evalSetID, err)
		}
	}
	for _, evaluationCase := range loaded.train.EvalCases {
		if err := evalSetManager.AddCase(ctx, appName, loaded.train.EvalSetID, evaluationCase); err != nil {
			return fmt.Errorf("add train case: %w", err)
		}
	}
	for _, evaluationCase := range loaded.validation.EvalCases {
		if err := evalSetManager.AddCase(ctx, appName, loaded.validation.EvalSetID, evaluationCase); err != nil {
			return fmt.Errorf("add validation case: %w", err)
		}
	}
	for _, evalSetID := range []string{loaded.train.EvalSetID, loaded.validation.EvalSetID} {
		for _, configuredMetric := range loaded.metrics {
			metricCopy := *configuredMetric
			if err := metricManager.Add(ctx, appName, evalSetID, &metricCopy); err != nil {
				return fmt.Errorf("add metric %q: %w", metricCopy.MetricName, err)
			}
		}
	}
	return nil
}

func newPromptIter(
	ctx context.Context,
	loaded *inputs,
	scenario string,
	agentInstance agent.Agent,
	agentEvaluator evaluation.AgentEvaluator,
) (engine.Engine, error) {
	result, err := engine.New(
		ctx,
		engine.WithAgent(agentInstance),
		engine.WithAgentEvaluator(agentEvaluator),
		engine.WithBackwarder(deterministicBackwarder{}),
		engine.WithAggregator(deterministicAggregator{}),
		engine.WithOptimizer(deterministicOptimizer{
			scenario: scenario, trainInputs: trainInputIndex(loaded),
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create PromptIter engine: %w", err)
	}
	return result, nil
}

func buildEngineRequest(loaded *inputs, runtime *exampleRuntime, scenario string) *engine.RunRequest {
	maxRounds := loaded.config.MaxRounds
	acceptance := engine.AcceptancePolicy{MinScoreGain: -1}
	stopPolicy := engine.StopPolicy{}
	if scenario == "success" {
		acceptance.MinScoreGain = .01
		stopPolicy.TargetScore = loaded.config.TargetScore
	} else if maxRounds > 2 {
		maxRounds = 2
	}
	return &engine.RunRequest{
		Train:          []engine.EvalSetInput{{EvalSetID: loaded.train.EvalSetID}},
		Validation:     []engine.EvalSetInput{{EvalSetID: loaded.validation.EvalSetID}},
		InitialProfile: runtime.baselineProfile,
		EvaluationOptions: engine.EvaluationOptions{
			NumRuns:                  loaded.config.NumRuns,
			TraceUsageCoversAllCalls: true,
		},
		// The deterministic fake engine explores every candidate. The stricter
		// regression gate below is the production write-back decision.
		AcceptancePolicy: acceptance,
		StopPolicy:       stopPolicy,
		MaxRounds:        maxRounds,
		TargetSurfaceIDs: []string{runtime.targetSurfaceID},
	}
}

func summarizeUsage(
	promptIterResult *engine.RunResult,
	agentInstance *supportAgent,
	elapsed time.Duration,
) (regression.UsageSummary, error) {
	zeroCost := 0.0
	usage, err := adapter.SummarizeUsage(promptIterResult, elapsed, &zeroCost)
	if err != nil {
		return regression.UsageSummary{}, fmt.Errorf("summarize usage: %w", err)
	}
	if !usage.Complete {
		return regression.UsageSummary{}, errors.New("PromptIter engine usage is incomplete")
	}
	if usage.Calls != agentInstance.Calls() {
		return regression.UsageSummary{}, fmt.Errorf(
			"PromptIter engine calls %d do not match support agent calls %d",
			usage.Calls,
			agentInstance.Calls(),
		)
	}
	usage.Source = "deterministic_example"
	return usage, nil
}

func auditResult(
	ctx context.Context,
	loaded *inputs,
	scenario string,
	runID string,
	targetSurfaceID string,
	promptIterResult *engine.RunResult,
	usage regression.UsageSummary,
) (*regression.RunResult, error) {
	analyzer, err := regression.New(regression.Dependencies{
		Attributor:  attribution.NewRules(),
		DeltaEngine: delta.New(1e-9),
		Gate:        gate.NewPolicy(),
	})
	if err != nil {
		return nil, err
	}
	return analyzer.Analyze(ctx, &regression.RunSpec{
		RunID:           runID,
		TargetSurfaceID: targetSurfaceID,
		MetricPolicies:  loaded.config.MetricPolicies,
		CriticalCaseIDs: loaded.config.CriticalCaseIDs,
		Gate:            loaded.config.Gate,
		Budget:          loaded.config.Budget,
		Runtime: regression.RuntimePolicy{
			Seed: loaded.config.Seed, NumRuns: loaded.config.NumRuns,
			Deterministic: true,
		},
		Audit:            loaded.config.Audit,
		InputFingerprint: scenarioFingerprint(loaded.fingerprint, scenario),
		Metadata: map[string]string{
			"engine":     "promptiter-engine",
			"model":      "fake-no-api-key",
			"optimizer":  optimizerName(scenario),
			"maxRounds":  fmt.Sprint(promptIterResult.Configuration.MaxRounds),
			"randomness": "none",
		},
	}, promptIterResult, usage)
}

func optimizerName(scenario string) string {
	if scenario == "success" {
		return "deterministic-progressive-capability-repair"
	}
	return "deterministic-" + scenario
}

func writeArtifacts(
	ctx context.Context,
	output string,
	result *regression.RunResult,
) ([]artifact.File, error) {
	store, err := artifact.NewStore(output)
	if err != nil {
		return nil, err
	}
	return artifact.WriteReports(ctx, store, result)
}

func (r *exampleRuntime) close() {
	if r == nil {
		return
	}
	if r.agentEvaluator != nil {
		_ = r.agentEvaluator.Close()
	}
	if r.agentRunner != nil {
		_ = r.agentRunner.Close()
	}
}

func trainInputIndex(loaded *inputs) map[string]string {
	result := make(map[string]string, len(loaded.train.EvalCases))
	for _, evaluationCase := range loaded.train.EvalCases {
		if evaluationCase == nil || len(evaluationCase.Conversation) == 0 ||
			evaluationCase.Conversation[0] == nil || evaluationCase.Conversation[0].UserContent == nil {
			continue
		}
		result[evaluationCase.EvalID] = evaluationCase.Conversation[0].UserContent.Content
	}
	return result
}

func stringPointer(value string) *string { return &value }
