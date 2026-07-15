//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/internal/profilecompiler"
)

// EnginePromptIterator adapts the existing PromptIter engine to Pipeline.
type EnginePromptIterator struct {
	Engine  promptiterengine.Engine
	Options []promptiterengine.Option
}

// Run delegates to the wrapped PromptIter engine.
func (p EnginePromptIterator) Run(
	ctx context.Context,
	request *promptiterengine.RunRequest,
) (*promptiterengine.RunResult, error) {
	if p.Engine == nil {
		return nil, errors.New("promptiter engine is nil")
	}
	return p.Engine.Run(ctx, request, p.Options...)
}

// EvaluationServiceEvaluator adapts evaluation.AgentEvaluator to baseline Pipeline evaluation.
type EvaluationServiceEvaluator struct {
	Evaluator     evaluation.AgentEvaluator
	Options       []evaluation.Option
	PromptApplier PromptApplier
}

// Evaluate runs the existing evaluation service and converts results into the PromptIter report shape.
func (e EvaluationServiceEvaluator) Evaluate(
	ctx context.Context,
	request EvaluationRequest,
) (*promptiterengine.EvaluationResult, error) {
	if e.Evaluator == nil {
		return nil, errors.New("evaluation service evaluator is nil")
	}
	options := append([]evaluation.Option{
		evaluation.WithRunDetailsEnabled(true),
	}, e.Options...)
	if e.PromptApplier != nil {
		promptOptions, err := e.PromptApplier.EvaluationOptions(request)
		if err != nil {
			return nil, fmt.Errorf("apply prompt: %w", err)
		}
		options = append(options, promptOptions...)
	}
	result, err := e.Evaluator.Evaluate(ctx, request.EvalSetID, options...)
	if err != nil {
		return nil, err
	}
	return AdaptEvaluationResult(result)
}

// PromptApplier turns a prompt source into evaluation options for one run.
type PromptApplier interface {
	EvaluationOptions(request EvaluationRequest) ([]evaluation.Option, error)
}

// PromptApplierFunc adapts a function into PromptApplier.
type PromptApplierFunc func(request EvaluationRequest) ([]evaluation.Option, error)

// EvaluationOptions calls f(request).
func (f PromptApplierFunc) EvaluationOptions(request EvaluationRequest) ([]evaluation.Option, error) {
	if f == nil {
		return nil, nil
	}
	return f(request)
}

// TextPromptSurfaceApplier applies the source prompt or candidate profile to one
// configured prompt surface during evaluation. Tool description surfaces compile
// to declaration-only overrides, preserving original callable tools and sibling
// tools at runtime.
type TextPromptSurfaceApplier struct {
	SurfaceIDs []string
}

// EvaluationOptions returns evaluation run options that patch target prompt surfaces.
func (a TextPromptSurfaceApplier) EvaluationOptions(request EvaluationRequest) ([]evaluation.Option, error) {
	if (request.Profile == nil && strings.TrimSpace(request.Prompt) == "") || len(a.SurfaceIDs) == 0 {
		return nil, nil
	}
	if len(a.SurfaceIDs) != 1 {
		return nil, fmt.Errorf("built-in prompt applier requires exactly one target surface id; got %v", a.SurfaceIDs)
	}
	profile := request.Profile
	if profile == nil {
		var err error
		profile, err = BuildPromptProfile(a.SurfaceIDs, request.Prompt)
		if err != nil {
			return nil, err
		}
	}
	runOptions, err := promptSurfaceRunOptions(profile)
	if err != nil {
		return nil, err
	}
	return []evaluation.Option{evaluation.WithRunOptions(runOptions...)}, nil
}

func promptSurfaceRunOptions(profile *promptiter.Profile) ([]agent.RunOption, error) {
	return profilecompiler.CompileRunOptions(toCompilerProfile(profile), false)
}

func promptSurfaceRunOption(surfaceID, prompt string) (agent.RunOption, error) {
	profile, err := BuildPromptProfile([]string{surfaceID}, prompt)
	if err != nil {
		return nil, err
	}
	runOptions, err := promptSurfaceRunOptions(profile)
	if err != nil {
		return nil, err
	}
	return func(opts *agent.RunOptions) {
		for _, runOption := range runOptions {
			runOption(opts)
		}
	}, nil
}

// BuildPromptProfile builds a PromptIter initial profile from a prompt file for
// one instruction, global-instruction, or tool-description surface.
func BuildPromptProfile(surfaceIDs []string, prompt string) (*promptiter.Profile, error) {
	if strings.TrimSpace(prompt) == "" || len(surfaceIDs) == 0 {
		return nil, nil
	}
	if len(surfaceIDs) != 1 {
		return nil, fmt.Errorf("built-in prompt profile requires exactly one target surface id; got %v", surfaceIDs)
	}
	overrides := make([]promptiter.SurfaceOverride, 0, len(surfaceIDs))
	for _, surfaceID := range surfaceIDs {
		nodeID, surfaceType, part, err := parsePromptSurfaceID(surfaceID)
		if err != nil {
			return nil, err
		}
		value, err := promptSurfaceValue(nodeID, surfaceType, part, prompt)
		if err != nil {
			return nil, fmt.Errorf("build prompt profile for %q: %w", surfaceID, err)
		}
		overrides = append(overrides, promptiter.SurfaceOverride{
			SurfaceID: strings.TrimSpace(surfaceID),
			Value:     value,
		})
	}
	return &promptiter.Profile{Overrides: overrides}, nil
}

// BuildTextPromptProfile builds a PromptIter initial profile from a prompt file.
// Deprecated: use BuildPromptProfile.
func BuildTextPromptProfile(surfaceIDs []string, prompt string) (*promptiter.Profile, error) {
	return BuildPromptProfile(surfaceIDs, prompt)
}

func promptSurfaceValue(
	nodeID string,
	surfaceType astructure.SurfaceType,
	part string,
	prompt string,
) (astructure.SurfaceValue, error) {
	switch surfaceType {
	case astructure.SurfaceTypeInstruction, astructure.SurfaceTypeGlobalInstruction:
		text := prompt
		return astructure.SurfaceValue{Text: &text}, nil
	case astructure.SurfaceTypeTool:
		return astructure.SurfaceValue{
			Tools: []astructure.ToolRef{{ID: partOrNode(part, nodeID), Description: prompt}},
		}, nil
	case astructure.SurfaceTypeFewShot, astructure.SurfaceTypeSkill:
		return astructure.SurfaceValue{}, errors.New("surface requires a custom PromptIterator/PromptApplier integration")
	case astructure.SurfaceTypeModel:
		return astructure.SurfaceValue{}, errors.New("model surface requires a concrete model adapter")
	default:
		return astructure.SurfaceValue{}, fmt.Errorf("unsupported surface type %q", surfaceType)
	}
}

func partOrNode(part, nodeID string) string {
	if strings.TrimSpace(part) != "" {
		return strings.TrimSpace(part)
	}
	return strings.TrimSpace(nodeID)
}

func toCompilerProfile(profile *promptiter.Profile) *profilecompiler.Profile {
	if profile == nil {
		return nil
	}
	converted := &profilecompiler.Profile{
		StructureID: profile.StructureID,
		Overrides:   make([]profilecompiler.SurfaceOverride, 0, len(profile.Overrides)),
	}
	for _, override := range profile.Overrides {
		nodeID, surfaceType, _, err := parsePromptSurfaceID(override.SurfaceID)
		if err != nil {
			converted.Overrides = append(converted.Overrides, profilecompiler.SurfaceOverride{
				SurfaceID: strings.TrimSpace(override.SurfaceID),
				Value:     override.Value,
			})
			continue
		}
		converted.Overrides = append(converted.Overrides, profilecompiler.SurfaceOverride{
			SurfaceID: strings.TrimSpace(override.SurfaceID),
			NodeID:    nodeID,
			Type:      surfaceType,
			Value:     override.Value,
		})
	}
	return converted
}

func parsePromptSurfaceID(surfaceID string) (string, astructure.SurfaceType, string, error) {
	nodeID, surfaceToken, ok := strings.Cut(strings.TrimSpace(surfaceID), "#")
	if !ok || strings.TrimSpace(nodeID) == "" || strings.TrimSpace(surfaceToken) == "" {
		return "", "", "", fmt.Errorf("invalid prompt surface id %q", surfaceID)
	}
	surfaceTypeText, part, _ := strings.Cut(strings.TrimSpace(surfaceToken), ".")
	surfaceType := astructure.SurfaceType(strings.TrimSpace(surfaceTypeText))
	switch surfaceType {
	case astructure.SurfaceTypeInstruction,
		astructure.SurfaceTypeGlobalInstruction,
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceTypeTool,
		astructure.SurfaceTypeSkill,
		astructure.SurfaceTypeModel:
		return strings.TrimSpace(nodeID), surfaceType, strings.TrimSpace(part), nil
	default:
		return "", "", "", fmt.Errorf(
			"surface %q is not a supported prompt surface; supported types: %s, %s, %s, %s, %s, %s",
			surfaceID,
			astructure.SurfaceTypeInstruction,
			astructure.SurfaceTypeGlobalInstruction,
			astructure.SurfaceTypeFewShot,
			astructure.SurfaceTypeTool,
			astructure.SurfaceTypeSkill,
			astructure.SurfaceTypeModel,
		)
	}
}

// AdaptEvaluationResult converts evaluation.EvaluationResult into promptiter engine EvaluationResult.
func AdaptEvaluationResult(result *evaluation.EvaluationResult) (*promptiterengine.EvaluationResult, error) {
	if result == nil {
		return nil, errors.New("evaluation result is nil")
	}
	cases := make([]promptiterengine.CaseResult, 0, len(result.EvalCases))
	totalScore := 0.0
	totalMetrics := 0
	for _, evalCase := range result.EvalCases {
		if evalCase == nil {
			continue
		}
		metrics := make([]promptiterengine.MetricResult, 0, len(evalCase.MetricResults))
		for _, metric := range evalCase.MetricResults {
			if metric == nil || metric.EvalStatus == status.EvalStatusNotEvaluated {
				continue
			}
			reason := ""
			if metric.Details != nil {
				reason = metric.Details.Reason
			}
			actualInvocation, expectedInvocation := invocationPairForMetric(metric, evalCase)
			metrics = append(metrics, promptiterengine.MetricResult{
				MetricName:         metric.MetricName,
				Score:              metric.Score,
				Status:             metric.EvalStatus,
				Reason:             reason,
				ActualInvocation:   actualInvocation,
				ExpectedInvocation: expectedInvocation,
			})
			totalScore += metric.Score
			totalMetrics++
		}
		sessionID, trace := firstRunDetails(evalCase.RunDetails)
		actualInvocation, expectedInvocation := firstInvocationPair(evalCase)
		cases = append(cases, promptiterengine.CaseResult{
			EvalSetID:          result.EvalSetID,
			EvalCaseID:         evalCase.EvalCaseID,
			SessionID:          sessionID,
			Trace:              trace,
			ActualInvocation:   actualInvocation,
			ExpectedInvocation: expectedInvocation,
			Metrics:            metrics,
		})
	}
	if totalMetrics == 0 {
		return nil, fmt.Errorf("evaluation result %q has no metric scores", result.EvalSetID)
	}
	score := totalScore / float64(totalMetrics)
	return &promptiterengine.EvaluationResult{
		OverallScore: score,
		EvalSets: []promptiterengine.EvalSetResult{
			{
				EvalSetID:    result.EvalSetID,
				OverallScore: score,
				Cases:        cases,
			},
		},
	}, nil
}

func firstRunDetails(details []*evaluation.EvaluationCaseRunDetails) (string, *atrace.Trace) {
	for _, detail := range details {
		if detail == nil || detail.Inference == nil {
			continue
		}
		var trace *atrace.Trace
		if len(detail.Inference.ExecutionTraces) > 0 {
			trace = detail.Inference.ExecutionTraces[0]
		}
		return detail.Inference.SessionID, trace
	}
	return "", nil
}

func invocationPairForMetric(
	result *evalresult.EvalMetricResult,
	evalCase *evaluation.EvaluationCaseResult,
) (*evalset.Invocation, *evalset.Invocation) {
	if result == nil || evalCase == nil {
		return nil, nil
	}
	var firstActual *evalset.Invocation
	var firstExpected *evalset.Invocation
	var statusActual *evalset.Invocation
	var statusExpected *evalset.Invocation
	for _, caseResult := range evalCase.EvalCaseResults {
		if caseResult == nil {
			continue
		}
		for _, perInvocation := range caseResult.EvalMetricResultPerInvocation {
			if perInvocation == nil {
				continue
			}
			for _, metric := range perInvocation.EvalMetricResults {
				if metric == nil || metric.MetricName != result.MetricName {
					continue
				}
				if firstActual == nil && firstExpected == nil {
					firstActual = perInvocation.ActualInvocation
					firstExpected = perInvocation.ExpectedInvocation
				}
				if metricMatchesAggregate(result, metric) {
					return perInvocation.ActualInvocation, perInvocation.ExpectedInvocation
				}
				if statusActual == nil && statusExpected == nil && metric.EvalStatus == result.EvalStatus {
					statusActual = perInvocation.ActualInvocation
					statusExpected = perInvocation.ExpectedInvocation
				}
			}
		}
	}
	if statusActual != nil || statusExpected != nil {
		return statusActual, statusExpected
	}
	return firstActual, firstExpected
}

func metricMatchesAggregate(aggregate, candidate *evalresult.EvalMetricResult) bool {
	if aggregate == nil || candidate == nil || candidate.EvalStatus != aggregate.EvalStatus {
		return false
	}
	aggregateReason := metricReason(aggregate)
	if aggregateReason != "" {
		return aggregateReason == metricReason(candidate)
	}
	if metricReason(candidate) != "" {
		return false
	}
	return candidate.Score == aggregate.Score
}

func metricReason(metric *evalresult.EvalMetricResult) string {
	if metric == nil || metric.Details == nil {
		return ""
	}
	return strings.TrimSpace(metric.Details.Reason)
}

func firstInvocationPair(evalCase *evaluation.EvaluationCaseResult) (*evalset.Invocation, *evalset.Invocation) {
	if evalCase == nil {
		return nil, nil
	}
	for _, caseResult := range evalCase.EvalCaseResults {
		if caseResult == nil {
			continue
		}
		for _, perInvocation := range caseResult.EvalMetricResultPerInvocation {
			if perInvocation == nil {
				continue
			}
			if perInvocation.ActualInvocation != nil || perInvocation.ExpectedInvocation != nil {
				return perInvocation.ActualInvocation, perInvocation.ExpectedInvocation
			}
		}
	}
	return nil, nil
}
