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
	"fmt"
	"reflect"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// FailureCategory classifies why an eval case failed. Categories participate
// in a fixed causal propagation order (route -> tool -> response-level), so
// downstream symptoms can be folded under their root cause.
type FailureCategory string

const (
	// CauseFinalResponseMismatch marks final responses that miss the expected
	// content or quality bar. It is also the fallback category.
	CauseFinalResponseMismatch FailureCategory = "final_response_mismatch"
	// CauseToolCallError marks wrong, missing, or extra tool calls.
	CauseToolCallError FailureCategory = "tool_call_error"
	// CauseToolArgumentError marks correct tool choices with wrong arguments.
	CauseToolArgumentError FailureCategory = "tool_argument_error"
	// CauseRouteError marks sub-agent or graph node routing mistakes.
	CauseRouteError FailureCategory = "route_error"
	// CauseFormatError marks structured output or format rubric violations.
	CauseFormatError FailureCategory = "format_error"
	// CauseKnowledgeRecallGap marks answers grounded on missing knowledge.
	CauseKnowledgeRecallGap FailureCategory = "knowledge_recall_gap"
)

// knownFailureCategories is the closed set accepted by configuration fields
// that reference categories by name.
var knownFailureCategories = []FailureCategory{
	CauseFinalResponseMismatch,
	CauseToolCallError,
	CauseToolArgumentError,
	CauseRouteError,
	CauseFormatError,
	CauseKnowledgeRecallGap,
}

// IsKnownFailureCategory reports whether name is a valid failure category.
func IsKnownFailureCategory(name string) bool {
	for _, category := range knownFailureCategories {
		if string(category) == name {
			return true
		}
	}
	return false
}

// knownFailureCategoryList renders the category set for error messages.
func knownFailureCategoryList() string {
	names := make([]string, 0, len(knownFailureCategories))
	for _, category := range knownFailureCategories {
		names = append(names, string(category))
	}
	return strings.Join(names, ", ")
}

// causalRank orders categories along the execution pipeline: routing decides
// which agent runs, tools decide what data the answer sees, and response
// level failures are downstream symptoms of both.
var causalRank = map[FailureCategory]int{
	CauseRouteError:            0,
	CauseToolCallError:         1,
	CauseToolArgumentError:     1,
	CauseFormatError:           2,
	CauseKnowledgeRecallGap:    2,
	CauseFinalResponseMismatch: 2,
}

// lossSeverityByCategory maps root cause categories to loss hint severities.
var lossSeverityByCategory = map[FailureCategory]promptiter.LossSeverity{
	CauseRouteError:            promptiter.LossSeverityP0,
	CauseToolCallError:         promptiter.LossSeverityP0,
	CauseToolArgumentError:     promptiter.LossSeverityP1,
	CauseFormatError:           promptiter.LossSeverityP1,
	CauseKnowledgeRecallGap:    promptiter.LossSeverityP1,
	CauseFinalResponseMismatch: promptiter.LossSeverityP2,
}

// FailureCause is one classified failure signal on one case.
type FailureCause struct {
	// Category is the failure classification.
	Category FailureCategory `json:"category"`
	// Metric names the failing metric that produced the signal.
	Metric string `json:"metric"`
	// Evidence is a human-readable explanation with concrete expected versus
	// actual details.
	Evidence string `json:"evidence"`
	// DerivedFrom marks this cause as a downstream symptom of an upstream
	// root cause. Empty for root causes.
	DerivedFrom FailureCategory `json:"derivedFrom,omitempty"`
}

// CaseAttribution is the causal-chain attribution of one failed case.
type CaseAttribution struct {
	// EvalSetID and EvalCaseID identify the failed case.
	EvalSetID  string `json:"evalSetId"`
	EvalCaseID string `json:"evalCaseId"`
	// RootCauses lists independent root causes (lowest causal rank).
	RootCauses []FailureCause `json:"rootCauses"`
	// Chain lists all causes in causal order, roots first; derived causes
	// carry DerivedFrom.
	Chain []FailureCause `json:"chain"`
}

// ChainSummary renders the causal chain compactly, e.g.
// "root: tool_call_error, cascaded to final_response_mismatch".
func (a *CaseAttribution) ChainSummary() string {
	if a == nil || len(a.Chain) == 0 {
		return ""
	}
	roots := make([]string, 0, len(a.RootCauses))
	for _, cause := range a.RootCauses {
		roots = append(roots, string(cause.Category))
	}
	derived := make([]string, 0)
	for _, cause := range a.Chain {
		if cause.DerivedFrom != "" {
			derived = append(derived, string(cause.Category))
		}
	}
	summary := "root: " + strings.Join(roots, ", ")
	if len(derived) > 0 {
		summary += ", cascaded to " + strings.Join(derived, ", ")
	}
	return summary
}

// Attributor classifies failed cases into causal failure chains. It is a
// deterministic rule engine over metric outcomes, expected versus actual tool
// trajectories, and traces.
type Attributor struct {
	// metricCategories routes metric names to categories. Entries derived
	// from criterion structure are overridden by configured hints.
	metricCategories map[string]FailureCategory
}

// NewAttributor builds an attributor from criterion-derived metric categories
// and configured hint overrides.
func NewAttributor(metrics []*metric.EvalMetric, hints map[string]string) *Attributor {
	categories := make(map[string]FailureCategory)
	for _, evalMetric := range metrics {
		if evalMetric == nil {
			continue
		}
		if category, ok := deriveMetricCategory(evalMetric); ok {
			categories[evalMetric.MetricName] = category
		}
	}
	for metricName, category := range hints {
		categories[metricName] = FailureCategory(category)
	}
	return &Attributor{metricCategories: categories}
}

// deriveMetricCategory classifies a metric by its criterion structure, which
// is more robust than metric naming conventions on hidden samples.
func deriveMetricCategory(evalMetric *metric.EvalMetric) (FailureCategory, bool) {
	criterion := evalMetric.Criterion
	if criterion == nil {
		return "", false
	}
	if criterion.ToolTrajectory != nil {
		// The tool family; split into call versus argument error per case.
		return CauseToolCallError, true
	}
	if criterion.FinalResponse != nil {
		if criterion.FinalResponse.JSON != nil || criterion.FinalResponse.XML != nil {
			return CauseFormatError, true
		}
		return CauseFinalResponseMismatch, true
	}
	if criterion.LLMJudge != nil {
		return deriveRubricCategory(criterion.LLMJudge.Rubrics), true
	}
	return "", false
}

// deriveRubricCategory picks a category for an LLM judge metric from its
// rubric types when they are unanimous: all-format rubrics classify as
// format_error, all-knowledge rubrics as knowledge_recall_gap, anything
// mixed or unmarked stays a final response quality failure. Rubric types are
// free-form strings, so the match is anchored to the type prefix (e.g.
// FORMAT_COMPLIANCE) to avoid substring hits such as INFORMATION_ACCURACY.
func deriveRubricCategory(rubrics []*llm.Rubric) FailureCategory {
	if len(rubrics) == 0 {
		return CauseFinalResponseMismatch
	}
	allFormat := true
	allKnowledge := true
	for _, rubric := range rubrics {
		if rubric == nil {
			continue
		}
		rubricType := strings.ToUpper(rubric.Type)
		if !strings.HasPrefix(rubricType, "FORMAT") {
			allFormat = false
		}
		if !strings.HasPrefix(rubricType, "KNOWLEDGE") {
			allKnowledge = false
		}
	}
	switch {
	case allFormat:
		return CauseFormatError
	case allKnowledge:
		return CauseKnowledgeRecallGap
	default:
		return CauseFinalResponseMismatch
	}
}

// Attribute classifies one failed case. It returns nil when the case passed.
// expected carries the eval case's expected invocations for trajectory diffs;
// it may be nil when unavailable.
func (a *Attributor) Attribute(snapshot CaseSnapshot, expected []*evalset.Invocation) *CaseAttribution {
	if snapshot.Pass {
		return nil
	}
	hits := make([]FailureCause, 0, len(snapshot.Metrics))
	for _, metricResult := range snapshot.Metrics {
		if metricResult.Status != status.EvalStatusFailed {
			continue
		}
		hits = append(hits, a.classifyMetricFailure(snapshot, metricResult, expected))
	}
	if len(hits) == 0 {
		// The case failed without a failed metric (defensive): keep the
		// guarantee that every failed case has at least one explainable cause.
		hits = append(hits, FailureCause{
			Category: CauseFinalResponseMismatch,
			Evidence: "case failed without per-metric failure details",
		})
	}
	return foldCausalChain(snapshot, hits)
}

// classifyMetricFailure maps one failed metric to a failure cause.
func (a *Attributor) classifyMetricFailure(
	snapshot CaseSnapshot,
	metricResult MetricSnapshot,
	expected []*evalset.Invocation,
) FailureCause {
	cause := FailureCause{
		Metric:   metricResult.Name,
		Evidence: metricResult.Reason,
	}
	category, hasCategory := a.metricCategories[metricResult.Name]
	if !hasCategory {
		category = CauseFinalResponseMismatch
	}
	if category == CauseToolCallError || category == CauseToolArgumentError {
		return a.classifyToolFailure(snapshot, metricResult, expected)
	}
	cause.Category = category
	if cause.Evidence == "" {
		cause.Evidence = fmt.Sprintf("metric %s failed with score %.4f", metricResult.Name, metricResult.Score)
	}
	return cause
}

// classifyToolFailure splits tool-family failures into wrong/missing/extra
// tool calls versus wrong arguments by diffing actual against expected
// trajectories, falling back to the criterion failure reason text.
func (a *Attributor) classifyToolFailure(
	snapshot CaseSnapshot,
	metricResult MetricSnapshot,
	expected []*evalset.Invocation,
) FailureCause {
	cause := FailureCause{Metric: metricResult.Name}
	actualTools := collectTools(snapshot.ActualInvocations)
	expectedTools := collectTools(expected)
	if snapshot.ActualInvocations != nil && expected != nil {
		if evidence, mismatch := diffToolNames(actualTools, expectedTools); mismatch {
			cause.Category = CauseToolCallError
			cause.Evidence = evidence
			return cause
		}
		if evidence, mismatch := diffToolArguments(actualTools, expectedTools); mismatch {
			cause.Category = CauseToolArgumentError
			cause.Evidence = evidence
			return cause
		}
	}
	// Trajectory data unavailable or inconclusive: fall back to the
	// structured reason text emitted by the tool trajectory criterion.
	reason := metricResult.Reason
	cause.Evidence = reason
	if strings.Contains(reason, "arguments mismatch") {
		cause.Category = CauseToolArgumentError
		return cause
	}
	cause.Category = CauseToolCallError
	if reason == "" {
		cause.Evidence = "tool trajectory metric failed without detailed reason"
	}
	return cause
}

func collectTools(invocations []*evalset.Invocation) []*evalset.Tool {
	tools := make([]*evalset.Tool, 0)
	for _, invocation := range invocations {
		if invocation == nil {
			continue
		}
		tools = append(tools, invocation.Tools...)
	}
	return tools
}

// diffToolNames compares tool name multisets and describes missing and
// unexpected calls.
func diffToolNames(actual, expected []*evalset.Tool) (string, bool) {
	actualNames := toolNameCounts(actual)
	expectedNames := toolNameCounts(expected)
	missing := make([]string, 0)
	for name, count := range expectedNames {
		if actualNames[name] < count {
			missing = append(missing, name)
		}
	}
	extra := make([]string, 0)
	for name, count := range actualNames {
		if expectedNames[name] < count {
			extra = append(extra, name)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return "", false
	}
	sort.Strings(missing)
	sort.Strings(extra)
	parts := make([]string, 0, 2)
	if len(missing) > 0 {
		parts = append(parts, "expected tool(s) not called: "+strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		parts = append(parts, "unexpected tool call(s): "+strings.Join(extra, ", "))
	}
	return strings.Join(parts, "; "), true
}

func toolNameCounts(tools []*evalset.Tool) map[string]int {
	counts := make(map[string]int, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		counts[tool.Name]++
	}
	return counts
}

// diffToolArguments reports the first argument mismatch between same-named
// actual and expected tool calls, in call order.
func diffToolArguments(actual, expected []*evalset.Tool) (string, bool) {
	byName := make(map[string][]*evalset.Tool)
	for _, tool := range actual {
		if tool == nil {
			continue
		}
		byName[tool.Name] = append(byName[tool.Name], tool)
	}
	for _, expectedTool := range expected {
		if expectedTool == nil {
			continue
		}
		candidates := byName[expectedTool.Name]
		if len(candidates) == 0 {
			continue
		}
		actualTool := candidates[0]
		byName[expectedTool.Name] = candidates[1:]
		if !argumentsEqual(actualTool.Arguments, expectedTool.Arguments) {
			return fmt.Sprintf(
				"tool %s argument mismatch: expected %s, actual %s",
				expectedTool.Name,
				compactJSON(expectedTool.Arguments),
				compactJSON(actualTool.Arguments),
			), true
		}
	}
	return "", false
}

// argumentsEqual compares tool arguments structurally, tolerating JSON
// strings versus decoded maps.
func argumentsEqual(actual, expected any) bool {
	return reflect.DeepEqual(normalizeJSONValue(actual), normalizeJSONValue(expected))
}

func normalizeJSONValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		var decoded any
		if err := json.Unmarshal([]byte(typed), &decoded); err == nil {
			return decoded
		}
		return typed
	case []byte:
		var decoded any
		if err := json.Unmarshal(typed, &decoded); err == nil {
			return decoded
		}
		return string(typed)
	default:
		// Round-trip through JSON to normalize numeric types and maps.
		encoded, err := json.Marshal(typed)
		if err != nil {
			return typed
		}
		var decoded any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			return typed
		}
		return decoded
	}
}

func compactJSON(value any) string {
	encoded, err := json.Marshal(normalizeJSONValue(value))
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

// foldCausalChain groups hits by causal rank: lowest-rank hits become the
// independent root causes, higher-rank hits are marked as derived symptoms.
// Duplicate categories are merged, keeping the first evidence.
func foldCausalChain(snapshot CaseSnapshot, hits []FailureCause) *CaseAttribution {
	merged := make([]FailureCause, 0, len(hits))
	seen := make(map[FailureCategory]int)
	for _, hit := range hits {
		if index, ok := seen[hit.Category]; ok {
			if merged[index].Evidence == "" {
				merged[index].Evidence = hit.Evidence
			}
			continue
		}
		seen[hit.Category] = len(merged)
		merged = append(merged, hit)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return causalRank[merged[i].Category] < causalRank[merged[j].Category]
	})
	rootRank := causalRank[merged[0].Category]
	attribution := &CaseAttribution{
		EvalSetID:  snapshot.EvalSetID,
		EvalCaseID: snapshot.EvalCaseID,
	}
	rootCategory := merged[0].Category
	for _, cause := range merged {
		if causalRank[cause.Category] == rootRank {
			attribution.RootCauses = append(attribution.RootCauses, cause)
		} else {
			cause.DerivedFrom = rootCategory
		}
		attribution.Chain = append(attribution.Chain, cause)
	}
	return attribution
}

// AttributionStats counts root causes by category across attributions.
func AttributionStats(attributions []CaseAttribution) map[FailureCategory]int {
	stats := make(map[FailureCategory]int)
	for _, attribution := range attributions {
		for _, cause := range attribution.RootCauses {
			stats[cause.Category]++
		}
	}
	return stats
}

// buildLossHints converts attributed root causes of baseline train failures
// into engine loss hints: only root causes are injected so the optimizer
// works on causes, not symptoms. The hint text carries the chain summary.
func buildLossHints(attributions []CaseAttribution) []promptiterengine.LossHint {
	hints := make([]promptiterengine.LossHint, 0, len(attributions))
	for _, attribution := range attributions {
		for _, cause := range attribution.RootCauses {
			if cause.Metric == "" {
				continue
			}
			severity := lossSeverityByCategory[cause.Category]
			reason := fmt.Sprintf("[%s] %s", cause.Category, cause.Evidence)
			if summary := attribution.ChainSummary(); summary != "" {
				reason += "（" + summary + "）"
			}
			hints = append(hints, promptiterengine.LossHint{
				EvalCaseID: attribution.EvalCaseID,
				MetricName: cause.Metric,
				Severity:   severity,
				Reason:     reason,
			})
		}
	}
	return hints
}
