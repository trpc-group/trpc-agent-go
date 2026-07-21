//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

const (
	deltaNewPass       = "new_pass"
	deltaNewFail       = "new_fail"
	deltaImproved      = "improved"
	deltaRegressed     = "regressed"
	deltaUnchangedPass = "unchanged_pass"
	deltaUnchangedFail = "unchanged_fail"

	gateDecisionAccept = "accept"
	gateDecisionReject = "reject"

	attributionToolNotCalled         = "tool_not_called"
	attributionWrongToolName         = "wrong_tool_name"
	attributionToolArgumentsMismatch = "tool_arguments_mismatch"
	attributionRouteError            = "route_error"
	attributionFormatError           = "format_error"
	attributionKnowledgeInsufficient = "knowledge_insufficient"
	attributionFinalResponseMismatch = "final_response_mismatch"
	attributionMetricFailure         = "metric_failure"
)

var toolCountReasonPattern = regexp.MustCompile(`actual\((\d+)\)\s*!=\s*expected\((\d+)\)`)

type finalGateConfig struct {
	MinValidationGain          float64
	MaxDurationMs              int64
	MaxModelCalls              int
	CriticalCaseIDs            []string
	RejectOnNewHardFail        bool
	RejectOnCriticalRegression bool
}

type gateReportOptions struct {
	LatencyCheckSkippedReason string
}

type ValidationDelta struct {
	PerCase []CaseDelta  `json:"perCase"`
	Summary DeltaSummary `json:"summary"`
}

type CaseDelta struct {
	EvalSetID          string  `json:"evalSetId"`
	EvalCaseID         string  `json:"evalCaseId"`
	BaselineScore      float64 `json:"baselineScore"`
	CandidateScore     float64 `json:"candidateScore"`
	ScoreDelta         float64 `json:"scoreDelta"`
	BaselinePassed     bool    `json:"baselinePassed"`
	CandidatePassed    bool    `json:"candidatePassed"`
	Category           string  `json:"category"`
	NewHardFail        bool    `json:"newHardFail"`
	CriticalRegression bool    `json:"criticalRegression"`
}

type DeltaSummary struct {
	NewPass       int `json:"newPass"`
	NewFail       int `json:"newFail"`
	Improved      int `json:"improved"`
	Regressed     int `json:"regressed"`
	UnchangedPass int `json:"unchangedPass"`
	UnchangedFail int `json:"unchangedFail"`
}

type CostSummary struct {
	TotalUSD float64 `json:"totalUsd"`
}

type GateReport struct {
	Decision                   string   `json:"decision"`
	Reasons                    []string `json:"reasons"`
	ValidationGain             float64  `json:"validationGain"`
	MinValidationGain          float64  `json:"minValidationGain"`
	MaxDurationMs              int64    `json:"maxDurationMs"`
	LatencyMs                  int64    `json:"latencyMs"`
	MaxModelCalls              int      `json:"maxModelCalls"`
	ModelCallCount             int      `json:"modelCallCount"`
	CriticalCaseIDs            []string `json:"criticalCaseIds"`
	RejectOnNewHardFail        bool     `json:"rejectOnNewHardFail"`
	RejectOnCriticalRegression bool     `json:"rejectOnCriticalRegression"`
	NewHardFails               []string `json:"newHardFails"`
	CriticalRegressions        []string `json:"criticalRegressions"`
	CostCheckSkippedReason     string   `json:"costCheckSkippedReason"`
}

type FailureAttribution struct {
	PerFailedCase []FailedCaseAttribution `json:"perFailedCase"`
	Summary       AttributionSummary      `json:"summary"`
}

type FailedCaseAttribution struct {
	EvalSetID         string                 `json:"evalSetId"`
	EvalCaseID        string                 `json:"evalCaseId"`
	Category          string                 `json:"category"`
	FailedMetrics     []FailedMetricEvidence `json:"failedMetrics"`
	Evidence          []string               `json:"evidence"`
	TerminalStep      *TraceStepSummary      `json:"terminalStep,omitempty"`
	AppliedSurfaceIDs []string               `json:"appliedSurfaceIds,omitempty"`
}

type FailedMetricEvidence struct {
	MetricName string  `json:"metricName"`
	Score      float64 `json:"score"`
	Status     string  `json:"status"`
	Reason     string  `json:"reason"`
}

type TraceStepSummary struct {
	StepID            string   `json:"stepId,omitempty"`
	AgentName         string   `json:"agentName,omitempty"`
	NodeID            string   `json:"nodeId,omitempty"`
	AppliedSurfaceIDs []string `json:"appliedSurfaceIds,omitempty"`
	Error             string   `json:"error,omitempty"`
}

type AttributionSummary struct {
	ToolNotCalled         int `json:"toolNotCalled"`
	WrongToolName         int `json:"wrongToolName"`
	ToolArgumentsMismatch int `json:"toolArgumentsMismatch"`
	RouteError            int `json:"routeError"`
	FormatError           int `json:"formatError"`
	KnowledgeInsufficient int `json:"knowledgeInsufficient"`
	FinalResponseMismatch int `json:"finalResponseMismatch"`
	MetricFailure         int `json:"metricFailure"`
}

type caseKey struct {
	evalSetID  string
	evalCaseID string
}

type caseEntry struct {
	key  caseKey
	item CaseSummary
}

func defaultFinalGateConfig() finalGateConfig {
	return finalGateConfig{
		MinValidationGain:          0.05,
		MaxDurationMs:              180000,
		CriticalCaseIDs:            []string{"validation_status_tr789"},
		RejectOnNewHardFail:        true,
		RejectOnCriticalRegression: true,
	}
}

func (cfg *finalGateFileConfig) resolved() finalGateConfig {
	resolved := defaultFinalGateConfig()
	if cfg == nil {
		return resolved
	}
	if cfg.MinValidationGain != nil {
		resolved.MinValidationGain = *cfg.MinValidationGain
	}
	if cfg.MaxDurationMs != nil {
		resolved.MaxDurationMs = *cfg.MaxDurationMs
	}
	if cfg.MaxModelCalls != nil {
		resolved.MaxModelCalls = *cfg.MaxModelCalls
	}
	if cfg.CriticalCaseIDs != nil {
		resolved.CriticalCaseIDs = append([]string(nil), cfg.CriticalCaseIDs...)
	}
	if cfg.RejectOnNewHardFail != nil {
		resolved.RejectOnNewHardFail = *cfg.RejectOnNewHardFail
	}
	if cfg.RejectOnCriticalRegression != nil {
		resolved.RejectOnCriticalRegression = *cfg.RejectOnCriticalRegression
	}
	return resolved
}

func buildValidationDelta(baseline, candidate *EvaluationSummary) (*ValidationDelta, error) {
	baselineOrder, baselineCases, err := flattenCaseSummaries(baseline)
	if err != nil {
		return nil, fmt.Errorf("flatten baseline validation: %w", err)
	}
	_, candidateCases, err := flattenCaseSummaries(candidate)
	if err != nil {
		return nil, fmt.Errorf("flatten candidate validation: %w", err)
	}
	if len(baselineCases) != len(candidateCases) {
		return nil, fmt.Errorf("baseline case count %d does not match candidate case count %d", len(baselineCases), len(candidateCases))
	}
	delta := &ValidationDelta{
		PerCase: make([]CaseDelta, 0, len(baselineOrder)),
	}
	for _, entry := range baselineOrder {
		candidateCase, ok := candidateCases[entry.key]
		if !ok {
			return nil, fmt.Errorf("candidate validation missing case %q in eval set %q", entry.key.evalCaseID, entry.key.evalSetID)
		}
		baselineScore, baselinePassed := caseScoreAndPassed(entry.item)
		candidateScore, candidatePassed := caseScoreAndPassed(candidateCase)
		caseDelta := CaseDelta{
			EvalSetID:       entry.key.evalSetID,
			EvalCaseID:      entry.key.evalCaseID,
			BaselineScore:   baselineScore,
			CandidateScore:  candidateScore,
			ScoreDelta:      candidateScore - baselineScore,
			BaselinePassed:  baselinePassed,
			CandidatePassed: candidatePassed,
			Category:        classifyDelta(baselineScore, baselinePassed, candidateScore, candidatePassed),
			NewHardFail:     !isHardFail(baselineScore, baselinePassed) && isHardFail(candidateScore, candidatePassed),
		}
		delta.PerCase = append(delta.PerCase, caseDelta)
		delta.Summary.add(caseDelta.Category)
		delete(candidateCases, entry.key)
	}
	if len(candidateCases) > 0 {
		keys := make([]string, 0, len(candidateCases))
		for key := range candidateCases {
			keys = append(keys, key.evalSetID+"/"+key.evalCaseID)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("candidate validation contains extra cases: %v", keys)
	}
	return delta, nil
}

func buildGateReport(
	baseline, candidate *EvaluationSummary,
	delta *ValidationDelta,
	cfg finalGateConfig,
	latencyMs int64,
	modelCallCount int,
	mode string,
	options ...gateReportOptions,
) (*GateReport, error) {
	if baseline == nil || candidate == nil {
		return nil, errors.New("baseline and candidate validation summaries are required")
	}
	if delta == nil {
		return nil, errors.New("validation delta is nil")
	}
	var opts gateReportOptions
	if len(options) > 0 {
		opts = options[0]
	}
	criticalSet := make(map[string]struct{}, len(cfg.CriticalCaseIDs))
	for _, id := range cfg.CriticalCaseIDs {
		criticalSet[id] = struct{}{}
	}
	foundCritical := make(map[string]struct{}, len(criticalSet))
	report := &GateReport{
		Decision:                   gateDecisionAccept,
		Reasons:                    []string{},
		ValidationGain:             candidate.OverallScore - baseline.OverallScore,
		MinValidationGain:          cfg.MinValidationGain,
		MaxDurationMs:              cfg.MaxDurationMs,
		LatencyMs:                  latencyMs,
		MaxModelCalls:              cfg.MaxModelCalls,
		ModelCallCount:             modelCallCount,
		CriticalCaseIDs:            append([]string(nil), cfg.CriticalCaseIDs...),
		RejectOnNewHardFail:        cfg.RejectOnNewHardFail,
		RejectOnCriticalRegression: cfg.RejectOnCriticalRegression,
		NewHardFails:               []string{},
		CriticalRegressions:        []string{},
		CostCheckSkippedReason:     "cost check skipped (fake mode)",
	}
	reject := false
	if report.ValidationGain+epsilon() < cfg.MinValidationGain {
		reject = true
		report.Reasons = append(report.Reasons, fmt.Sprintf("validation gain %.4f is below minimum %.4f", report.ValidationGain, cfg.MinValidationGain))
	} else {
		report.Reasons = append(report.Reasons, fmt.Sprintf("validation gain %.4f satisfies minimum %.4f", report.ValidationGain, cfg.MinValidationGain))
	}
	for i := range delta.PerCase {
		caseDelta := &delta.PerCase[i]
		if caseDelta.NewHardFail {
			report.NewHardFails = append(report.NewHardFails, caseDelta.EvalCaseID)
		}
		if _, ok := criticalSet[caseDelta.EvalCaseID]; ok {
			foundCritical[caseDelta.EvalCaseID] = struct{}{}
			scoreRegressed := caseDelta.CandidateScore+epsilon() < caseDelta.BaselineScore
			statusRegressed := caseDelta.Category == deltaNewFail
			if scoreRegressed || statusRegressed {
				caseDelta.CriticalRegression = true
				report.CriticalRegressions = append(report.CriticalRegressions, caseDelta.EvalCaseID)
			}
		}
	}
	for _, id := range cfg.CriticalCaseIDs {
		if _, ok := foundCritical[id]; !ok {
			return nil, fmt.Errorf("critical case %q not found in validation delta", id)
		}
	}
	if len(report.NewHardFails) == 0 {
		report.Reasons = append(report.Reasons, "no new hard fail")
	} else if cfg.RejectOnNewHardFail {
		reject = true
		report.Reasons = append(report.Reasons, fmt.Sprintf("new hard fail cases: %v", report.NewHardFails))
	} else {
		report.Reasons = append(report.Reasons, fmt.Sprintf("new hard fail cases detected but not enforced: %v", report.NewHardFails))
	}
	if len(report.CriticalRegressions) == 0 {
		report.Reasons = append(report.Reasons, "no critical regression")
	} else if cfg.RejectOnCriticalRegression {
		reject = true
		report.Reasons = append(report.Reasons, fmt.Sprintf("critical regression cases: %v", report.CriticalRegressions))
	} else {
		report.Reasons = append(report.Reasons, fmt.Sprintf("critical regression cases detected but not enforced: %v", report.CriticalRegressions))
	}
	if opts.LatencyCheckSkippedReason != "" {
		report.Reasons = append(report.Reasons, opts.LatencyCheckSkippedReason)
	} else if cfg.MaxDurationMs > 0 {
		if latencyMs > cfg.MaxDurationMs {
			reject = true
			report.Reasons = append(report.Reasons, fmt.Sprintf("optimization latency %dms exceeds maximum %dms", latencyMs, cfg.MaxDurationMs))
		} else {
			report.Reasons = append(report.Reasons, fmt.Sprintf("optimization latency %dms is within maximum %dms", latencyMs, cfg.MaxDurationMs))
		}
	} else {
		report.Reasons = append(report.Reasons, "latency budget check skipped")
	}
	if cfg.MaxModelCalls > 0 && modelCallCount > cfg.MaxModelCalls {
		reject = true
		report.Reasons = append(report.Reasons, fmt.Sprintf("model calls %d exceeds maximum %d", modelCallCount, cfg.MaxModelCalls))
	} else if cfg.MaxModelCalls > 0 {
		report.Reasons = append(report.Reasons, fmt.Sprintf("model calls %d is within maximum %d", modelCallCount, cfg.MaxModelCalls))
	} else {
		report.Reasons = append(report.Reasons, "model call budget check skipped")
	}
	if mode == fakeMode {
		report.Reasons = append(report.Reasons, report.CostCheckSkippedReason)
	}
	if reject {
		report.Decision = gateDecisionReject
	}
	return report, nil
}

func buildFailureAttribution(result *promptiterengine.EvaluationResult) (*FailureAttribution, error) {
	if result == nil {
		return nil, errors.New("evaluation result is nil")
	}
	attribution := &FailureAttribution{
		PerFailedCase: []FailedCaseAttribution{},
	}
	for _, evalSet := range result.EvalSets {
		for _, evalCase := range evalSet.Cases {
			failedMetrics, err := failedMetricEvidence(evalCase.Metrics)
			if err != nil {
				return nil, fmt.Errorf("build attribution for case %q: %w", evalCase.EvalCaseID, err)
			}
			if len(failedMetrics) == 0 {
				continue
			}
			category := classifyFailureAttribution(failedMetrics)
			terminalStep := summarizeTerminalStep(evalCase.Trace)
			caseAttribution := FailedCaseAttribution{
				EvalSetID:         evalSet.EvalSetID,
				EvalCaseID:        evalCase.EvalCaseID,
				Category:          category,
				FailedMetrics:     failedMetrics,
				Evidence:          attributionEvidence(failedMetrics),
				TerminalStep:      terminalStep,
				AppliedSurfaceIDs: appliedSurfaceIDs(terminalStep),
			}
			attribution.PerFailedCase = append(attribution.PerFailedCase, caseAttribution)
			attribution.Summary.add(category)
		}
	}
	return attribution, nil
}

func failedMetricEvidence(metrics []promptiterengine.MetricResult) ([]FailedMetricEvidence, error) {
	failed := make([]FailedMetricEvidence, 0, len(metrics))
	for _, metric := range metrics {
		if metric.Status != status.EvalStatusFailed {
			continue
		}
		reason := strings.TrimSpace(metric.Reason)
		if reason == "" {
			return nil, fmt.Errorf("failed metric %q is missing reason", metric.MetricName)
		}
		failed = append(failed, FailedMetricEvidence{
			MetricName: metric.MetricName,
			Score:      metric.Score,
			Status:     string(metric.Status),
			Reason:     reason,
		})
	}
	return failed, nil
}

func classifyFailureAttribution(metrics []FailedMetricEvidence) string {
	if containsToolCount(metrics, 0, 1) {
		return attributionToolNotCalled
	}
	if containsReason(metrics, "name mismatch", "wrong tool name") {
		return attributionWrongToolName
	}
	if containsReason(metrics, "arguments mismatch", "argument mismatch") {
		return attributionToolArgumentsMismatch
	}
	if containsRouteError(metrics) {
		return attributionRouteError
	}
	if containsReason(metrics, "json mismatch", "xml mismatch", "format") {
		return attributionFormatError
	}
	if containsReason(metrics, "knowledge", "insufficient", "missing evidence") {
		return attributionKnowledgeInsufficient
	}
	for _, metric := range metrics {
		if metric.MetricName == "final_response_avg_score" {
			return attributionFinalResponseMismatch
		}
	}
	return attributionMetricFailure
}

func containsToolCount(metrics []FailedMetricEvidence, actualWant, expectedMin int) bool {
	for _, metric := range metrics {
		actual, expected, ok := parseToolCounts(metric.Reason)
		if !ok {
			continue
		}
		if actual == actualWant && expected >= expectedMin {
			return true
		}
	}
	return false
}

func containsRouteError(metrics []FailedMetricEvidence) bool {
	for _, metric := range metrics {
		actual, expected, ok := parseToolCounts(metric.Reason)
		if ok && actual > 0 && expected == 0 {
			return true
		}
	}
	return false
}

func parseToolCounts(reason string) (int, int, bool) {
	matches := toolCountReasonPattern.FindStringSubmatch(reason)
	if len(matches) != 3 {
		return 0, 0, false
	}
	actual, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, 0, false
	}
	expected, err := strconv.Atoi(matches[2])
	if err != nil {
		return 0, 0, false
	}
	return actual, expected, true
}

func containsReason(metrics []FailedMetricEvidence, needles ...string) bool {
	for _, metric := range metrics {
		reason := strings.ToLower(metric.Reason)
		for _, needle := range needles {
			if strings.Contains(reason, needle) {
				return true
			}
		}
	}
	return false
}

func attributionEvidence(metrics []FailedMetricEvidence) []string {
	evidence := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		evidence = append(evidence, fmt.Sprintf("%s failed: %s", metric.MetricName, metric.Reason))
	}
	return evidence
}

func summarizeTerminalStep(trace *atrace.Trace) *TraceStepSummary {
	if trace == nil || len(trace.Steps) == 0 {
		return nil
	}
	step := trace.Steps[len(trace.Steps)-1]
	return &TraceStepSummary{
		StepID:            step.StepID,
		AgentName:         step.AgentName,
		NodeID:            step.NodeID,
		AppliedSurfaceIDs: append([]string(nil), step.AppliedSurfaceIDs...),
		Error:             step.Error,
	}
}

func appliedSurfaceIDs(step *TraceStepSummary) []string {
	if step == nil {
		return []string{}
	}
	return append([]string(nil), step.AppliedSurfaceIDs...)
}

func flattenCaseSummaries(summary *EvaluationSummary) ([]caseEntry, map[caseKey]CaseSummary, error) {
	if summary == nil {
		return nil, nil, errors.New("evaluation summary is nil")
	}
	order := []caseEntry{}
	index := map[caseKey]CaseSummary{}
	for _, evalSet := range summary.EvalSets {
		if evalSet.EvalSetID == "" {
			return nil, nil, errors.New("eval set id is empty")
		}
		for _, evalCase := range evalSet.Cases {
			if evalCase.EvalCaseID == "" {
				return nil, nil, errors.New("eval case id is empty")
			}
			key := caseKey{evalSetID: evalSet.EvalSetID, evalCaseID: evalCase.EvalCaseID}
			if _, ok := index[key]; ok {
				return nil, nil, fmt.Errorf("duplicate eval case %q in eval set %q", evalCase.EvalCaseID, evalSet.EvalSetID)
			}
			index[key] = evalCase
			order = append(order, caseEntry{key: key, item: evalCase})
		}
	}
	return order, index, nil
}

func caseScoreAndPassed(evalCase CaseSummary) (float64, bool) {
	if len(evalCase.Metrics) == 0 {
		return 0, false
	}
	total := 0.0
	passed := true
	for _, metric := range evalCase.Metrics {
		total += metric.Score
		if metric.Status != "passed" {
			passed = false
		}
	}
	return total / float64(len(evalCase.Metrics)), passed
}

func isHardFail(score float64, passed bool) bool {
	return !passed && math.Abs(score) <= epsilon()
}

func classifyDelta(
	baselineScore float64,
	baselinePassed bool,
	candidateScore float64,
	candidatePassed bool,
) string {
	switch {
	case !baselinePassed && candidatePassed:
		return deltaNewPass
	case baselinePassed && !candidatePassed:
		return deltaNewFail
	case candidateScore > baselineScore+epsilon():
		return deltaImproved
	case candidateScore+epsilon() < baselineScore:
		return deltaRegressed
	case baselinePassed && candidatePassed:
		return deltaUnchangedPass
	default:
		return deltaUnchangedFail
	}
}

func (summary *DeltaSummary) add(category string) {
	switch category {
	case deltaNewPass:
		summary.NewPass++
	case deltaNewFail:
		summary.NewFail++
	case deltaImproved:
		summary.Improved++
	case deltaRegressed:
		summary.Regressed++
	case deltaUnchangedPass:
		summary.UnchangedPass++
	case deltaUnchangedFail:
		summary.UnchangedFail++
	}
}

func (summary *AttributionSummary) add(category string) {
	switch category {
	case attributionToolNotCalled:
		summary.ToolNotCalled++
	case attributionWrongToolName:
		summary.WrongToolName++
	case attributionToolArgumentsMismatch:
		summary.ToolArgumentsMismatch++
	case attributionRouteError:
		summary.RouteError++
	case attributionFormatError:
		summary.FormatError++
	case attributionKnowledgeInsufficient:
		summary.KnowledgeInsufficient++
	case attributionFinalResponseMismatch:
		summary.FinalResponseMismatch++
	case attributionMetricFailure:
		summary.MetricFailure++
	}
}

func epsilon() float64 {
	return 1e-9
}
