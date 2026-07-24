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
	"fmt"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/internal/regression"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	candidateAgentName = "candidate"
	caseParallelism    = 1
)

type sharedMetricLocator struct{}

func (sharedMetricLocator) Build(baseDir, appName, _ string) string {
	return filepath.Join(baseDir, appName, "metrics.json")
}

type promptIterRuntime struct {
	candidate      *llmagent.LLMAgent
	agentEvaluator evaluation.AgentEvaluator
	runner         runner.Runner
}

func buildRuntime(cfg *config, baselinePrompt string) (*promptIterRuntime, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if strings.TrimSpace(baselinePrompt) == "" {
		return nil, errors.New("baseline prompt is empty")
	}
	temperature := 0.0
	candidate := llmagent.New(
		candidateAgentName,
		llmagent.WithModel(&deterministicModel{}),
		llmagent.WithInstruction(baselinePrompt),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Temperature: &temperature,
			Stream:      false,
		}),
	)
	candidateRunner := runner.NewRunner(cfg.AppName, candidate)
	managerBaseDir := filepath.Dir(cfg.DataDir)
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(managerBaseDir))
	metricManager := metriclocal.New(
		metric.WithBaseDir(managerBaseDir),
		metric.WithLocator(sharedMetricLocator{}),
	)
	agentEvaluator, err := evaluation.New(
		cfg.AppName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalresultinmemory.New()),
		evaluation.WithNumRuns(1),
	)
	if err != nil {
		closeErr := candidateRunner.Close()
		return nil, errors.Join(fmt.Errorf("create agent evaluator: %w", err), closeErr)
	}
	return &promptIterRuntime{
		candidate:      candidate,
		agentEvaluator: agentEvaluator,
		runner:         candidateRunner,
	}, nil
}

func (r *promptIterRuntime) engineForAttempt(
	ctx context.Context,
	prompt string,
	attempt int,
) (promptiterengine.Engine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.candidate == nil || r.agentEvaluator == nil {
		return nil, errors.New("runtime is incomplete")
	}
	engine, err := promptiterengine.New(
		ctx,
		promptiterengine.WithAgent(r.candidate),
		promptiterengine.WithAgentEvaluator(r.agentEvaluator),
		promptiterengine.WithBackwarder(&deterministicBackwarder{}),
		promptiterengine.WithAggregator(&deterministicAggregator{}),
		promptiterengine.WithOptimizer(&deterministicOptimizer{prompt: prompt, attempt: attempt}),
	)
	if err != nil {
		return nil, fmt.Errorf("create PromptIter engine: %w", err)
	}
	return engine, nil
}

func (r *promptIterRuntime) evaluateProfile(
	ctx context.Context,
	evalSetID string,
	profile *promptiter.Profile,
) (*regression.EvaluationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.candidate == nil || r.agentEvaluator == nil {
		return nil, errors.New("runtime is incomplete")
	}
	snapshot, err := astructure.Export(ctx, r.candidate)
	if err != nil {
		return nil, fmt.Errorf("export candidate structure: %w", err)
	}
	runOptions, err := compileProfileOptions(snapshot, profile)
	if err != nil {
		return nil, err
	}
	result, err := r.agentEvaluator.Evaluate(
		ctx,
		evalSetID,
		evaluation.WithRunDetailsEnabled(true),
		evaluation.WithRunOptions(runOptions...),
		evaluation.WithNumRuns(1),
		evaluation.WithEvalCaseParallelism(caseParallelism),
	)
	if err != nil {
		return nil, fmt.Errorf("evaluate profile on %q: %w", evalSetID, err)
	}
	normalized, err := regression.NormalizeAgentEvaluation(result)
	if err != nil {
		return nil, fmt.Errorf("normalize evaluation %q: %w", evalSetID, err)
	}
	return normalized, nil
}

func compileProfileOptions(snapshot *astructure.Snapshot, profile *promptiter.Profile) ([]agent.RunOption, error) {
	if snapshot == nil || snapshot.StructureID == "" {
		return nil, errors.New("candidate structure is incomplete")
	}
	surfaces := make(map[string]astructure.Surface, len(snapshot.Surfaces))
	for _, surface := range snapshot.Surfaces {
		if surface.SurfaceID == "" || surface.NodeID == "" {
			return nil, errors.New("candidate surface identity is incomplete")
		}
		surfaces[surface.SurfaceID] = surface
	}
	options := []agent.RunOption{agent.WithExecutionTraceEnabled(true)}
	if profile == nil {
		return options, nil
	}
	if profile.StructureID != "" && profile.StructureID != snapshot.StructureID {
		return nil, fmt.Errorf("profile structure id %q does not match %q", profile.StructureID, snapshot.StructureID)
	}
	seen := make(map[string]struct{}, len(profile.Overrides))
	for _, override := range profile.Overrides {
		if _, ok := seen[override.SurfaceID]; ok {
			return nil, fmt.Errorf("duplicate profile surface %q", override.SurfaceID)
		}
		seen[override.SurfaceID] = struct{}{}
		surface, ok := surfaces[override.SurfaceID]
		if !ok {
			return nil, fmt.Errorf("profile references unknown surface %q", override.SurfaceID)
		}
		if surface.Type != astructure.SurfaceTypeInstruction || override.Value.Text == nil {
			return nil, fmt.Errorf("surface %q is not a text instruction", override.SurfaceID)
		}
		if override.Value.PromptSyntax != nil || len(override.Value.FewShot) > 0 ||
			override.Value.Model != nil || len(override.Value.Tools) > 0 || len(override.Value.Skills) > 0 {
			return nil, fmt.Errorf("surface %q contains unsupported non-text values", override.SurfaceID)
		}
		var patch agent.SurfacePatch
		patch.SetInstruction(*override.Value.Text)
		options = append(options, agent.WithSurfacePatchForNode(surface.NodeID, patch))
	}
	return options, nil
}

func (r *promptIterRuntime) Close() error {
	if r == nil {
		return nil
	}
	var result error
	if r.agentEvaluator != nil {
		if err := r.agentEvaluator.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close evaluator: %w", err))
		}
	}
	if r.runner != nil {
		if err := r.runner.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close runner: %w", err))
		}
	}
	return result
}
