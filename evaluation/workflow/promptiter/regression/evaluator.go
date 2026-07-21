//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

const (
	metricFinalResponse    = "final_response_match"
	metricToolTrajectory   = "tool_trajectory_avg_score"
	metricRoute            = "route_accuracy"
	metricStructuredOutput = "structured_output_valid"
	metricKnowledgeRecall  = "knowledge_recall"
	metricLLMRubric        = "llm_rubric"
)

// Evaluator scores a prompt variant on an evaluation set.
type Evaluator interface {
	Evaluate(ctx context.Context, set *EvalSet, variantID, prompt string) (*EvaluationSummary, error)
}

// LocalEvaluator is an API-key-free, deterministic evaluator. It consumes
// standard expected invocations and fake/trace outputs from the evalset file.
type LocalEvaluator struct {
	metrics         []MetricConfig
	fallbackVariant string
	mode            string
}

// NewLocalEvaluator creates a deterministic evaluator.
func NewLocalEvaluator(metrics []MetricConfig, fallbackVariant string, runtimeMode ...string) (*LocalEvaluator, error) {
	if err := validateMetrics(metrics); err != nil {
		return nil, err
	}
	if fallbackVariant == "" {
		fallbackVariant = "baseline"
	}
	mode := "fake"
	if len(runtimeMode) > 1 {
		return nil, errors.New("at most one runtime mode may be configured")
	}
	if len(runtimeMode) == 1 {
		mode = runtimeMode[0]
	}
	if mode != "fake" && mode != "trace" {
		return nil, fmt.Errorf("unsupported local evaluator mode %q", mode)
	}
	return &LocalEvaluator{
		metrics:         append([]MetricConfig(nil), metrics...),
		fallbackVariant: fallbackVariant,
		mode:            mode,
	}, nil
}

// RuntimeMode reports whether outputs are fake-model scenarios or strict trace replays.
func (e *LocalEvaluator) RuntimeMode() string {
	return e.mode
}

// Evaluate scores every case in input order and attaches explainable failure attribution.
func (e *LocalEvaluator) Evaluate(
	ctx context.Context,
	set *EvalSet,
	variantID string,
	prompt string,
) (*EvaluationSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if set == nil {
		return nil, errors.New("eval set is nil")
	}
	if err := validateEvalSet(set); err != nil {
		return nil, fmt.Errorf("validate eval set: %w", err)
	}
	if variantID == "" {
		return nil, errors.New("variant id is empty")
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("prompt is empty")
	}
	if !utf8.ValidString(prompt) {
		return nil, errors.New("prompt is not valid UTF-8")
	}
	markedVariant, marked := promptVariantID(prompt)
	if variantID == "baseline" {
		if marked {
			return nil, fmt.Errorf("baseline prompt unexpectedly contains candidate marker %q", markedVariant)
		}
	} else {
		if !marked {
			return nil, fmt.Errorf("candidate prompt does not contain a deterministic variant marker for %q", variantID)
		}
		if markedVariant != variantID {
			return nil, fmt.Errorf("candidate prompt marker %q does not match requested variant %q", markedVariant, variantID)
		}
		variantID = markedVariant
	}
	passThreshold := *set.PassThreshold
	summary := &EvaluationSummary{
		EvalSetID:        set.EvalSetID,
		VariantID:        variantID,
		PassThreshold:    passThreshold,
		Cases:            make([]CaseResult, 0, len(set.EvalCases)),
		AttributionStats: make(map[FailureCategory]int),
	}
	for i := range set.EvalCases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := e.evaluateCase(ctx, &set.EvalCases[i], variantID, prompt, passThreshold)
		if err != nil {
			return nil, fmt.Errorf("evaluate case %q: %w", set.EvalCases[i].EvalID, err)
		}
		result.FailureAttributions = AttributeFailure(*result)
		if len(result.FailureAttributions) > 0 {
			primary := result.FailureAttributions[0]
			result.PrimaryFailure = &primary
			summary.AttributionStats[primary.Category]++
		}
		summary.Cases = append(summary.Cases, *result)
		summary.OverallScore += result.Score
		usage, err := summary.Usage.AddChecked(result.Usage)
		if err != nil {
			return nil, fmt.Errorf("aggregate case %q usage: %w", result.CaseID, err)
		}
		summary.Usage = usage
		if result.Passed {
			summary.PassedCases++
		} else {
			summary.FailedCases++
		}
		if result.HardFail {
			summary.HardFailedCases++
		}
	}
	if len(summary.Cases) > 0 {
		summary.OverallScore = roundScore(summary.OverallScore / float64(len(summary.Cases)))
	}
	return summary, nil
}

func (e *LocalEvaluator) evaluateCase(
	ctx context.Context,
	evalCase *EvalCase,
	variantID string,
	prompt string,
	passThreshold float64,
) (*CaseResult, error) {
	expected := expectedInvocation(evalCase)
	if expected == nil {
		return nil, errors.New("expected invocation is nil")
	}
	responseVariantID := variantID
	usedFallback := false
	output, ok := evalCase.FakeResponses[variantID]
	if !ok && variantID != "baseline" {
		responseVariantID = e.fallbackVariant
		usedFallback = true
		output, ok = evalCase.FakeResponses[e.fallbackVariant]
	}
	if !ok {
		return nil, fmt.Errorf(
			"neither variant %q nor fallback %q is configured",
			variantID,
			e.fallbackVariant,
		)
	}
	semanticPromptSHA256 := HashText(semanticPromptContent(prompt))
	if !usedFallback && variantID != "baseline" && output.PromptSemanticSHA256 == "" {
		return nil, fmt.Errorf("variant %q has no prompt semantic hash binding", variantID)
	}
	if !usedFallback && output.PromptSemanticSHA256 != "" && output.PromptSemanticSHA256 != semanticPromptSHA256 {
		return nil, fmt.Errorf(
			"variant %q prompt semantic hash %q does not match evaluated prompt %q",
			responseVariantID,
			output.PromptSemanticSHA256,
			semanticPromptSHA256,
		)
	}
	if e.mode == "trace" && len(output.Trace) == 0 {
		return nil, fmt.Errorf("trace mode variant %q has no recorded trace", variantID)
	}
	if len(output.Trace) > 0 {
		var err error
		output, err = normalizeTraceReplayOutput(output, e.mode == "trace")
		if err != nil {
			return nil, fmt.Errorf("variant %q trace is inconsistent: %w", variantID, err)
		}
	}
	expectedResponse := ""
	if expected.FinalResponse != nil {
		expectedResponse = expected.FinalResponse.Content
	}
	structuredValid := determineStructuredValidity(evalCase.Expectations.ResponseFormat, output)
	usage := output.Usage
	observedToolCalls := len(output.Tools)
	if traceToolCalls := countTraceToolCalls(output.Trace); traceToolCalls > observedToolCalls {
		observedToolCalls = traceToolCalls
	}
	if usage.ToolCalls == 0 && observedToolCalls > 0 {
		usage.ToolCalls = observedToolCalls
	}
	if usage.ToolCalls != observedToolCalls {
		return nil, fmt.Errorf("reported tool calls %d do not match observed calls %d", usage.ToolCalls, observedToolCalls)
	}
	if err := validateUsage(usage); err != nil {
		return nil, fmt.Errorf("invalid usage: %w", err)
	}
	executionError := output.Error
	if executionError == "" {
		executionError = traceFailureMessage(output.Trace, e.mode == "trace")
	}
	result := &CaseResult{
		CaseID:                 evalCase.EvalID,
		Critical:               evalCase.Critical,
		Error:                  executionError,
		ResponseVariantID:      responseVariantID,
		ResponsePromptSHA256:   output.PromptSemanticSHA256,
		UsedFallback:           usedFallback,
		ExpectedResponse:       expectedResponse,
		FinalResponse:          output.Response,
		ExpectedToolTrajectory: expected.Tools,
		ToolTrajectory:         output.Tools,
		ExpectedRoute:          evalCase.Expectations.Route,
		Route:                  output.Route,
		ResponseFormat:         evalCase.Expectations.ResponseFormat,
		StructuredValid:        structuredValid,
		RequiredFacts:          append([]string(nil), evalCase.Expectations.RequiredFacts...),
		RetrievedFacts:         append([]string(nil), output.RetrievedFacts...),
		MinRetrievedDocuments:  evalCase.Expectations.MinRetrievedDocuments,
		RetrievedDocuments:     output.RetrievedDocuments,
		RubricReason:           output.RubricReason,
		Trace:                  append([]TraceStep(nil), output.Trace...),
		Usage:                  usage,
	}
	if executionError != "" {
		result.Score = 0
		result.Passed = false
		result.HardFail = true
		for _, metric := range e.metrics {
			result.MetricResults = append(result.MetricResults, MetricResult{
				MetricName: metric.MetricName,
				Threshold:  metric.Threshold,
				Weight:     metric.Weight,
				Passed:     false,
				HardFail:   metric.HardFail,
				Reason:     "inference error: " + executionError,
			})
		}
		return result, nil
	}
	totalWeight := 0.0
	for _, metric := range e.metrics {
		score, reason, err := scoreMetric(ctx, metric.MetricName, expected, evalCase.Expectations, output, structuredValid)
		if err != nil {
			return nil, fmt.Errorf("score metric %q: %w", metric.MetricName, err)
		}
		score = roundScore(score)
		passed := scorePasses(score, metric.Threshold)
		result.MetricResults = append(result.MetricResults, MetricResult{
			MetricName: metric.MetricName,
			Score:      score,
			Threshold:  metric.Threshold,
			Weight:     metric.Weight,
			Passed:     passed,
			HardFail:   metric.HardFail,
			Reason:     reason,
		})
		result.Score += score * metric.Weight
		totalWeight += metric.Weight
		if metric.HardFail && !passed {
			result.HardFail = true
		}
	}
	if totalWeight == 0 {
		return nil, errors.New("metric total weight is zero")
	}
	result.Score = roundScore(result.Score / totalWeight)
	result.Passed = scorePasses(result.Score, passThreshold) && !result.HardFail
	return result, nil
}

func traceFailureMessage(trace []TraceStep, strict bool) string {
	for _, step := range trace {
		status := strings.ToLower(strings.TrimSpace(step.Status))
		failed := traceStatusIndicatesFailure(status)
		if strict {
			failed = !traceStatusSuccessful(status)
		}
		if failed {
			message := fmt.Sprintf("trace step %s ended with status %s", step.StepID, step.Status)
			if step.Message != "" {
				message += ": " + step.Message
			}
			return message
		}
	}
	return ""
}

func countTraceToolCalls(trace []TraceStep) int {
	count := 0
	for _, step := range trace {
		if isToolCallTraceStep(step.Kind) {
			count++
		}
	}
	return count
}

func normalizeTraceReplayOutput(output FakeOutput, strict bool) (FakeOutput, error) {
	if strict && len(output.Trace) == 0 {
		return FakeOutput{}, errors.New("strict trace is empty")
	}
	var route string
	toolSteps := make([]TraceStep, 0)
	retrievalSeen := false
	lastRetrievalIndex := -1
	lastModelResponseIndex := -1
	modelCalls := 0
	toolCalls := 0
	hasFailedStep := false
	previousElapsed := int64(0)
	for index, step := range output.Trace {
		if strings.TrimSpace(step.Kind) == "" {
			return FakeOutput{}, fmt.Errorf("trace step %q has no kind", step.StepID)
		}
		if strict {
			if step.ElapsedMS == nil {
				return FakeOutput{}, fmt.Errorf("trace step %q has no elapsedMs", step.StepID)
			}
			if *step.ElapsedMS < 0 || (index > 0 && *step.ElapsedMS < previousElapsed) {
				return FakeOutput{}, fmt.Errorf("trace step %q elapsedMs is negative or non-monotonic", step.StepID)
			}
			previousElapsed = *step.ElapsedMS
		}
		if !traceStatusSuccessful(step.Status) && !traceStatusIndicatesFailure(step.Status) {
			return FakeOutput{}, fmt.Errorf("trace step %q has unknown status %q", step.StepID, step.Status)
		}
		kind := strings.ToLower(strings.TrimSpace(step.Kind))
		if isModelResponseTraceStep(kind) {
			modelCalls++
			lastModelResponseIndex = index
		}
		toolStep := isToolCallTraceStep(kind)
		if toolStep {
			toolCalls++
			if strict || traceStatusSuccessful(step.Status) {
				if strings.TrimSpace(step.Name) == "" {
					return FakeOutput{}, fmt.Errorf("tool trace step %q has no name", step.StepID)
				}
				toolSteps = append(toolSteps, step)
			}
		}
		if traceStatusIndicatesFailure(step.Status) {
			hasFailedStep = true
			if strict && index != len(output.Trace)-1 {
				next := output.Trace[index+1]
				if index != len(output.Trace)-2 ||
					!isModelResponseTraceStep(next.Kind) ||
					!traceStatusIndicatesFailure(next.Status) {
					return FakeOutput{}, fmt.Errorf(
						"failed trace step %q is not immediately followed by a failed terminal response",
						step.StepID,
					)
				}
			}
			continue
		}
		if isRouteTraceStep(kind) {
			if strings.TrimSpace(step.Name) == "" {
				return FakeOutput{}, fmt.Errorf("route trace step %q has no name", step.StepID)
			}
			route = step.Name
		}
		if isRetrievalTraceStep(kind) {
			retrievalSeen = true
			lastRetrievalIndex = index
			if rawDocuments, exists := step.Output["documents"]; exists {
				if _, ok := traceInteger(rawDocuments); !ok {
					return FakeOutput{}, fmt.Errorf("retrieval step %q has invalid documents count", step.StepID)
				}
			}
			if rawMatchedFacts, exists := step.Output["matchedFacts"]; exists {
				if _, ok := traceInteger(rawMatchedFacts); !ok {
					return FakeOutput{}, fmt.Errorf("retrieval step %q has invalid matchedFacts count", step.StepID)
				}
			}
			if rawFacts, exists := step.Output["facts"]; exists {
				if _, ok := traceStrings(rawFacts); !ok {
					return FakeOutput{}, fmt.Errorf("retrieval step %q has invalid facts", step.StepID)
				}
			}
		}
	}
	if route != "" {
		if output.Route == "" {
			output.Route = route
		} else if output.Route != route {
			return FakeOutput{}, fmt.Errorf("trace route %q does not match output route %q", route, output.Route)
		}
	}
	if strict && len(toolSteps) != len(output.Tools) {
		return FakeOutput{}, fmt.Errorf("trace has %d tool calls but output has %d", len(toolSteps), len(output.Tools))
	}
	if !strict && len(toolSteps) > len(output.Tools) {
		return FakeOutput{}, fmt.Errorf("trace has %d evidenced tool calls but output has %d", len(toolSteps), len(output.Tools))
	}
	for i, step := range toolSteps {
		actual := output.Tools[i]
		if actual == nil {
			return FakeOutput{}, fmt.Errorf("output tool %d is nil", i)
		}
		if step.Name != "" && step.Name != actual.Name {
			return FakeOutput{}, fmt.Errorf("trace tool %q does not match output tool %q", step.Name, actual.Name)
		}
		if strict && actual.Arguments != nil && step.Input == nil {
			return FakeOutput{}, fmt.Errorf("trace input for tool %q is missing", actual.Name)
		}
		if step.Input != nil && !canonicalEqual(step.Input, actual.Arguments) {
			return FakeOutput{}, fmt.Errorf("trace input for tool %q does not match output arguments", actual.Name)
		}
		if strict && actual.Result != nil && step.Output == nil {
			return FakeOutput{}, fmt.Errorf("trace output for tool %q is missing", actual.Name)
		}
		if step.Output != nil && !canonicalEqual(step.Output, actual.Result) {
			return FakeOutput{}, fmt.Errorf("trace output for tool %q does not match output result", actual.Name)
		}
	}
	if lastRetrievalIndex >= 0 {
		step := output.Trace[lastRetrievalIndex]
		rawDocuments, documentsExist := step.Output["documents"]
		if strict && !documentsExist {
			return FakeOutput{}, fmt.Errorf("retrieval step %q has no documents count", step.StepID)
		}
		if documentsExist {
			documents, ok := traceInteger(rawDocuments)
			if !ok || documents != output.RetrievedDocuments {
				return FakeOutput{}, fmt.Errorf("retrieval step %q documents do not match output", step.StepID)
			}
		}
		rawFacts, factsExist := step.Output["facts"]
		if strict && !factsExist {
			return FakeOutput{}, fmt.Errorf("retrieval step %q has no facts", step.StepID)
		}
		if factsExist {
			facts, ok := traceStrings(rawFacts)
			if !ok || !normalizedStringSetEqual(facts, output.RetrievedFacts) {
				return FakeOutput{}, fmt.Errorf("retrieval step %q facts do not match output", step.StepID)
			}
		}
		rawMatchedFacts, matchedFactsExist := step.Output["matchedFacts"]
		if strict && !matchedFactsExist {
			return FakeOutput{}, fmt.Errorf("retrieval step %q has no matchedFacts count", step.StepID)
		}
		if matchedFactsExist {
			matchedFacts, ok := traceInteger(rawMatchedFacts)
			if !ok || matchedFacts != len(output.RetrievedFacts) {
				return FakeOutput{}, fmt.Errorf("retrieval step %q matchedFacts do not match output", step.StepID)
			}
		}
	}
	if !strict {
		if lastModelResponseIndex >= 0 {
			lastModelResponse := output.Trace[lastModelResponseIndex]
			if traceStatusSuccessful(lastModelResponse.Status) &&
				lastModelResponse.Message != "" && lastModelResponse.Message != output.Response {
				return FakeOutput{}, fmt.Errorf(
					"final-response trace step %q does not match output response",
					lastModelResponse.StepID,
				)
			}
			if traceStatusIndicatesFailure(lastModelResponse.Status) && output.Error != "" &&
				lastModelResponse.Message != "" && lastModelResponse.Message != output.Error {
				return FakeOutput{}, fmt.Errorf(
					"failed final-response trace step %q does not match output error",
					lastModelResponse.StepID,
				)
			}
		}
		return output, nil
	}
	if toolCalls != output.Usage.ToolCalls {
		return FakeOutput{}, fmt.Errorf("trace tool calls %d do not match usage %d", toolCalls, output.Usage.ToolCalls)
	}
	if modelCalls != output.Usage.ModelCalls {
		return FakeOutput{}, fmt.Errorf("trace model calls %d do not match usage %d", modelCalls, output.Usage.ModelCalls)
	}
	last := output.Trace[len(output.Trace)-1]
	if last.ElapsedMS == nil || *last.ElapsedMS != output.Usage.LatencyMS {
		return FakeOutput{}, fmt.Errorf("terminal trace elapsedMs does not match usage latencyMs %d", output.Usage.LatencyMS)
	}
	lastKind := strings.ToLower(strings.TrimSpace(last.Kind))
	if !isModelResponseTraceStep(lastKind) {
		return FakeOutput{}, errors.New("strict trace must end with a final_response or llm step")
	}
	if last.Usage == nil || *last.Usage != output.Usage {
		return FakeOutput{}, errors.New("terminal trace usage does not match output usage")
	}
	if (last.RubricScore == nil) != (output.RubricScore == nil) ||
		last.RubricScore != nil && *last.RubricScore != *output.RubricScore {
		return FakeOutput{}, errors.New("terminal trace rubric score does not match output rubric score")
	}
	if last.RubricReason != output.RubricReason {
		return FakeOutput{}, errors.New("terminal trace rubric reason does not match output rubric reason")
	}
	if output.Route != "" && route == "" {
		return FakeOutput{}, fmt.Errorf("output route %q has no trace evidence", output.Route)
	}
	if (output.RetrievedDocuments > 0 || len(output.RetrievedFacts) > 0) && !retrievalSeen {
		return FakeOutput{}, errors.New("retrieval output has no trace evidence")
	}
	if hasFailedStep {
		if output.Response != "" {
			return FakeOutput{}, errors.New("failed trace cannot have a final response")
		}
		if !traceStatusIndicatesFailure(last.Status) {
			return FakeOutput{}, errors.New("failed trace must end with a failed final response")
		}
		if strings.TrimSpace(output.Error) == "" || last.Message != output.Error {
			return FakeOutput{}, errors.New("failed terminal trace message does not match output error")
		}
		return output, nil
	}
	if !isModelResponseTraceStep(lastKind) || !traceStatusSuccessful(last.Status) {
		return FakeOutput{}, errors.New("successful trace must end with a completed final_response or llm step")
	}
	if last.Message != output.Response {
		return FakeOutput{}, errors.New("terminal trace message does not match output response")
	}
	if strings.TrimSpace(output.Error) != "" {
		return FakeOutput{}, errors.New("successful terminal trace conflicts with output error")
	}
	return output, nil
}

func isModelResponseTraceStep(kind string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	return kind == "llm" || kind == "final_response" || kind == "final-response"
}

func traceStrings(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			result = append(result, text)
		}
		return result, true
	default:
		return nil, false
	}
}

func normalizedStringSetEqual(left, right []string) bool {
	leftSet := make(map[string]struct{}, len(left))
	for _, value := range left {
		normalized := normalizeText(value)
		if normalized == "" {
			return false
		}
		if _, exists := leftSet[normalized]; exists {
			return false
		}
		leftSet[normalized] = struct{}{}
	}
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		normalized := normalizeText(value)
		if normalized == "" {
			return false
		}
		if _, exists := rightSet[normalized]; exists {
			return false
		}
		rightSet[normalized] = struct{}{}
	}
	if len(leftSet) != len(rightSet) {
		return false
	}
	for value := range leftSet {
		if _, exists := rightSet[value]; !exists {
			return false
		}
	}
	return true
}

func traceStatusSuccessful(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "success", "succeeded", "ok":
		return true
	default:
		return false
	}
}

func traceStatusIndicatesFailure(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "failure", "error", "errored", "timeout", "timed_out", "timed-out",
		"cancelled", "canceled", "incomplete", "aborted", "abort":
		return true
	default:
		return false
	}
}

func isToolCallTraceStep(kind string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "tool", "tool_call", "tool-call", "tool_use", "tool-use", "tool_execution",
		"function_call", "function-call":
		return true
	default:
		return false
	}
}

func isRouteTraceStep(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "route", "router", "routing", "route_decision", "route-decision", "handoff", "transfer":
		return true
	default:
		return false
	}
}

func isRetrievalTraceStep(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "retrieval", "retrieve", "knowledge", "knowledge_retrieval", "knowledge-retrieval":
		return true
	default:
		return false
	}
}

func traceInteger(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, typed >= 0
	case int64:
		if typed < 0 || typed > int64(int(^uint(0)>>1)) {
			return 0, false
		}
		return int(typed), true
	case float64:
		if typed < 0 || typed != math.Trunc(typed) || typed > float64(int(^uint(0)>>1)) {
			return 0, false
		}
		return int(typed), true
	case json.Number:
		integer, err := typed.Int64()
		if err != nil || integer < 0 || integer > int64(int(^uint(0)>>1)) {
			return 0, false
		}
		return int(integer), true
	default:
		return 0, false
	}
}

func scoreMetric(
	ctx context.Context,
	metricName string,
	expected *evalset.Invocation,
	expectations Expectations,
	output FakeOutput,
	structuredValid bool,
) (float64, string, error) {
	switch metricName {
	case metricFinalResponse:
		if expected.FinalResponse == nil {
			return 1, "no final response reference", nil
		}
		expectedText := expected.FinalResponse.Content
		score, err := textSimilarity(ctx, expectedText, output.Response)
		if err != nil {
			return 0, "", err
		}
		if score == 1 {
			return score, "final response matches reference", nil
		}
		return score, fmt.Sprintf("final response ROUGE similarity is %.6f", score), nil
	case metricToolTrajectory:
		score, detail := toolTrajectoryScore(expected.Tools, output.Tools)
		return score, detail, nil
	case metricRoute:
		if expectations.Route == "" {
			return 1, "no route required", nil
		}
		if expectations.Route == output.Route {
			return 1, "route matches reference", nil
		}
		return 0, fmt.Sprintf("expected route %q, got %q", expectations.Route, output.Route), nil
	case metricStructuredOutput:
		if expectations.ResponseFormat == "" {
			return 1, "no structured output required", nil
		}
		if structuredValid {
			return 1, "structured output is valid", nil
		}
		return 0, fmt.Sprintf("response is not valid %s", expectations.ResponseFormat), nil
	case metricKnowledgeRecall:
		score := knowledgeRecall(expectations, output)
		if score == 1 {
			return score, "required knowledge was retrieved", nil
		}
		return score, "required knowledge retrieval is incomplete", nil
	case metricLLMRubric:
		if output.RubricScore != nil {
			return clampScore(*output.RubricScore), defaultReason(output.RubricReason, "fake rubric score"), nil
		}
		if expected.FinalResponse == nil {
			return 1, defaultReason(output.RubricReason, "no fake rubric or final response reference"), nil
		}
		expectedText := expected.FinalResponse.Content
		score, err := textSimilarity(ctx, expectedText, output.Response)
		if err != nil {
			return 0, "", err
		}
		return score, defaultReason(output.RubricReason, "rubric derived from ROUGE final-response similarity"), nil
	default:
		return 0, "unsupported metric", nil
	}
}

func supportedMetric(name string) bool {
	switch name {
	case metricFinalResponse, metricToolTrajectory, metricRoute,
		metricStructuredOutput, metricKnowledgeRecall, metricLLMRubric:
		return true
	default:
		return false
	}
}

func determineStructuredValidity(format string, output FakeOutput) bool {
	switch strings.ToLower(format) {
	case "":
		return true
	case "json":
		valid := json.Valid([]byte(output.Response))
		if output.StructuredValid != nil {
			valid = valid && *output.StructuredValid
		}
		return valid
	case "xml":
		valid := validXML(output.Response)
		if output.StructuredValid != nil {
			valid = valid && *output.StructuredValid
		}
		return valid
	case "yaml", "yml":
		valid := validYAML(output.Response)
		if output.StructuredValid != nil {
			valid = valid && *output.StructuredValid
		}
		return valid
	default:
		return output.StructuredValid != nil && *output.StructuredValid
	}
}

func validYAML(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	decoder := yaml.NewDecoder(strings.NewReader(value))
	var document any
	if err := decoder.Decode(&document); err != nil {
		return false
	}
	var trailing any
	return errors.Is(decoder.Decode(&trailing), io.EOF)
}

func validXML(value string) bool {
	decoder := xml.NewDecoder(strings.NewReader(value))
	depth := 0
	roots := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return roots == 1 && depth == 0
		}
		if err != nil {
			return false
		}
		switch token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots > 1 {
					return false
				}
			}
			depth++
		case xml.EndElement:
			depth--
			if depth < 0 {
				return false
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(token.(xml.CharData))) != "" {
				return false
			}
		}
	}
}

func toolTrajectoryScore(expected, actual []*evalset.Tool) (float64, string) {
	if len(expected) == 0 && len(actual) == 0 {
		return 1, "tool trajectory is empty as expected"
	}
	if len(expected) == 0 || len(actual) == 0 {
		return 0, fmt.Sprintf("expected %d tool calls, got %d", len(expected), len(actual))
	}
	matched := 0.0
	maxLen := len(expected)
	if len(actual) > maxLen {
		maxLen = len(actual)
	}
	for i := 0; i < len(expected) && i < len(actual); i++ {
		if expected[i] == nil || actual[i] == nil {
			continue
		}
		if expected[i].Name != actual[i].Name {
			continue
		}
		matched += 0.4
		if expected[i].Arguments == nil || canonicalEqual(expected[i].Arguments, actual[i].Arguments) {
			matched += 0.4
		}
		if expected[i].Result == nil || canonicalEqual(expected[i].Result, actual[i].Result) {
			matched += 0.2
		}
	}
	score := clampScore(matched / float64(maxLen))
	if score == 1 {
		return score, "tool trajectory matches reference"
	}
	return score, "tool name, arguments, result, or call count differs from reference"
}

func canonicalEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func knowledgeRecall(expectations Expectations, output FakeOutput) float64 {
	components := make([]float64, 0, 2)
	if len(expectations.RequiredFacts) > 0 {
		actual := make(map[string]struct{}, len(output.RetrievedFacts))
		for _, fact := range output.RetrievedFacts {
			actual[normalizeText(fact)] = struct{}{}
		}
		hits := 0
		for _, fact := range expectations.RequiredFacts {
			if _, ok := actual[normalizeText(fact)]; ok {
				hits++
			}
		}
		components = append(components, float64(hits)/float64(len(expectations.RequiredFacts)))
	}
	if expectations.MinRetrievedDocuments > 0 {
		documentScore := float64(output.RetrievedDocuments) / float64(expectations.MinRetrievedDocuments)
		components = append(components, clampScore(documentScore))
	}
	if len(components) == 0 {
		return 1
	}
	total := 0.0
	for _, component := range components {
		total += component
	}
	return clampScore(total / float64(len(components)))
}

func normalizeText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Join(strings.Fields(value), " ")
}

func clampScore(score float64) float64 {
	return math.Max(0, math.Min(1, score))
}

func roundScore(score float64) float64 {
	return math.Round(clampScore(score)*1_000_000) / 1_000_000
}

func defaultReason(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
