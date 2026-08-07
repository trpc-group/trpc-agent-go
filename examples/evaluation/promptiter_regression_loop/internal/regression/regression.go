//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"fmt"
	"sort"
	"strings"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

type AttributionCategory string

const (
	attributionFinalResponse AttributionCategory = "final_response_mismatch"
	attributionToolCall      AttributionCategory = "tool_call_error"
	attributionToolArgument  AttributionCategory = "tool_argument_error"
	attributionRouting       AttributionCategory = "routing_error"
	attributionFormat        AttributionCategory = "format_error"
	attributionKnowledge     AttributionCategory = "knowledge_recall"
	attributionExecution     AttributionCategory = "execution_error"
	attributionUnknown       AttributionCategory = "unknown"
)

type GateConfig struct {
	MinScoreGain        float64  `json:"minScoreGain"`
	MaxNewFailures      int      `json:"maxNewFailures"`
	MaxScoreRegressions int      `json:"maxScoreRegressions"`
	CriticalCaseIDs     []string `json:"criticalCaseIds"`
	MaxModelCalls       int      `json:"maxModelCalls"`
	MaxToolCalls        int      `json:"maxToolCalls"`
	MaxTokens           int      `json:"maxTokens"`
}

type UsageSummary struct {
	ModelCalls int `json:"modelCalls"`
	ToolCalls  int `json:"toolCalls"`
	Tokens     int `json:"tokens"`
}

type MetricDelta struct {
	Name            string  `json:"name"`
	BaselineScore   float64 `json:"baselineScore"`
	CandidateScore  float64 `json:"candidateScore"`
	Delta           float64 `json:"delta"`
	BaselinePassed  bool    `json:"baselinePassed"`
	CandidatePassed bool    `json:"candidatePassed"`
	Reason          string  `json:"reason,omitempty"`
}

type CaseDelta struct {
	EvalSetID       string                `json:"evalSetId"`
	CaseID          string                `json:"caseId"`
	BaselinePassed  bool                  `json:"baselinePassed"`
	CandidatePassed bool                  `json:"candidatePassed"`
	Outcome         string                `json:"outcome"`
	Metrics         []MetricDelta         `json:"metrics"`
	Attributions    []AttributionCategory `json:"attributions,omitempty"`
}

type DeltaSummary struct {
	BaselineScore  float64     `json:"baselineScore"`
	CandidateScore float64     `json:"candidateScore"`
	ScoreDelta     float64     `json:"scoreDelta"`
	NewPasses      int         `json:"newPasses"`
	NewFailures    int         `json:"newFailures"`
	Regressions    int         `json:"scoreRegressions"`
	Cases          []CaseDelta `json:"cases"`
}

type GateDecision struct {
	Accepted bool     `json:"accepted"`
	Reasons  []string `json:"reasons"`
}

// ValidateGateConfig rejects ambiguous or impossible gate settings.
func ValidateGateConfig(cfg GateConfig) error {
	if cfg.MinScoreGain < 0 {
		return fmt.Errorf("minimum score gain must not be negative")
	}
	if cfg.MaxNewFailures < 0 || cfg.MaxScoreRegressions < 0 {
		return fmt.Errorf("failure and regression limits must not be negative")
	}
	if cfg.MaxModelCalls < 0 || cfg.MaxToolCalls < 0 || cfg.MaxTokens < 0 {
		return fmt.Errorf("usage limits must not be negative")
	}
	seen := make(map[string]struct{}, len(cfg.CriticalCaseIDs))
	for _, id := range cfg.CriticalCaseIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			return fmt.Errorf("critical case id must not be empty")
		}
		if id != trimmed {
			return fmt.Errorf("critical case id %q must not have leading or trailing whitespace", id)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate critical case id %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func CompareEvaluations(baseline, candidate *promptiterengine.EvaluationResult) (DeltaSummary, error) {
	if baseline == nil || candidate == nil {
		return DeltaSummary{}, fmt.Errorf("baseline and candidate evaluations are required")
	}
	baseCases, err := indexCases(baseline)
	if err != nil {
		return DeltaSummary{}, fmt.Errorf("invalid baseline evaluation: %w", err)
	}
	candidateCases, err := indexCases(candidate)
	if err != nil {
		return DeltaSummary{}, fmt.Errorf("invalid candidate evaluation: %w", err)
	}
	if len(baseCases) == 0 {
		return DeltaSummary{}, fmt.Errorf("baseline evaluation has no cases")
	}
	if len(baseCases) != len(candidateCases) {
		return DeltaSummary{}, fmt.Errorf("evaluation case count changed from %d to %d", len(baseCases), len(candidateCases))
	}
	keys := make([]string, 0, len(baseCases))
	for key := range baseCases {
		if _, ok := candidateCases[key]; !ok {
			return DeltaSummary{}, fmt.Errorf("candidate evaluation is missing case %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := DeltaSummary{
		BaselineScore:  baseline.OverallScore,
		CandidateScore: candidate.OverallScore,
		ScoreDelta:     candidate.OverallScore - baseline.OverallScore,
	}
	for _, key := range keys {
		baseCase := baseCases[key]
		candidateCase := candidateCases[key]
		change, err := compareCases(baseCase, candidateCase)
		if err != nil {
			return DeltaSummary{}, err
		}
		switch change.Outcome {
		case "new_pass":
			result.NewPasses++
		case "new_failure":
			result.NewFailures++
		}
		for _, metric := range change.Metrics {
			if metric.Delta < 0 {
				result.Regressions++
			}
		}
		result.Cases = append(result.Cases, change)
	}
	return result, nil
}

func indexCases(result *promptiterengine.EvaluationResult) (map[string]promptiterengine.CaseResult, error) {
	indexed := make(map[string]promptiterengine.CaseResult)
	for _, evalSet := range result.EvalSets {
		if strings.TrimSpace(evalSet.EvalSetID) == "" {
			return nil, fmt.Errorf("eval set id is empty")
		}
		for _, evalCase := range evalSet.Cases {
			if strings.TrimSpace(evalCase.EvalCaseID) == "" {
				return nil, fmt.Errorf("case id is empty in eval set %q", evalSet.EvalSetID)
			}
			key := evalSet.EvalSetID + "/" + evalCase.EvalCaseID
			if _, exists := indexed[key]; exists {
				return nil, fmt.Errorf("duplicate case %q", key)
			}
			indexed[key] = evalCase
		}
	}
	return indexed, nil
}

func compareCases(baseline, candidate promptiterengine.CaseResult) (CaseDelta, error) {
	baseMetrics, err := indexMetrics(baseline.Metrics)
	if err != nil {
		return CaseDelta{}, fmt.Errorf("invalid baseline case %q: %w", baseline.EvalCaseID, err)
	}
	candidateMetrics, err := indexMetrics(candidate.Metrics)
	if err != nil {
		return CaseDelta{}, fmt.Errorf("invalid candidate case %q: %w", candidate.EvalCaseID, err)
	}
	if len(baseMetrics) == 0 {
		return CaseDelta{}, fmt.Errorf("case %q has no metrics", baseline.EvalCaseID)
	}
	if len(baseMetrics) != len(candidateMetrics) {
		return CaseDelta{}, fmt.Errorf("metric count changed for case %q", baseline.EvalCaseID)
	}
	result := CaseDelta{EvalSetID: baseline.EvalSetID, CaseID: baseline.EvalCaseID}
	names := make([]string, 0, len(baseMetrics))
	for name := range baseMetrics {
		if _, ok := candidateMetrics[name]; !ok {
			return CaseDelta{}, fmt.Errorf("candidate case %q is missing metric %q", baseline.EvalCaseID, name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	result.BaselinePassed = true
	result.CandidatePassed = true
	for _, name := range names {
		baseMetric := baseMetrics[name]
		candidateMetric := candidateMetrics[name]
		basePassed := baseMetric.Status == status.EvalStatusPassed
		candidatePassed := candidateMetric.Status == status.EvalStatusPassed
		result.BaselinePassed = result.BaselinePassed && basePassed
		result.CandidatePassed = result.CandidatePassed && candidatePassed
		result.Metrics = append(result.Metrics, MetricDelta{
			Name: name, BaselineScore: baseMetric.Score, CandidateScore: candidateMetric.Score,
			Delta: candidateMetric.Score - baseMetric.Score, BaselinePassed: basePassed,
			CandidatePassed: candidatePassed, Reason: candidateMetric.Reason,
		})
	}
	switch {
	case !result.BaselinePassed && result.CandidatePassed:
		result.Outcome = "new_pass"
	case result.BaselinePassed && !result.CandidatePassed:
		result.Outcome = "new_failure"
	default:
		result.Outcome = "unchanged"
	}
	if !result.CandidatePassed {
		result.Attributions = AttributeFailure(candidate)
	}
	return result, nil
}

func indexMetrics(metrics []promptiterengine.MetricResult) (map[string]promptiterengine.MetricResult, error) {
	indexed := make(map[string]promptiterengine.MetricResult, len(metrics))
	for _, metric := range metrics {
		if strings.TrimSpace(metric.MetricName) == "" {
			return nil, fmt.Errorf("metric name is empty")
		}
		if _, exists := indexed[metric.MetricName]; exists {
			return nil, fmt.Errorf("duplicate metric %q", metric.MetricName)
		}
		indexed[metric.MetricName] = metric
	}
	return indexed, nil
}

func AttributeFailure(evalCase promptiterengine.CaseResult) []AttributionCategory {
	seen := make(map[AttributionCategory]struct{})
	add := func(category AttributionCategory) { seen[category] = struct{}{} }
	for _, metric := range evalCase.Metrics {
		if metric.Status != status.EvalStatusFailed {
			continue
		}
		text := strings.ToLower(metric.MetricName + " " + metric.Reason)
		switch {
		case containsAny(text, "tool argument", "tool parameter", "argument", "parameter"):
			add(attributionToolArgument)
		case containsAny(text, "tool", "trajectory"):
			add(attributionToolCall)
		case containsAny(text, "route", "router", "routing"):
			add(attributionRouting)
		case containsAny(text, "format", "json", "schema", "structure"):
			add(attributionFormat)
		case containsAny(text, "knowledge", "recall", "grounding"):
			add(attributionKnowledge)
		case containsAny(text, "response", "answer", "rouge"):
			add(attributionFinalResponse)
		default:
			add(attributionUnknown)
		}
	}
	if evalCase.Trace != nil {
		if evalCase.Trace.Status != atrace.TraceStatusCompleted {
			add(attributionExecution)
		}
		for _, step := range evalCase.Trace.Steps {
			if step.Error != "" {
				if step.NodeType == "tool" {
					add(attributionToolCall)
				} else {
					add(attributionExecution)
				}
			}
		}
	}
	result := make([]AttributionCategory, 0, len(seen))
	for category := range seen {
		result = append(result, category)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func DecideGate(cfg GateConfig, delta DeltaSummary, usage UsageSummary) GateDecision {
	decision := GateDecision{Accepted: true}
	reject := func(reason string) {
		decision.Accepted = false
		decision.Reasons = append(decision.Reasons, reason)
	}
	if delta.ScoreDelta < cfg.MinScoreGain {
		reject(fmt.Sprintf("score gain %.4f is below minimum %.4f", delta.ScoreDelta, cfg.MinScoreGain))
	}
	if delta.NewFailures > cfg.MaxNewFailures {
		reject(fmt.Sprintf("new failures %d exceed limit %d", delta.NewFailures, cfg.MaxNewFailures))
	}
	if delta.Regressions > cfg.MaxScoreRegressions {
		reject(fmt.Sprintf("metric regressions %d exceed limit %d", delta.Regressions, cfg.MaxScoreRegressions))
	}
	critical := make(map[string]struct{}, len(cfg.CriticalCaseIDs))
	for _, id := range cfg.CriticalCaseIDs {
		critical[id] = struct{}{}
	}
	for _, evalCase := range delta.Cases {
		if _, ok := critical[evalCase.CaseID]; ok && evalCase.BaselinePassed && !evalCase.CandidatePassed {
			reject(fmt.Sprintf("critical case %q regressed", evalCase.CaseID))
		}
	}
	if cfg.MaxModelCalls > 0 && usage.ModelCalls > cfg.MaxModelCalls {
		reject("model call budget exceeded")
	}
	if cfg.MaxToolCalls > 0 && usage.ToolCalls > cfg.MaxToolCalls {
		reject("tool call budget exceeded")
	}
	if cfg.MaxTokens > 0 && usage.Tokens > cfg.MaxTokens {
		reject("token budget exceeded")
	}
	if decision.Accepted {
		decision.Reasons = []string{"all acceptance gates passed"}
	}
	return decision
}
