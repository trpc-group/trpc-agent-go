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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	regressionloop "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regressionloop"
)

type fakeEvaluator struct {
	baseDir  string
	appName  string
	scenario string
	trace    bool
}

func (e fakeEvaluator) Evaluate(_ context.Context, req regressionloop.EvaluationRequest) (*promptiterengine.EvaluationResult, error) {
	variant := "baseline"
	if req.Phase == regressionloop.PhaseCandidateValidation {
		variant = candidateVariantFromPrompt(req.Prompt, e.scenario)
	}
	result, err := loadFakeEvaluation(e.baseDir, req.EvalSetID, variant, req.Metrics)
	if err != nil {
		return nil, err
	}
	if e.trace {
		attachTraceEvidence(result, req.Config.TargetSurfaceIDs)
	}
	return result, nil
}

type fakePromptIterator struct {
	baseDir     string
	appName     string
	scenario    string
	metricsPath string
}

func (p fakePromptIterator) Run(_ context.Context, req *promptiterengine.RunRequest) (*promptiterengine.RunResult, error) {
	if req == nil {
		return nil, fmt.Errorf("run request is nil")
	}
	if len(req.Train) == 0 || len(req.Validation) == 0 {
		return nil, fmt.Errorf("train and validation eval sets are required")
	}
	metrics, err := regressionloop.LoadMetricDefinitions(p.metricsPath)
	if err != nil {
		return nil, err
	}
	baselineValidation, err := loadFakeEvaluation(p.baseDir, req.Validation[0].EvalSetID, "baseline", metrics)
	if err != nil {
		return nil, err
	}
	variant := candidateVariant(p.scenario)
	train, err := loadFakeEvaluation(p.baseDir, req.Train[0].EvalSetID, variant, metrics)
	if err != nil {
		return nil, err
	}
	validation, err := loadFakeEvaluation(p.baseDir, req.Validation[0].EvalSetID, variant, metrics)
	if err != nil {
		return nil, err
	}
	scoreDelta := validation.OverallScore - baselineValidation.OverallScore
	accepted := scoreDelta >= req.AcceptancePolicy.MinScoreGain
	reason := "candidate score gain does not satisfy acceptance policy"
	if accepted {
		reason = "candidate score gain satisfies acceptance policy"
	}
	optimizedPrompt := optimizedPromptForScenario(p.scenario)
	profile := &promptiter.Profile{}
	if accepted {
		profile = &promptiter.Profile{
			StructureID: "fake-structure",
			Overrides: []promptiter.SurfaceOverride{
				{
					SurfaceID: firstTargetSurface(req),
					Value:     astructure.SurfaceValue{Text: &optimizedPrompt},
				},
			},
		}
	}
	return &promptiterengine.RunResult{
		AppName:            p.appName,
		ID:                 "fake-regression-loop",
		Status:             promptiterengine.RunStatusSucceeded,
		CurrentRound:       1,
		BaselineValidation: baselineValidation,
		AcceptedProfile:    profile,
		Rounds: []promptiterengine.RoundResult{
			{
				Round:   1,
				Train:   train,
				Losses:  nil,
				Patches: fakePatchSet(firstTargetSurface(req), optimizedPrompt),
				OutputProfile: &promptiter.Profile{
					StructureID: "fake-structure",
					Overrides: []promptiter.SurfaceOverride{
						{
							SurfaceID: firstTargetSurface(req),
							Value:     astructure.SurfaceValue{Text: &optimizedPrompt},
						},
					},
				},
				Validation: validation,
				Acceptance: &promptiterengine.AcceptanceDecision{
					Accepted:   accepted,
					ScoreDelta: scoreDelta,
					Reason:     reason,
				},
			},
		},
	}, nil
}

type traceSmokePromptIterator struct {
	baseDir     string
	appName     string
	metricsPath string
}

func (p traceSmokePromptIterator) Run(_ context.Context, req *promptiterengine.RunRequest) (*promptiterengine.RunResult, error) {
	if req == nil || len(req.Validation) == 0 {
		return nil, fmt.Errorf("validation eval set is required")
	}
	metrics, err := regressionloop.LoadMetricDefinitions(p.metricsPath)
	if err != nil {
		return nil, err
	}
	baselineValidation, err := loadFakeEvaluation(p.baseDir, req.Validation[0].EvalSetID, "trace", metrics)
	if err != nil {
		return nil, err
	}
	attachTraceEvidence(baselineValidation, req.TargetSurfaceIDs)
	return &promptiterengine.RunResult{
		AppName:            p.appName,
		ID:                 "trace-smoke-regression-loop",
		Status:             promptiterengine.RunStatusSucceeded,
		CurrentRound:       0,
		BaselineValidation: baselineValidation,
		AcceptedProfile:    &promptiter.Profile{},
	}, nil
}

type fakeEvalSet struct {
	EvalSetID string         `json:"evalSetId"`
	EvalCases []fakeEvalCase `json:"evalCases"`
}

type fakeEvalCase struct {
	EvalID       string `json:"evalId"`
	SessionInput struct {
		State map[string]any `json:"state"`
	} `json:"sessionInput"`
}

func loadFakeEvaluation(
	baseDir,
	evalSetID,
	variant string,
	metrics []regressionloop.MetricDefinition,
) (*promptiterengine.EvaluationResult, error) {
	path := filepath.Join(baseDir, evalSetID+".evalset.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read evalset %q: %w", evalSetID, err)
	}
	var set fakeEvalSet
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("decode evalset %q: %w", evalSetID, err)
	}
	cases := make([]promptiterengine.CaseResult, 0, len(set.EvalCases))
	metricIndex := indexMetricDefinitions(metrics)
	total := 0.0
	for _, item := range set.EvalCases {
		state := item.SessionInput.State
		score := variantScore(state, variant)
		total += score
		metricName := stringState(state, "metricName")
		if metricName == "" {
			metricName = firstMetricName(metrics, "final_response")
		}
		metricDef, ok := metricIndex[metricName]
		if len(metricIndex) > 0 && !ok {
			return nil, fmt.Errorf("evalset %q case %q references metric %q missing from metrics.json",
				evalSetID, item.EvalID, metricName)
		}
		statusValue := variantStatus(state, variant, score, metricDef.Threshold)
		reason := variantReason(state, variant)
		cases = append(cases, promptiterengine.CaseResult{
			EvalSetID:  evalSetID,
			EvalCaseID: item.EvalID,
			Metrics: []promptiterengine.MetricResult{
				{
					MetricName: metricName,
					Score:      score,
					Status:     statusValue,
					Reason:     reason,
				},
			},
		})
	}
	overall := 0.0
	if len(cases) > 0 {
		overall = total / float64(len(cases))
	}
	return &promptiterengine.EvaluationResult{
		OverallScore: overall,
		EvalSets: []promptiterengine.EvalSetResult{
			{
				EvalSetID:    evalSetID,
				OverallScore: overall,
				Cases:        cases,
			},
		},
	}, nil
}

func indexMetricDefinitions(metrics []regressionloop.MetricDefinition) map[string]regressionloop.MetricDefinition {
	index := make(map[string]regressionloop.MetricDefinition, len(metrics))
	for _, metricDef := range metrics {
		index[metricDef.MetricName] = metricDef
	}
	return index
}

func firstMetricName(metrics []regressionloop.MetricDefinition, fallback string) string {
	for _, metricDef := range metrics {
		if strings.TrimSpace(metricDef.MetricName) != "" {
			return metricDef.MetricName
		}
	}
	return fallback
}

func fakePatchSet(surfaceID, prompt string) *promptiter.PatchSet {
	return &promptiter.PatchSet{
		Patches: []promptiter.SurfacePatch{
			{
				SurfaceID: surfaceID,
				Value:     astructure.SurfaceValue{Text: &prompt},
				Reason:    "fake optimizer merged train failure attributions into a stricter routing and formatting instruction",
			},
		},
	}
}

func firstTargetSurface(req *promptiterengine.RunRequest) string {
	if req != nil && len(req.TargetSurfaceIDs) > 0 {
		return req.TargetSurfaceIDs[0]
	}
	return "support_agent#instruction"
}

func candidateVariant(scenario string) string {
	switch scenarioOrDefault(scenario) {
	case "success":
		return "candidate_success"
	case "ineffective":
		return "candidate_ineffective"
	default:
		return "candidate"
	}
}

func candidateVariantFromPrompt(prompt string, scenario string) string {
	upperPrompt := strings.ToUpper(prompt)
	switch {
	case strings.Contains(upperPrompt, "SUCCESS_PROMPT"):
		return "candidate_success"
	case strings.Contains(upperPrompt, "INEFFECTIVE_PROMPT"):
		return "candidate_ineffective"
	case strings.Contains(upperPrompt, "OVERFIT_PROMPT"):
		return "candidate"
	default:
		return candidateVariant(scenario)
	}
}

func scenarioOrDefault(scenario string) string {
	switch strings.ToLower(strings.TrimSpace(scenario)) {
	case "success", "ineffective", "overfit", "trace-smoke":
		return strings.ToLower(strings.TrimSpace(scenario))
	default:
		return "overfit"
	}
}

func optimizedPromptForScenario(scenario string) string {
	switch scenarioOrDefault(scenario) {
	case "success":
		return "SUCCESS_PROMPT: fix routing, JSON formatting, knowledge recall, and preserve validation-critical policy dates."
	case "ineffective":
		return "INEFFECTIVE_PROMPT: be helpful, concise, and friendly."
	default:
		return "OVERFIT_PROMPT: optimize train failures aggressively, even if validation policy dates change."
	}
}

func variantScore(state map[string]any, variant string) float64 {
	switch variant {
	case "candidate_success":
		return 1
	case "candidate_ineffective", "trace":
		return numberState(state, "baselineScore")
	default:
		return numberState(state, variant+"Score")
	}
}

func variantStatus(state map[string]any, variant string, score float64, threshold float64) status.EvalStatus {
	switch variant {
	case "candidate_success":
		return status.EvalStatusPassed
	case "candidate_ineffective", "trace":
		return statusState(state, "baselineStatus", score, threshold)
	default:
		return statusState(state, variant+"Status", score, threshold)
	}
}

func variantReason(state map[string]any, variant string) string {
	switch variant {
	case "candidate_success":
		return "candidate satisfies metric after prompt optimization"
	case "candidate_ineffective", "trace":
		return stringState(state, "baselineReason")
	default:
		return stringState(state, variant+"Reason")
	}
}

func attachTraceEvidence(result *promptiterengine.EvaluationResult, surfaceIDs []string) {
	surfaceID := "support_agent#instruction"
	if len(surfaceIDs) > 0 && strings.TrimSpace(surfaceIDs[0]) != "" {
		surfaceID = surfaceIDs[0]
	}
	for setIndex := range result.EvalSets {
		for caseIndex := range result.EvalSets[setIndex].Cases {
			evalCase := &result.EvalSets[setIndex].Cases[caseIndex]
			traceStatus := atrace.TraceStatusCompleted
			stepError := ""
			for _, metric := range evalCase.Metrics {
				if metric.Status == status.EvalStatusFailed && strings.Contains(metric.Reason, "inference") {
					traceStatus = atrace.TraceStatusFailed
					stepError = metric.Reason
					break
				}
			}
			evalCase.Trace = &atrace.Trace{
				RootAgentName:    "support_agent",
				RootInvocationID: "invocation-" + evalCase.EvalCaseID,
				SessionID:        "session-" + evalCase.EvalCaseID,
				Status:           traceStatus,
				Steps: []atrace.Step{
					{
						StepID:            "step-" + evalCase.EvalCaseID,
						InvocationID:      "invocation-" + evalCase.EvalCaseID,
						AgentName:         "support_agent",
						NodeID:            nodeIDFromSurface(surfaceID),
						AppliedSurfaceIDs: []string{surfaceID},
						Output:            &atrace.Snapshot{Text: "trace output for " + evalCase.EvalCaseID},
						Error:             stepError,
					},
				},
			}
		}
	}
}

func numberState(state map[string]any, key string) float64 {
	switch value := state[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	default:
		return 0
	}
}

func stringState(state map[string]any, key string) string {
	value, _ := state[key].(string)
	return value
}

func statusState(state map[string]any, key string, score float64, threshold float64) status.EvalStatus {
	switch strings.ToLower(stringState(state, key)) {
	case string(status.EvalStatusPassed):
		return status.EvalStatusPassed
	case string(status.EvalStatusFailed):
		return status.EvalStatusFailed
	default:
		if threshold == 0 {
			threshold = 1
		}
		if score >= threshold {
			return status.EvalStatusPassed
		}
		return status.EvalStatusFailed
	}
}
