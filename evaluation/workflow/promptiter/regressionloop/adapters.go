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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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

// TextPromptSurfaceApplier applies the source prompt to configured prompt surfaces.
// Instruction/global-instruction surfaces are direct text replacements. Few-shot,
// tool, and skill surfaces receive deterministic text-derived runtime patches for
// local regression loops; production integrations can still provide a custom
// PromptApplier when they need to preserve existing tools, skills, or model
// instances.
type TextPromptSurfaceApplier struct {
	SurfaceIDs []string
}

// EvaluationOptions returns evaluation run options that patch target prompt surfaces.
func (a TextPromptSurfaceApplier) EvaluationOptions(request EvaluationRequest) ([]evaluation.Option, error) {
	if strings.TrimSpace(request.Prompt) == "" || len(a.SurfaceIDs) == 0 {
		return nil, nil
	}
	runOptions := make([]agent.RunOption, 0, len(a.SurfaceIDs))
	for _, surfaceID := range a.SurfaceIDs {
		runOption, err := promptSurfaceRunOption(surfaceID, request.Prompt)
		if err != nil {
			return nil, err
		}
		runOptions = append(runOptions, runOption)
	}
	return []evaluation.Option{evaluation.WithRunOptions(runOptions...)}, nil
}

func promptSurfaceRunOption(surfaceID, prompt string) (agent.RunOption, error) {
	nodeID, surfaceType, part, err := parsePromptSurfaceID(surfaceID)
	if err != nil {
		return nil, err
	}
	var patch agent.SurfacePatch
	switch surfaceType {
	case astructure.SurfaceTypeInstruction:
		patch.SetInstruction(prompt)
	case astructure.SurfaceTypeGlobalInstruction:
		patch.SetGlobalInstruction(prompt)
	case astructure.SurfaceTypeFewShot:
		patch.SetFewShot([][]model.Message{{model.NewSystemMessage(prompt)}})
	case astructure.SurfaceTypeTool:
		name := partOrNode(part, nodeID)
		patch.SetTools([]tool.Tool{
			promptDescriptionTool{
				declaration: tool.Declaration{
					Name:        name,
					Description: prompt,
				},
			},
		})
	case astructure.SurfaceTypeSkill:
		name := partOrNode(part, nodeID)
		patch.SetSkillRepository(singleSkillRepository{
			summary: skill.Summary{Name: name, Description: prompt},
		})
	case astructure.SurfaceTypeModel:
		return nil, fmt.Errorf("surface %q targets model selection; provide a custom PromptApplier with a concrete model", surfaceID)
	default:
		return nil, fmt.Errorf("surface %q is not a supported prompt surface", surfaceID)
	}
	return agent.WithSurfacePatchForNode(nodeID, patch), nil
}

// BuildPromptProfile builds a PromptIter initial profile from a prompt file.
// It supports text, few-shot, tool-description, and skill-description surfaces.
// Model surfaces require a concrete model instance and should use a custom
// PromptIterator/PromptApplier integration.
func BuildPromptProfile(surfaceIDs []string, prompt string) (*promptiter.Profile, error) {
	if strings.TrimSpace(prompt) == "" || len(surfaceIDs) == 0 {
		return nil, nil
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
	case astructure.SurfaceTypeFewShot:
		return astructure.SurfaceValue{
			FewShot: []astructure.FewShotExample{
				{Messages: []astructure.FewShotMessage{{Role: string(model.RoleSystem), Content: prompt}}},
			},
		}, nil
	case astructure.SurfaceTypeTool:
		return astructure.SurfaceValue{
			Tools: []astructure.ToolRef{{ID: partOrNode(part, nodeID), Description: prompt}},
		}, nil
	case astructure.SurfaceTypeSkill:
		return astructure.SurfaceValue{
			Skills: []astructure.SkillRef{{ID: partOrNode(part, nodeID), Description: prompt}},
		}, nil
	case astructure.SurfaceTypeModel:
		return astructure.SurfaceValue{}, errors.New("model surface requires a concrete model adapter")
	default:
		return astructure.SurfaceValue{}, fmt.Errorf("unsupported surface type %q", surfaceType)
	}
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

func partOrNode(part, nodeID string) string {
	if strings.TrimSpace(part) != "" {
		return strings.TrimSpace(part)
	}
	return strings.TrimSpace(nodeID)
}

type singleSkillRepository struct {
	summary skill.Summary
}

type promptDescriptionTool struct {
	declaration tool.Declaration
}

func (t promptDescriptionTool) Declaration() *tool.Declaration {
	return &t.declaration
}

func (r singleSkillRepository) Summaries() []skill.Summary {
	return []skill.Summary{r.summary}
}

func (r singleSkillRepository) Get(name string) (*skill.Skill, error) {
	if name != "" && name != r.summary.Name {
		return nil, fmt.Errorf("skill %q not found", name)
	}
	return &skill.Skill{Summary: r.summary, Body: r.summary.Description}, nil
}

func (r singleSkillRepository) Path(name string) (string, error) {
	if name != "" && name != r.summary.Name {
		return "", fmt.Errorf("skill %q not found", name)
	}
	return "", errors.New("single prompt-derived skill repository has no filesystem path")
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
			metrics = append(metrics, promptiterengine.MetricResult{
				MetricName: metric.MetricName,
				Score:      metric.Score,
				Status:     metric.EvalStatus,
				Reason:     reason,
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
