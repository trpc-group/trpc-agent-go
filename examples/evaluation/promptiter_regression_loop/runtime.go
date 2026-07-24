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
	"fmt"
	"path/filepath"

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
	candidateAgentName   = "candidate"
	candidateTemperature = 0.0
	evaluationRuns       = 1
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
	temperature := candidateTemperature
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
	evalResultManager := evalresultinmemory.New()
	agentEvaluator, err := evaluation.New(
		cfg.AppName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithNumRuns(evaluationRuns),
	)
	if err != nil {
		if closeErr := candidateRunner.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close candidate runner: %w", closeErr))
		}
		return nil, fmt.Errorf("create agent evaluator: %w", err)
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
	if r == nil || r.candidate == nil || r.agentEvaluator == nil {
		return nil, errors.New("runtime is incomplete")
	}
	instance, err := promptiterengine.New(
		ctx,
		promptiterengine.WithAgent(r.candidate),
		promptiterengine.WithAgentEvaluator(r.agentEvaluator),
		promptiterengine.WithBackwarder(&deterministicBackwarder{}),
		promptiterengine.WithAggregator(&deterministicAggregator{}),
		promptiterengine.WithOptimizer(&deterministicOptimizer{
			prompt: prompt, attempt: attempt,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create PromptIter engine: %w", err)
	}
	return instance, nil
}

func (r *promptIterRuntime) evaluateProfile(
	ctx context.Context,
	evalSetID string,
	profile *promptiter.Profile,
) (*regression.EvaluationResult, error) {
	if r == nil || r.candidate == nil || r.agentEvaluator == nil {
		return nil, errors.New("runtime is incomplete")
	}
	snapshot, err := astructure.Export(ctx, r.candidate)
	if err != nil {
		return nil, fmt.Errorf("export candidate structure: %w", err)
	}
	compiled, err := compileProfileOptions(snapshot, profile)
	if err != nil {
		return nil, err
	}
	result, err := r.agentEvaluator.Evaluate(ctx, evalSetID,
		evaluation.WithRunDetailsEnabled(true),
		evaluation.WithRunOptions(compiled...),
		evaluation.WithNumRuns(evaluationRuns),
		evaluation.WithEvalCaseParallelism(caseParallelism),
	)
	if err != nil {
		return nil, fmt.Errorf("evaluate profile on %q: %w", evalSetID, err)
	}
	return regression.NormalizeAgentEvaluation(result)
}

func compileProfileOptions(
	snapshot *astructure.Snapshot,
	profile *promptiter.Profile,
) ([]agent.RunOption, error) {
	surfaces, err := indexProfileSurfaces(snapshot)
	if err != nil {
		return nil, err
	}
	options := []agent.RunOption{agent.WithExecutionTraceEnabled(true)}
	if profile == nil {
		return options, nil
	}
	if profile.StructureID != "" && profile.StructureID != snapshot.StructureID {
		return nil, fmt.Errorf("profile structure id %q does not match structure id %q",
			profile.StructureID, snapshot.StructureID)
	}
	seen := make(map[string]struct{}, len(profile.Overrides))
	for _, override := range profile.Overrides {
		if override.SurfaceID == "" {
			return nil, errors.New("profile override surface id is empty")
		}
		if _, ok := seen[override.SurfaceID]; ok {
			return nil, fmt.Errorf("duplicate profile override surface id %q", override.SurfaceID)
		}
		seen[override.SurfaceID] = struct{}{}
		surface, ok := surfaces[override.SurfaceID]
		if !ok {
			return nil, fmt.Errorf("profile override references unknown surface id %q", override.SurfaceID)
		}
		text, err := instructionOverrideText(surface, override.Value)
		if err != nil {
			return nil, fmt.Errorf("validate profile override %q: %w", override.SurfaceID, err)
		}
		if *surface.Value.Text == text {
			continue
		}
		var patch agent.SurfacePatch
		patch.SetInstruction(text)
		options = append(options, agent.WithSurfacePatchForNode(surface.NodeID, patch))
	}
	return options, nil
}

func indexProfileSurfaces(snapshot *astructure.Snapshot) (map[string]astructure.Surface, error) {
	if snapshot == nil {
		return nil, errors.New("candidate structure is nil")
	}
	if snapshot.StructureID == "" {
		return nil, errors.New("candidate structure id is empty")
	}
	index := make(map[string]astructure.Surface, len(snapshot.Surfaces))
	for _, surface := range snapshot.Surfaces {
		if surface.SurfaceID == "" {
			return nil, errors.New("candidate surface id is empty")
		}
		if surface.NodeID == "" {
			return nil, fmt.Errorf("candidate surface %q node id is empty", surface.SurfaceID)
		}
		if _, ok := index[surface.SurfaceID]; ok {
			return nil, fmt.Errorf("duplicate candidate surface id %q", surface.SurfaceID)
		}
		index[surface.SurfaceID] = surface
	}
	return index, nil
}

func instructionOverrideText(surface astructure.Surface, value astructure.SurfaceValue) (string, error) {
	if surface.Type != astructure.SurfaceTypeInstruction {
		return "", fmt.Errorf("surface type %q is unsupported", surface.Type)
	}
	if surface.Value.Text == nil {
		return "", errors.New("baseline instruction text is nil")
	}
	if value.Text == nil {
		return "", errors.New("instruction text is nil")
	}
	if value.PromptSyntax != nil {
		return "", errors.New("instruction prompt syntax is not nil")
	}
	if len(value.FewShot) > 0 || value.Model != nil || len(value.Tools) > 0 || len(value.Skills) > 0 {
		return "", errors.New("instruction value contains non-text fields")
	}
	return *value.Text, nil
}

func (r *promptIterRuntime) Close() error {
	if r == nil {
		return nil
	}
	var result error
	if r.agentEvaluator != nil {
		if err := r.agentEvaluator.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close agent evaluator: %w", err))
		}
	}
	if r.runner != nil {
		if err := r.runner.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close candidate runner: %w", err))
		}
	}
	return result
}
