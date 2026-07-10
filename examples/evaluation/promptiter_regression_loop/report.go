//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

const (
	reportJSONName     = "optimization_report.json"
	reportMarkdownName = "optimization_report.md"
)

const (
	deltaNewPass       = "new_pass"
	deltaNewFail       = "new_fail"
	deltaImproved      = "improved"
	deltaRegressed     = "regressed"
	deltaUnchangedPass = "unchanged_pass"
	deltaUnchangedFail = "unchanged_fail"
)

const (
	attributionToolNotCalled         = "tool_not_called"
	attributionWrongToolName         = "wrong_tool_name"
	attributionToolArgumentsMismatch = "tool_arguments_mismatch"
	attributionFinalResponseMismatch = "final_response_mismatch"
	attributionRouteError            = "route_error"
	attributionFormatError           = "format_error"
	attributionKnowledgeInsufficient = "knowledge_insufficient"
	attributionMetricFailure         = "metric_failure"
)

var attributionCategories = []string{
	attributionToolNotCalled,
	attributionWrongToolName,
	attributionToolArgumentsMismatch,
	attributionFinalResponseMismatch,
	attributionRouteError,
	attributionFormatError,
	attributionKnowledgeInsufficient,
	attributionMetricFailure,
}

var remainingPhasePending []string

type OptimizationReport struct {
	Mode                    string            `json:"mode"`
	Seed                    int64             `json:"seed"`
	TargetSurfaces          []string          `json:"targetSurfaces"`
	PromptSource            string            `json:"promptSource"`
	PromptHash              string            `json:"promptHash"`
	PromptSummary           string            `json:"promptSummary"`
	BaselineToolDescription string            `json:"baselineToolDescription"`
	Baseline                BaselineReport    `json:"baseline"`
	Candidate               CandidateReport   `json:"candidate"`
	Rounds                  []RoundReport     `json:"rounds"`
	Delta                   ValidationDelta   `json:"delta"`
	Attribution             AttributionReport `json:"attribution"`
	Gate                    GateReport        `json:"gate"`
	TraceSmoke              TraceSmokeReport  `json:"traceSmoke"`
	Phase1Pending           []string          `json:"phase1Pending,omitempty"`
	Cost                    CostSummary       `json:"cost"`
	LatencyMs               int64             `json:"latencyMs"`
}

type BaselineReport struct {
	Train      *EvaluationSummary `json:"train"`
	Validation *EvaluationSummary `json:"validation"`
}

type CandidateReport struct {
	Train           *EvaluationSummary  `json:"train"`
	Validation      *EvaluationSummary  `json:"validation"`
	AcceptedProfile *promptiter.Profile `json:"acceptedProfile"`
	Accepted        bool                `json:"accepted"`
}

type RoundReport struct {
	Round         int                 `json:"round"`
	Train         *EvaluationSummary  `json:"train"`
	Validation    *EvaluationSummary  `json:"validation"`
	Patches       []PatchSummary      `json:"patches"`
	Accepted      bool                `json:"accepted"`
	ScoreDelta    float64             `json:"scoreDelta"`
	AcceptReason  string              `json:"acceptReason"`
	StopReason    string              `json:"stopReason"`
	OutputProfile *promptiter.Profile `json:"outputProfile,omitempty"`
}

type PatchSummary struct {
	SurfaceID       string `json:"surfaceId"`
	ToolID          string `json:"toolId,omitempty"`
	ToolDescription string `json:"toolDescription,omitempty"`
	Reason          string `json:"reason"`
}

type EvaluationSummary struct {
	Score    float64          `json:"score"`
	EvalSets []EvalSetSummary `json:"evalSets"`
}

type EvalSetSummary struct {
	EvalSetID string            `json:"evalSetId"`
	Score     float64           `json:"score"`
	Cases     []EvalCaseSummary `json:"cases"`
}

type EvalCaseSummary struct {
	EvalCaseID string          `json:"evalCaseId"`
	Score      float64         `json:"score"`
	Passed     bool            `json:"passed"`
	Metrics    []MetricSummary `json:"metrics"`
}

type MetricSummary struct {
	MetricName string  `json:"metricName"`
	Score      float64 `json:"score"`
	Status     string  `json:"status"`
	Reason     string  `json:"reason,omitempty"`
}

type CostSummary struct {
	TotalUSD        float64 `json:"totalUsd"`
	ModelCallCount  int     `json:"modelCallCount"`
	WorkerCallCount int     `json:"workerCallCount"`
}

type ValidationDelta struct {
	PerCase []CaseDelta  `json:"perCase"`
	Summary DeltaSummary `json:"summary"`
}

type CaseDelta struct {
	EvalSetID       string  `json:"evalSetId"`
	EvalCaseID      string  `json:"evalCaseId"`
	Classification  string  `json:"classification"`
	BaselineScore   float64 `json:"baselineScore"`
	CandidateScore  float64 `json:"candidateScore"`
	ScoreDelta      float64 `json:"scoreDelta"`
	BaselinePassed  bool    `json:"baselinePassed"`
	CandidatePassed bool    `json:"candidatePassed"`
	NewHardFail     bool    `json:"newHardFail"`
	Critical        bool    `json:"critical"`
	CriticalRegress bool    `json:"criticalRegression"`
}

type DeltaSummary struct {
	NewPass                  int     `json:"newPass"`
	NewFail                  int     `json:"newFail"`
	Improved                 int     `json:"improved"`
	Regressed                int     `json:"regressed"`
	UnchangedPass            int     `json:"unchangedPass"`
	UnchangedFail            int     `json:"unchangedFail"`
	NewHardFail              int     `json:"newHardFail"`
	CriticalRegression       int     `json:"criticalRegression"`
	BaselineValidationScore  float64 `json:"baselineValidationScore"`
	CandidateValidationScore float64 `json:"candidateValidationScore"`
	ScoreDelta               float64 `json:"scoreDelta"`
}

type GateReport struct {
	Decision    string   `json:"decision"`
	Publishable bool     `json:"publishable"`
	Reasons     []string `json:"reasons"`
}

type TraceSmokeReport struct {
	Enabled                   bool               `json:"enabled"`
	EvalSetID                 string             `json:"evalSetId,omitempty"`
	OptimizationSkipped       bool               `json:"optimizationSkipped"`
	OptimizationSkippedReason string             `json:"optimizationSkippedReason,omitempty"`
	Evaluation                *EvaluationSummary `json:"evaluation,omitempty"`
	Attribution               *AttributionReport `json:"attribution,omitempty"`
}

type AttributionReport struct {
	PerFailedCase []FailureAttribution `json:"perFailedCase"`
	Summary       map[string]int       `json:"summary"`
}

type FailureAttribution struct {
	EvalSetID         string               `json:"evalSetId"`
	EvalCaseID        string               `json:"evalCaseId"`
	Category          string               `json:"category"`
	Explanation       string               `json:"explanation"`
	FailedMetrics     []FailedMetricReason `json:"failedMetrics"`
	ActualToolNames   []string             `json:"actualToolNames,omitempty"`
	ExpectedToolNames []string             `json:"expectedToolNames,omitempty"`
	TerminalStepID    string               `json:"terminalStepId,omitempty"`
	TerminalOutput    string               `json:"terminalOutput,omitempty"`
}

type FailedMetricReason struct {
	MetricName string `json:"metricName"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

type reportInput struct {
	mode                    string
	seed                    int64
	prompt                  promptSource
	targetSurfaceIDs        []string
	baselineToolDescription string
	runResult               *promptiterengine.RunResult
	candidateTrain          *promptiterengine.EvaluationResult
	finalGate               finalGateConfig
	latency                 time.Duration
	modelCallCount          int
	workerCallCount         int
}

type traceSmokeReportInput struct {
	mode                    string
	seed                    int64
	prompt                  promptSource
	targetSurfaceIDs        []string
	baselineToolDescription string
	evaluation              *promptiterengine.EvaluationResult
	latency                 time.Duration
	modelCallCount          int
	workerCallCount         int
}

func buildOptimizationReport(input reportInput) (*OptimizationReport, error) {
	if input.runResult == nil {
		return nil, errors.New("run result is nil")
	}
	if len(input.runResult.Rounds) == 0 {
		return nil, errors.New("run result has no rounds")
	}
	if input.candidateTrain == nil {
		return nil, errors.New("candidate train evaluation is nil")
	}
	baselineTrain := input.runResult.Rounds[0].Train
	candidateValidation, accepted := acceptedValidation(input.runResult)
	baselineValidationSummary := summarizeEvaluationResult(input.runResult.BaselineValidation)
	candidateValidationSummary := summarizeEvaluationResult(candidateValidation)
	delta := buildValidationDelta(
		baselineValidationSummary,
		candidateValidationSummary,
		input.finalGate.CriticalCaseIDs,
	)
	attribution := buildFailureAttribution(candidateValidation)
	cost := CostSummary{
		TotalUSD:        0,
		ModelCallCount:  input.modelCallCount,
		WorkerCallCount: input.workerCallCount,
	}
	gate := decideFinalGate(
		input.mode,
		input.finalGate,
		delta,
		input.latency,
		cost.TotalUSD,
	)
	report := &OptimizationReport{
		Mode:                    input.mode,
		Seed:                    input.seed,
		TargetSurfaces:          append([]string(nil), input.targetSurfaceIDs...),
		PromptSource:            input.prompt.Path,
		PromptHash:              input.prompt.Hash,
		PromptSummary:           input.prompt.Summary,
		BaselineToolDescription: input.baselineToolDescription,
		Baseline: BaselineReport{
			Train:      summarizeEvaluationResult(baselineTrain),
			Validation: baselineValidationSummary,
		},
		Candidate: CandidateReport{
			Train:           summarizeEvaluationResult(input.candidateTrain),
			Validation:      candidateValidationSummary,
			AcceptedProfile: input.runResult.AcceptedProfile,
			Accepted:        accepted,
		},
		Rounds:        summarizeRounds(input.runResult.Rounds),
		Delta:         delta,
		Attribution:   attribution,
		Gate:          gate,
		TraceSmoke:    TraceSmokeReport{Enabled: false},
		Phase1Pending: append([]string(nil), remainingPhasePending...),
		Cost:          cost,
		LatencyMs:     input.latency.Milliseconds(),
	}
	return report, nil
}

func buildTraceSmokeReport(input traceSmokeReportInput) (*OptimizationReport, error) {
	if input.evaluation == nil {
		return nil, errors.New("trace smoke evaluation is nil")
	}
	evaluation := summarizeEvaluationResult(input.evaluation)
	attribution := buildFailureAttribution(input.evaluation)
	cost := CostSummary{
		TotalUSD:        0,
		ModelCallCount:  input.modelCallCount,
		WorkerCallCount: input.workerCallCount,
	}
	return &OptimizationReport{
		Mode:                    input.mode,
		Seed:                    input.seed,
		TargetSurfaces:          append([]string(nil), input.targetSurfaceIDs...),
		PromptSource:            input.prompt.Path,
		PromptHash:              input.prompt.Hash,
		PromptSummary:           input.prompt.Summary,
		BaselineToolDescription: input.baselineToolDescription,
		Rounds:                  []RoundReport{},
		Attribution:             attribution,
		Gate: GateReport{
			Decision:    "skipped",
			Publishable: false,
			Reasons:     []string{traceSmokeOptimizationSkippedReason},
		},
		TraceSmoke: TraceSmokeReport{
			Enabled:                   true,
			EvalSetID:                 traceSmokeEvalSetID,
			OptimizationSkipped:       true,
			OptimizationSkippedReason: traceSmokeOptimizationSkippedReason,
			Evaluation:                evaluation,
			Attribution:               &attribution,
		},
		Cost:      cost,
		LatencyMs: input.latency.Milliseconds(),
	}, nil
}

func acceptedValidation(result *promptiterengine.RunResult) (*promptiterengine.EvaluationResult, bool) {
	if result == nil {
		return nil, false
	}
	current := result.BaselineValidation
	accepted := false
	for _, round := range result.Rounds {
		if round.Acceptance == nil || !round.Acceptance.Accepted {
			continue
		}
		current = round.Validation
		accepted = true
	}
	return current, accepted
}

func buildValidationDelta(
	baseline *EvaluationSummary,
	candidate *EvaluationSummary,
	criticalCaseIDs []string,
) ValidationDelta {
	baselineCases := indexCases(baseline)
	candidateCases := indexCases(candidate)
	keys := make([]caseKey, 0, len(baselineCases)+len(candidateCases))
	seen := map[caseKey]struct{}{}
	for key := range baselineCases {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range candidateCases {
		if _, ok := seen[key]; ok {
			continue
		}
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].evalSetID == keys[j].evalSetID {
			return keys[i].evalCaseID < keys[j].evalCaseID
		}
		return keys[i].evalSetID < keys[j].evalSetID
	})
	out := ValidationDelta{
		PerCase: make([]CaseDelta, 0, len(keys)),
		Summary: DeltaSummary{
			BaselineValidationScore:  scoreOf(baseline),
			CandidateValidationScore: scoreOf(candidate),
			ScoreDelta:               scoreOf(candidate) - scoreOf(baseline),
		},
	}
	for _, key := range keys {
		baselineCase := baselineCases[key]
		candidateCase := candidateCases[key]
		classification := classifyDelta(baselineCase, candidateCase)
		critical := isCriticalCase(key.evalCaseID, criticalCaseIDs)
		criticalRegression := critical && isCaseRegression(baselineCase, candidateCase)
		caseDelta := CaseDelta{
			EvalSetID:       key.evalSetID,
			EvalCaseID:      key.evalCaseID,
			Classification:  classification,
			BaselineScore:   baselineCase.Score,
			CandidateScore:  candidateCase.Score,
			ScoreDelta:      candidateCase.Score - baselineCase.Score,
			BaselinePassed:  baselineCase.Passed,
			CandidatePassed: candidateCase.Passed,
			NewHardFail:     !isHardFail(baselineCase) && isHardFail(candidateCase),
			Critical:        critical,
			CriticalRegress: criticalRegression,
		}
		out.PerCase = append(out.PerCase, caseDelta)
		incrementDeltaSummary(&out.Summary, classification)
		if caseDelta.NewHardFail {
			out.Summary.NewHardFail++
		}
		if criticalRegression {
			out.Summary.CriticalRegression++
		}
	}
	return out
}

type caseKey struct {
	evalSetID  string
	evalCaseID string
}

func indexCases(summary *EvaluationSummary) map[caseKey]EvalCaseSummary {
	index := map[caseKey]EvalCaseSummary{}
	if summary == nil {
		return index
	}
	for _, evalSet := range summary.EvalSets {
		for _, evalCase := range evalSet.Cases {
			index[caseKey{
				evalSetID:  evalSet.EvalSetID,
				evalCaseID: evalCase.EvalCaseID,
			}] = evalCase
		}
	}
	return index
}

func classifyDelta(baseline EvalCaseSummary, candidate EvalCaseSummary) string {
	switch {
	case !baseline.Passed && candidate.Passed:
		return deltaNewPass
	case baseline.Passed && !candidate.Passed:
		return deltaNewFail
	case candidate.Score > baseline.Score:
		return deltaImproved
	case candidate.Score < baseline.Score:
		return deltaRegressed
	case baseline.Passed && candidate.Passed:
		return deltaUnchangedPass
	default:
		return deltaUnchangedFail
	}
}

func incrementDeltaSummary(summary *DeltaSummary, classification string) {
	switch classification {
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

func buildFailureAttribution(result *promptiterengine.EvaluationResult) AttributionReport {
	report := AttributionReport{
		PerFailedCase: []FailureAttribution{},
		Summary:       newAttributionSummary(),
	}
	if result == nil {
		return report
	}
	for _, evalSet := range result.EvalSets {
		for _, evalCase := range evalSet.Cases {
			failedMetrics := failedMetricReasons(evalCase.Metrics)
			if len(failedMetrics) == 0 {
				continue
			}
			category, terminal := classifyFailure(evalCase, failedMetrics)
			report.Summary[category]++
			report.PerFailedCase = append(report.PerFailedCase, FailureAttribution{
				EvalSetID:         evalCase.EvalSetID,
				EvalCaseID:        evalCase.EvalCaseID,
				Category:          category,
				Explanation:       attributionExplanation(category, evalCase, failedMetrics),
				FailedMetrics:     failedMetrics,
				ActualToolNames:   toolNames(evalCase.ActualInvocation),
				ExpectedToolNames: toolNames(evalCase.ExpectedInvocation),
				TerminalStepID:    terminal.StepID,
				TerminalOutput:    terminal.Text,
			})
		}
	}
	sort.SliceStable(report.PerFailedCase, func(i, j int) bool {
		if report.PerFailedCase[i].EvalSetID == report.PerFailedCase[j].EvalSetID {
			return report.PerFailedCase[i].EvalCaseID < report.PerFailedCase[j].EvalCaseID
		}
		return report.PerFailedCase[i].EvalSetID < report.PerFailedCase[j].EvalSetID
	})
	return report
}

func newAttributionSummary() map[string]int {
	out := make(map[string]int, len(attributionCategories))
	for _, category := range attributionCategories {
		out[category] = 0
	}
	return out
}

func failedMetricReasons(metrics []promptiterengine.MetricResult) []FailedMetricReason {
	out := make([]FailedMetricReason, 0, len(metrics))
	for _, metric := range metrics {
		if metric.Status == status.EvalStatusPassed {
			continue
		}
		out = append(out, FailedMetricReason{
			MetricName: metric.MetricName,
			Status:     string(metric.Status),
			Reason:     strings.TrimSpace(metric.Reason),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].MetricName < out[j].MetricName
	})
	return out
}

type terminalTraceText struct {
	StepID string
	Text   string
}

func classifyFailure(
	evalCase promptiterengine.CaseResult,
	failedMetrics []FailedMetricReason,
) (string, terminalTraceText) {
	if category, ok := classifyFromInvocations(evalCase.ActualInvocation, evalCase.ExpectedInvocation); ok {
		return category, terminalText(evalCase.Trace)
	}
	terminal := terminalText(evalCase.Trace)
	searchText := strings.ToLower(strings.Join([]string{
		metricReasonText(failedMetrics),
		terminal.Text,
	}, " "))
	for _, metric := range failedMetrics {
		metricName := strings.ToLower(metric.MetricName)
		if strings.Contains(metricName, "tool_trajectory") {
			switch {
			case strings.Contains(searchText, "actual(0)") ||
				strings.Contains(searchText, "no tool") ||
				strings.Contains(searchText, "tool not called"):
				return attributionToolNotCalled, terminal
			case strings.Contains(searchText, "name mismatch") ||
				strings.Contains(searchText, "wrong tool"):
				return attributionWrongToolName, terminal
			case strings.Contains(searchText, "arguments mismatch"):
				return attributionToolArgumentsMismatch, terminal
			}
		}
	}
	switch {
	case containsAny(searchText, "json mismatch", "xml mismatch", "schema", "parse", "invalid format", "malformed"):
		return attributionFormatError, terminal
	case traceHasRouteError(evalCase.Trace) ||
		containsAny(searchText, "route", "routing", "runner", "run error", "tool execution", "no route"):
		return attributionRouteError, terminal
	case containsAny(searchText, "not enough", "not have enough", "do not have enough", "don't have enough", "insufficient", "cannot answer", "unknown"):
		return attributionKnowledgeInsufficient, terminal
	case hasMetricName(failedMetrics, "final_response") ||
		containsAny(searchText, "final response mismatch", "text mismatch", "rouge mismatch"):
		return attributionFinalResponseMismatch, terminal
	default:
		return attributionMetricFailure, terminal
	}
}

func classifyFromInvocations(actual, expected *evalset.Invocation) (string, bool) {
	if actual == nil || expected == nil {
		return "", false
	}
	actualTools := actual.Tools
	expectedTools := expected.Tools
	switch {
	case len(expectedTools) > 0 && len(actualTools) == 0:
		return attributionToolNotCalled, true
	case len(expectedTools) == 0 && len(actualTools) > 0:
		return attributionWrongToolName, true
	case len(expectedTools) > 0 && !hasAnyExpectedToolName(actualTools, expectedTools):
		return attributionWrongToolName, true
	case len(actualTools) < len(expectedTools):
		return attributionToolNotCalled, true
	case len(actualTools) != len(expectedTools):
		return attributionWrongToolName, true
	case len(expectedTools) > 0 && !toolArgumentsMatch(actualTools, expectedTools):
		return attributionToolArgumentsMismatch, true
	case finalResponseContent(actual) != finalResponseContent(expected):
		return attributionFinalResponseMismatch, true
	default:
		return "", false
	}
}

func hasAnyExpectedToolName(actualTools, expectedTools []*evalset.Tool) bool {
	expectedNames := map[string]struct{}{}
	for _, expected := range expectedTools {
		if expected == nil {
			continue
		}
		expectedNames[expected.Name] = struct{}{}
	}
	for _, actual := range actualTools {
		if actual == nil {
			continue
		}
		if _, ok := expectedNames[actual.Name]; ok {
			return true
		}
	}
	return false
}

func toolArgumentsMatch(actualTools, expectedTools []*evalset.Tool) bool {
	used := make([]bool, len(actualTools))
	for _, expected := range expectedTools {
		if expected == nil {
			continue
		}
		matched := false
		for i, actual := range actualTools {
			if used[i] || actual == nil || actual.Name != expected.Name {
				continue
			}
			used[i] = true
			if !jsonValuesEqual(actual.Arguments, expected.Arguments) {
				return false
			}
			matched = true
			break
		}
		if !matched {
			return false
		}
	}
	return true
}

func jsonValuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	if leftErr == nil && rightErr == nil {
		return string(leftJSON) == string(rightJSON)
	}
	return reflect.DeepEqual(left, right)
}

func finalResponseContent(invocation *evalset.Invocation) string {
	if invocation == nil || invocation.FinalResponse == nil {
		return ""
	}
	return strings.TrimSpace(invocation.FinalResponse.Content)
}

func metricReasonText(metrics []FailedMetricReason) string {
	parts := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		parts = append(parts, metric.Reason)
	}
	return strings.Join(parts, " ")
}

func terminalText(trace *atrace.Trace) terminalTraceText {
	if trace == nil || len(trace.Steps) == 0 {
		return terminalTraceText{}
	}
	predecessors := map[string]struct{}{}
	for _, step := range trace.Steps {
		if step.StepID == "" {
			continue
		}
		for _, predecessor := range step.PredecessorStepIDs {
			predecessors[predecessor] = struct{}{}
		}
	}
	var terminal *atrace.Step
	for i := range trace.Steps {
		step := &trace.Steps[i]
		if step.StepID == "" {
			continue
		}
		if _, ok := predecessors[step.StepID]; ok {
			continue
		}
		if terminal == nil || step.StepID > terminal.StepID {
			terminal = step
		}
	}
	if terminal == nil {
		terminal = &trace.Steps[len(trace.Steps)-1]
	}
	textParts := []string{}
	if terminal.Output != nil {
		textParts = append(textParts, strings.TrimSpace(terminal.Output.Text))
	}
	if strings.TrimSpace(terminal.Error) != "" {
		textParts = append(textParts, strings.TrimSpace(terminal.Error))
	}
	return terminalTraceText{
		StepID: terminal.StepID,
		Text:   strings.Join(textParts, " "),
	}
}

func traceHasRouteError(trace *atrace.Trace) bool {
	if trace == nil {
		return false
	}
	if trace.Status == atrace.TraceStatusFailed {
		return true
	}
	for _, step := range trace.Steps {
		if strings.TrimSpace(step.Error) != "" {
			return true
		}
	}
	return false
}

func hasMetricName(metrics []FailedMetricReason, needle string) bool {
	for _, metric := range metrics {
		if strings.Contains(strings.ToLower(metric.MetricName), needle) {
			return true
		}
	}
	return false
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func toolNames(invocation *evalset.Invocation) []string {
	if invocation == nil || len(invocation.Tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(invocation.Tools))
	for _, tool := range invocation.Tools {
		if tool == nil {
			continue
		}
		names = append(names, tool.Name)
	}
	return names
}

func attributionExplanation(
	category string,
	evalCase promptiterengine.CaseResult,
	failedMetrics []FailedMetricReason,
) string {
	reasons := metricReasonText(failedMetrics)
	actualTools := strings.Join(toolNames(evalCase.ActualInvocation), ", ")
	expectedTools := strings.Join(toolNames(evalCase.ExpectedInvocation), ", ")
	switch category {
	case attributionToolNotCalled:
		return fmt.Sprintf("Expected tool call(s) %s, but the agent did not call a tool. %s", expectedTools, reasons)
	case attributionWrongToolName:
		if expectedTools == "" {
			return fmt.Sprintf("Expected no tool calls, but actual tool call(s) were %s. %s", actualTools, reasons)
		}
		return fmt.Sprintf("Expected tool call(s) %s, but actual tool call(s) were %s. %s", expectedTools, actualTools, reasons)
	case attributionToolArgumentsMismatch:
		return fmt.Sprintf("Tool name matched, but arguments differed from the expected invocation. %s", reasons)
	case attributionFinalResponseMismatch:
		return fmt.Sprintf("The final answer did not match the expected response. %s", reasons)
	case attributionRouteError:
		return fmt.Sprintf("The execution route or terminal trace ended with an error. %s", reasons)
	case attributionFormatError:
		return fmt.Sprintf("The response shape failed the expected format. %s", reasons)
	case attributionKnowledgeInsufficient:
		return fmt.Sprintf("The agent response indicates insufficient task knowledge or missing facts. %s", reasons)
	default:
		return fmt.Sprintf("Metric failure could not be mapped to a more specific category. %s", reasons)
	}
}

func decideFinalGate(
	mode string,
	cfg finalGateConfig,
	delta ValidationDelta,
	latency time.Duration,
	totalUSD float64,
) GateReport {
	reasons := []string{}
	publishable := true
	validationGain := delta.Summary.ScoreDelta
	if validationGain < cfg.MinValidationGain {
		publishable = false
		reasons = append(reasons, fmt.Sprintf(
			"validation gain %.3f is below minimum %.3f",
			validationGain,
			cfg.MinValidationGain,
		))
	} else {
		reasons = append(reasons, fmt.Sprintf(
			"validation gain %.3f meets minimum %.3f",
			validationGain,
			cfg.MinValidationGain,
		))
	}
	if cfg.RejectOnNewHardFail {
		if delta.Summary.NewHardFail > 0 {
			publishable = false
			reasons = append(reasons, fmt.Sprintf("new hard fail count is %d", delta.Summary.NewHardFail))
		} else {
			reasons = append(reasons, "no new hard fails")
		}
	}
	if cfg.RejectOnCriticalRegression {
		if delta.Summary.CriticalRegression > 0 {
			publishable = false
			reasons = append(reasons, fmt.Sprintf(
				"critical regression count is %d",
				delta.Summary.CriticalRegression,
			))
		} else {
			reasons = append(reasons, "critical cases did not regress")
		}
	}
	latencyMs := latency.Milliseconds()
	if latencyMs >= cfg.MaxDurationMs {
		publishable = false
		reasons = append(reasons, fmt.Sprintf("latency %dms exceeds max %dms", latencyMs, cfg.MaxDurationMs))
	} else {
		reasons = append(reasons, fmt.Sprintf("latency %dms is below max %dms", latencyMs, cfg.MaxDurationMs))
	}
	if mode == defaultMode {
		if totalUSD != 0 {
			publishable = false
			reasons = append(reasons, fmt.Sprintf("fake mode cost must be 0, got %.6f", totalUSD))
		} else {
			reasons = append(reasons, "fake mode cost is 0")
		}
	}
	decision := "reject"
	if publishable {
		decision = "publish"
	}
	return GateReport{
		Decision:    decision,
		Publishable: publishable,
		Reasons:     reasons,
	}
}

func isHardFail(evalCase EvalCaseSummary) bool {
	return !evalCase.Passed && evalCase.Score == 0
}

func isCaseRegression(baseline EvalCaseSummary, candidate EvalCaseSummary) bool {
	return candidate.Score < baseline.Score || (baseline.Passed && !candidate.Passed)
}

func isCriticalCase(evalCaseID string, criticalCaseIDs []string) bool {
	normalizedCaseID := strings.ToLower(evalCaseID)
	for _, criticalCaseID := range criticalCaseIDs {
		normalizedCriticalID := strings.ToLower(strings.TrimSpace(criticalCaseID))
		if normalizedCriticalID == "" {
			continue
		}
		if normalizedCaseID == normalizedCriticalID || strings.Contains(normalizedCaseID, normalizedCriticalID) {
			return true
		}
	}
	return false
}

func summarizeRounds(rounds []promptiterengine.RoundResult) []RoundReport {
	out := make([]RoundReport, 0, len(rounds))
	for _, round := range rounds {
		accepted := false
		scoreDelta := 0.0
		acceptReason := ""
		if round.Acceptance != nil {
			accepted = round.Acceptance.Accepted
			scoreDelta = round.Acceptance.ScoreDelta
			acceptReason = round.Acceptance.Reason
		}
		stopReason := ""
		if round.Stop != nil {
			stopReason = round.Stop.Reason
		}
		out = append(out, RoundReport{
			Round:         round.Round,
			Train:         summarizeEvaluationResult(round.Train),
			Validation:    summarizeEvaluationResult(round.Validation),
			Patches:       summarizePatchSet(round.Patches),
			Accepted:      accepted,
			ScoreDelta:    scoreDelta,
			AcceptReason:  acceptReason,
			StopReason:    stopReason,
			OutputProfile: round.OutputProfile,
		})
	}
	return out
}

func summarizePatchSet(patchSet *promptiter.PatchSet) []PatchSummary {
	if patchSet == nil {
		return nil
	}
	out := make([]PatchSummary, 0, len(patchSet.Patches))
	for _, patch := range patchSet.Patches {
		summary := PatchSummary{
			SurfaceID: patch.SurfaceID,
			Reason:    patch.Reason,
		}
		if len(patch.Value.Tools) > 0 {
			summary.ToolID = patch.Value.Tools[0].ID
			summary.ToolDescription = patch.Value.Tools[0].Description
		}
		out = append(out, summary)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].SurfaceID < out[j].SurfaceID
	})
	return out
}

func summarizeEvaluationResult(result *promptiterengine.EvaluationResult) *EvaluationSummary {
	if result == nil {
		return nil
	}
	summary := &EvaluationSummary{
		Score:    result.OverallScore,
		EvalSets: make([]EvalSetSummary, 0, len(result.EvalSets)),
	}
	for _, evalSet := range result.EvalSets {
		setSummary := EvalSetSummary{
			EvalSetID: evalSet.EvalSetID,
			Score:     evalSet.OverallScore,
			Cases:     make([]EvalCaseSummary, 0, len(evalSet.Cases)),
		}
		for _, evalCase := range evalSet.Cases {
			caseSummary := EvalCaseSummary{
				EvalCaseID: evalCase.EvalCaseID,
				Passed:     true,
				Metrics:    make([]MetricSummary, 0, len(evalCase.Metrics)),
			}
			total := 0.0
			for _, metric := range evalCase.Metrics {
				caseSummary.Metrics = append(caseSummary.Metrics, MetricSummary{
					MetricName: metric.MetricName,
					Score:      metric.Score,
					Status:     string(metric.Status),
					Reason:     metric.Reason,
				})
				total += metric.Score
				if metric.Status != status.EvalStatusPassed {
					caseSummary.Passed = false
				}
			}
			sort.SliceStable(caseSummary.Metrics, func(i, j int) bool {
				return caseSummary.Metrics[i].MetricName < caseSummary.Metrics[j].MetricName
			})
			if len(evalCase.Metrics) > 0 {
				caseSummary.Score = total / float64(len(evalCase.Metrics))
			}
			setSummary.Cases = append(setSummary.Cases, caseSummary)
		}
		sort.SliceStable(setSummary.Cases, func(i, j int) bool {
			return setSummary.Cases[i].EvalCaseID < setSummary.Cases[j].EvalCaseID
		})
		summary.EvalSets = append(summary.EvalSets, setSummary)
	}
	sort.SliceStable(summary.EvalSets, func(i, j int) bool {
		return summary.EvalSets[i].EvalSetID < summary.EvalSets[j].EvalSetID
	})
	return summary
}

func writeOptimizationReport(outputDir string, report *OptimizationReport) (string, string, error) {
	if report == nil {
		return "", "", errors.New("report is nil")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create output dir %s: %w", outputDir, err)
	}
	jsonPath := filepath.Join(outputDir, reportJSONName)
	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("marshal report: %w", err)
	}
	jsonData = append(jsonData, '\n')
	if err := os.WriteFile(jsonPath, jsonData, 0o644); err != nil {
		return "", "", fmt.Errorf("write %s: %w", jsonPath, err)
	}
	markdownPath := filepath.Join(outputDir, reportMarkdownName)
	if err := os.WriteFile(markdownPath, []byte(renderMarkdownReport(report)), 0o644); err != nil {
		return "", "", fmt.Errorf("write %s: %w", markdownPath, err)
	}
	return jsonPath, markdownPath, nil
}

func renderMarkdownReport(report *OptimizationReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# PromptIter Regression Loop Report\n\n")
	fmt.Fprintf(&b, "- Mode: `%s`\n", report.Mode)
	fmt.Fprintf(&b, "- Prompt: `%s`\n", report.PromptSource)
	fmt.Fprintf(&b, "- Prompt hash: `%s`\n", report.PromptHash)
	fmt.Fprintf(&b, "- Target surfaces: `%s`\n", strings.Join(report.TargetSurfaces, "`, `"))
	if report.TraceSmoke.Enabled {
		fmt.Fprintf(&b, "- Trace smoke eval set: `%s`\n", report.TraceSmoke.EvalSetID)
		fmt.Fprintf(&b, "- Trace smoke score: %.3f\n", scoreOf(report.TraceSmoke.Evaluation))
		fmt.Fprintf(&b, "- Optimization skipped: %t\n", report.TraceSmoke.OptimizationSkipped)
		fmt.Fprintf(&b, "- Skip reason: %s\n\n", report.TraceSmoke.OptimizationSkippedReason)
		fmt.Fprintf(&b, "## Trace Smoke\n\n")
		fmt.Fprintf(&b, "- The trace evalset replays recorded actual invocations and execution traces.\n")
		fmt.Fprintf(&b, "- It verifies evaluation, report adaptation, and failure attribution compatibility.\n")
		fmt.Fprintf(&b, "- It does not validate candidate inference after a prompt patch.\n\n")
		fmt.Fprintf(&b, "## Failure Attribution\n\n")
		renderAttributionMarkdown(&b, report.TraceSmoke.Attribution)
		return b.String()
	}
	fmt.Fprintf(&b, "- Baseline validation score: %.3f\n", scoreOf(report.Baseline.Validation))
	fmt.Fprintf(&b, "- Candidate validation score: %.3f\n", scoreOf(report.Candidate.Validation))
	fmt.Fprintf(&b, "- Candidate train score: %.3f\n", scoreOf(report.Candidate.Train))
	fmt.Fprintf(&b, "- Candidate accepted by PromptIter: %t\n", report.Candidate.Accepted)
	fmt.Fprintf(&b, "- Final gate decision: `%s`\n\n", report.Gate.Decision)
	fmt.Fprintf(&b, "## Final Gate\n\n")
	for _, reason := range report.Gate.Reasons {
		fmt.Fprintf(&b, "- %s\n", reason)
	}
	fmt.Fprintf(&b, "\n## Validation Delta\n\n")
	fmt.Fprintf(
		&b,
		"- new_pass=%d new_fail=%d improved=%d regressed=%d unchanged_pass=%d unchanged_fail=%d\n",
		report.Delta.Summary.NewPass,
		report.Delta.Summary.NewFail,
		report.Delta.Summary.Improved,
		report.Delta.Summary.Regressed,
		report.Delta.Summary.UnchangedPass,
		report.Delta.Summary.UnchangedFail,
	)
	fmt.Fprintf(
		&b,
		"- new_hard_fail=%d critical_regression=%d score_delta=%.3f\n\n",
		report.Delta.Summary.NewHardFail,
		report.Delta.Summary.CriticalRegression,
		report.Delta.Summary.ScoreDelta,
	)
	fmt.Fprintf(&b, "## Failure Attribution\n\n")
	renderAttributionMarkdown(&b, &report.Attribution)
	fmt.Fprintf(&b, "## Rounds\n\n")
	for _, round := range report.Rounds {
		fmt.Fprintf(
			&b,
			"- Round %d: train %.3f, validation %.3f, accepted=%t, delta=%.3f\n",
			round.Round,
			scoreOf(round.Train),
			scoreOf(round.Validation),
			round.Accepted,
			round.ScoreDelta,
		)
		for _, patch := range round.Patches {
			fmt.Fprintf(&b, "  - Patch `%s`: %s\n", patch.SurfaceID, patch.ToolDescription)
		}
	}
	if len(report.Phase1Pending) > 0 {
		fmt.Fprintf(&b, "\n## Remaining Work\n\n")
		for _, pending := range report.Phase1Pending {
			fmt.Fprintf(&b, "- `%s`\n", pending)
		}
	}
	return b.String()
}

func renderAttributionMarkdown(b *strings.Builder, attribution *AttributionReport) {
	if attribution == nil || len(attribution.PerFailedCase) == 0 {
		fmt.Fprintf(b, "- no failed cases\n\n")
		return
	}
	for _, failure := range attribution.PerFailedCase {
		fmt.Fprintf(
			b,
			"- `%s`: `%s` - %s\n",
			failure.EvalCaseID,
			failure.Category,
			strings.TrimSpace(failure.Explanation),
		)
	}
	fmt.Fprintf(b, "\n")
}

func scoreOf(summary *EvaluationSummary) float64 {
	if summary == nil {
		return 0
	}
	return summary.Score
}
