//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const toolArgumentEpsilon = 1e-6

type attributionFinding struct {
	category   FailureCategory
	reason     string
	evidence   []EvidenceReference
	priority   int
	confidence float64
}

// AttributeFailure classifies one failed metric using provenance-bound,
// structural evidence before considering evaluator prose.
func AttributeFailure(input AttributionInput) FailureAttribution {
	result := baseAttribution(input)
	if category, sufficiency, reason, evidence, invalid := validateAttributionInput(input); invalid {
		result.PrimaryCategory = category
		result.Reason = reason
		result.Evidence = appendEvidence(result.Evidence, evidence...)
		result.EvidenceSufficiency = sufficiency
		return finishAttribution(result)
	}

	structural, ambiguous := structuralFindings(input.Case)
	if ambiguous != nil {
		result.PrimaryCategory = FailureAmbiguousEvidence
		result.Reason = ambiguous.reason
		result.Evidence = appendEvidence(result.Evidence, ambiguous.evidence...)
		result.EvidenceSufficiency = EvidenceAmbiguous
		return finishAttribution(result)
	}

	expectedResponse := strings.TrimSpace(input.Case.ExpectedResponse)
	actualResponse := strings.TrimSpace(input.Case.FinalResponse)
	textCategories, textEvidence, textConflict := proseCategories(
		input.Metric,
		expectedResponse != "" && expectedResponse != actualResponse,
	)
	strong := make([]attributionFinding, 0, len(structural))
	var response *attributionFinding
	for i := range structural {
		if structural[i].category == FailureResponseMismatch {
			finding := structural[i]
			response = &finding
			continue
		}
		strong = append(strong, structural[i])
	}

	switch {
	case len(strong) > 0:
		sort.SliceStable(strong, func(i, j int) bool {
			return strong[i].priority < strong[j].priority
		})
		primary := strong[0]
		result.PrimaryCategory = primary.category
		result.Reason = primary.reason
		result.Confidence = primary.confidence
		result.EvidenceSufficiency = EvidenceSufficient
		result.Evidence = appendEvidence(result.Evidence, primary.evidence...)
		for _, finding := range strong[1:] {
			result.SecondaryCategories = appendUniqueCategory(
				result.SecondaryCategories,
				finding.category,
			)
			result.Evidence = appendEvidence(result.Evidence, finding.evidence...)
		}
		if response != nil {
			result.SecondaryCategories = appendUniqueCategory(
				result.SecondaryCategories,
				response.category,
			)
			result.Evidence = appendEvidence(result.Evidence, response.evidence...)
		}
	case textConflict || len(textCategories) > 1:
		result.PrimaryCategory = FailureAmbiguousEvidence
		if textConflict {
			result.Reason = "evaluator evidence both affirms and rules out a failure category"
		} else {
			result.Reason = "equally specific evaluator evidence supports multiple failure categories"
		}
		result.Confidence = 0
		result.EvidenceSufficiency = EvidenceAmbiguous
		result.Evidence = appendEvidence(result.Evidence, textEvidence...)
		if response != nil {
			result.Evidence = appendEvidence(result.Evidence, response.evidence...)
		}
	case len(textCategories) == 1:
		result.PrimaryCategory = textCategories[0]
		result.Reason = "evaluator evidence explicitly identifies the failure category"
		result.Confidence = 0.72
		result.EvidenceSufficiency = EvidencePartial
		result.Evidence = appendEvidence(result.Evidence, textEvidence...)
		if response != nil && response.category != result.PrimaryCategory {
			result.SecondaryCategories = appendUniqueCategory(
				result.SecondaryCategories,
				response.category,
			)
			result.Evidence = appendEvidence(result.Evidence, response.evidence...)
		}
	case response != nil:
		result.PrimaryCategory = response.category
		result.Reason = response.reason
		result.Confidence = response.confidence
		result.EvidenceSufficiency = EvidenceSufficient
		result.Evidence = appendEvidence(result.Evidence, response.evidence...)
	default:
		result.PrimaryCategory = FailureInsufficient
		result.Reason = "failed metric has no structurally attributable or explicit evaluator evidence"
		result.Confidence = 0
		result.EvidenceSufficiency = EvidenceInsufficient
		result.Evidence = appendEvidence(
			result.Evidence,
			makeEvidence(
				"metric.status",
				"metric",
				fmt.Sprintf(
					"metric %q ended with status %q",
					input.Metric.MetricName,
					input.Metric.Status,
				),
			),
		)
	}
	return finishAttribution(result)
}

func baseAttribution(input AttributionInput) FailureAttribution {
	result := FailureAttribution{
		EvalSetID:           input.Case.EvalSetID,
		EvalCaseID:          input.Case.CaseID,
		MetricName:          input.Metric.MetricName,
		PrimaryCategory:     FailureInsufficient,
		Severity:            failureSeverity(input.Case),
		Confidence:          0,
		EvidenceSufficiency: EvidenceInsufficient,
	}
	if input.Snapshot != nil {
		if result.EvalSetID == "" {
			result.EvalSetID = input.Snapshot.Provenance.EvalSetID
		}
		result.EvaluationRunID = input.Snapshot.Provenance.RunID
		result.ProfileHash = input.Snapshot.Provenance.ProfileHash
	}
	return result
}

func failureSeverity(result CaseResult) FailureSeverity {
	switch {
	case result.HardFailure:
		return FailureSeverityP0
	case result.Critical:
		return FailureSeverityP1
	default:
		return FailureSeverityP2
	}
}

func finishAttribution(result FailureAttribution) FailureAttribution {
	if strings.TrimSpace(result.Reason) == "" {
		result.Reason = "failed metric could not be attributed"
	}
	if len(result.Evidence) == 0 {
		result.Evidence = appendEvidence(
			nil,
			makeEvidence(
				"attribution.fallback",
				"diagnostic",
				"no bounded case evidence was available",
			),
		)
	}
	if result.Confidence < 0 {
		result.Confidence = 0
	}
	if result.Confidence > 1 {
		result.Confidence = 1
	}
	return result
}

//nolint:gocyclo // Keep fail-closed evidence invariants linear and auditable.
func validateAttributionInput(
	input AttributionInput,
) (
	FailureCategory,
	EvidenceSufficiency,
	string,
	[]EvidenceReference,
	bool,
) {
	snapshot := input.Snapshot
	if snapshot == nil {
		return invalidAttribution("evaluation snapshot is nil")
	}
	if snapshot.Status != EvaluationCompleted {
		return invalidAttribution(fmt.Sprintf(
			"evaluation snapshot status %q is not completed",
			snapshot.Status,
		))
	}
	provenance := snapshot.Provenance
	switch {
	case strings.TrimSpace(provenance.RunID) == "":
		return invalidAttribution("evaluation run id is empty")
	case strings.TrimSpace(provenance.ProfileHash) == "":
		return invalidAttribution("evaluation profile hash is empty")
	case strings.TrimSpace(provenance.EvalSetID) == "":
		return invalidAttribution("evaluation provenance eval set id is empty")
	case input.Case.EvalSetID != provenance.EvalSetID:
		return invalidAttribution(fmt.Sprintf(
			"case eval set %q does not match snapshot eval set %q",
			input.Case.EvalSetID,
			provenance.EvalSetID,
		))
	case strings.TrimSpace(input.Case.CaseID) == "":
		return invalidAttribution("case id is empty")
	case strings.TrimSpace(input.Metric.MetricName) == "":
		return invalidAttribution("metric name is empty")
	case !isFinite(input.Metric.Score) || !isFinite(input.Metric.Threshold):
		return invalidAttribution("metric score or threshold is not finite")
	case !isComparableResultStatus(input.Case.Status):
		return invalidAttribution(fmt.Sprintf(
			"case status %q is not attributable",
			input.Case.Status,
		))
	case !statusMatchesPassed(input.Case.Status, input.Case.Passed):
		return invalidAttribution("case status and passed flag disagree")
	case !isComparableResultStatus(input.Metric.Status):
		return invalidAttribution(fmt.Sprintf(
			"metric status %q is not attributable",
			input.Metric.Status,
		))
	case !statusMatchesPassed(input.Metric.Status, input.Metric.Passed):
		return invalidAttribution("metric status and passed flag disagree")
	case input.Case.Passed || input.Metric.Passed:
		return invalidAttribution("attribution requires a failed case and failed metric")
	}
	if !containsExactlyOnce(snapshot.Inventory.CaseIDs, input.Case.CaseID) {
		return invalidAttribution(fmt.Sprintf(
			"case %q is not uniquely bound to the expected inventory",
			input.Case.CaseID,
		))
	}
	if !containsExactlyOnce(snapshot.Inventory.MetricNames, input.Metric.MetricName) {
		return invalidAttribution(fmt.Sprintf(
			"metric %q is not uniquely bound to the expected inventory",
			input.Metric.MetricName,
		))
	}

	caseMatches := matchingCases(snapshot.Cases, input.Case.EvalSetID, input.Case.CaseID)
	if len(caseMatches) > 1 {
		return ambiguousAttribution(fmt.Sprintf(
			"snapshot contains duplicate case evidence for %s/%s",
			input.Case.EvalSetID,
			input.Case.CaseID,
		))
	}
	if len(caseMatches) == 0 {
		return invalidAttribution(fmt.Sprintf(
			"snapshot does not contain case evidence for %s/%s",
			input.Case.EvalSetID,
			input.Case.CaseID,
		))
	}
	if !reflect.DeepEqual(caseMatches[0], input.Case) {
		return invalidAttribution(fmt.Sprintf(
			"provided case evidence does not match snapshot case %s/%s",
			input.Case.EvalSetID,
			input.Case.CaseID,
		))
	}

	metricMatches := matchingMetrics(input.Case.Metrics, input.Metric.MetricName)
	if len(metricMatches) > 1 {
		return ambiguousAttribution(fmt.Sprintf(
			"case %s/%s contains duplicate metric evidence for %q",
			input.Case.EvalSetID,
			input.Case.CaseID,
			input.Metric.MetricName,
		))
	}
	if len(metricMatches) == 0 {
		return invalidAttribution(fmt.Sprintf(
			"case %s/%s does not contain metric %q",
			input.Case.EvalSetID,
			input.Case.CaseID,
			input.Metric.MetricName,
		))
	}
	if !reflect.DeepEqual(metricMatches[0], input.Metric) {
		return invalidAttribution(fmt.Sprintf(
			"provided metric evidence does not match snapshot metric %q",
			input.Metric.MetricName,
		))
	}
	for _, rubric := range input.Metric.RubricScores {
		if !isFinite(rubric.Score) {
			return invalidAttribution(fmt.Sprintf(
				"rubric %q score is not finite",
				rubric.ID,
			))
		}
	}
	return "", "", "", nil, false
}

func invalidAttribution(
	reason string,
) (
	FailureCategory,
	EvidenceSufficiency,
	string,
	[]EvidenceReference,
	bool,
) {
	return FailureInsufficient,
		EvidenceInsufficient,
		"attribution evidence binding failed: " + reason,
		[]EvidenceReference{
			makeEvidence("attribution.binding", "provenance", reason),
		},
		true
}

func ambiguousAttribution(
	reason string,
) (
	FailureCategory,
	EvidenceSufficiency,
	string,
	[]EvidenceReference,
	bool,
) {
	return FailureAmbiguousEvidence,
		EvidenceAmbiguous,
		"attribution evidence is ambiguous: " + reason,
		[]EvidenceReference{
			makeEvidence("attribution.binding", "provenance", reason),
		},
		true
}

func containsExactlyOnce(values []string, target string) bool {
	count := 0
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count == 1
}

func matchingCases(cases []CaseResult, evalSetID, caseID string) []CaseResult {
	matches := make([]CaseResult, 0, 1)
	for _, item := range cases {
		if item.EvalSetID == evalSetID && item.CaseID == caseID {
			matches = append(matches, item)
		}
	}
	return matches
}

func matchingMetrics(metrics []MetricResult, metricName string) []MetricResult {
	matches := make([]MetricResult, 0, 1)
	for _, item := range metrics {
		if item.MetricName == metricName {
			matches = append(matches, item)
		}
	}
	return matches
}

func structuralFindings(
	result CaseResult,
) ([]attributionFinding, *attributionFinding) {
	findings := make([]attributionFinding, 0, 6)
	if expected := strings.TrimSpace(result.ExpectedRoute); expected != "" {
		actual := strings.TrimSpace(result.Route)
		if actual != "" && actual != expected {
			evidence := []EvidenceReference{
				makeEvidence(
					"case.route",
					"route",
					fmt.Sprintf("expected route %q; actual route %q", expected, actual),
				),
			}
			if trace := summarizeTrace(result.Trace); trace != "" {
				evidence = append(
					evidence,
					makeEvidence("case.trace", "trace", trace),
				)
			}
			findings = append(findings, attributionFinding{
				category:   FailureWrongRoute,
				reason:     "execution selected a different route than expected",
				evidence:   evidence,
				priority:   0,
				confidence: 0.99,
			})
		}
	}

	toolFinding, toolAmbiguity := compareTools(
		result.ExpectedTools,
		result.ToolTrajectory,
	)
	if toolAmbiguity != nil {
		// A proven route mismatch remains the primary structural cause even if
		// downstream tool evidence is malformed or internally ambiguous.
		if len(findings) == 0 {
			return nil, toolAmbiguity
		}
		findings[0].evidence = appendEvidence(
			findings[0].evidence,
			toolAmbiguity.evidence...,
		)
	}
	if toolFinding != nil {
		findings = append(findings, *toolFinding)
	}

	if issue := structuredOutputIssue(
		result.ExpectedResponse,
		result.StructuredOutput,
	); result.ExpectStructured && issue != "" {
		findings = append(findings, attributionFinding{
			category: FailureInvalidFormat,
			reason:   issue,
			evidence: []EvidenceReference{
				makeEvidence(
					"case.structured_output",
					"structured_output",
					fmt.Sprintf("%s; actual %q", issue, result.StructuredOutput),
				),
			},
			priority:   3,
			confidence: 0.97,
		})
	}

	if missing := missingExpectedFacts(
		result.ExpectedFacts,
		result.FinalResponse,
	); len(missing) > 0 {
		findings = append(findings, attributionFinding{
			category: FailureKnowledgeRecall,
			reason:   "final response did not affirm expected knowledge",
			evidence: []EvidenceReference{
				makeEvidence(
					"case.expected_facts",
					"knowledge",
					"missing facts: "+strings.Join(missing, ", "),
				),
				makeEvidence(
					"case.final_response",
					"response",
					result.FinalResponse,
				),
			},
			priority:   4,
			confidence: 0.92,
		})
	}

	if expected, actual := strings.TrimSpace(result.ExpectedResponse),
		strings.TrimSpace(result.FinalResponse); expected != "" &&
		expected != actual {
		findings = append(findings, attributionFinding{
			category: FailureResponseMismatch,
			reason:   "final response does not match the expected response",
			evidence: []EvidenceReference{
				makeEvidence(
					"case.response",
					"response",
					fmt.Sprintf("expected %q; actual %q", expected, actual),
				),
			},
			priority:   5,
			confidence: 0.9,
		})
	}
	return findings, nil
}

func compareTools(
	expectedInput []ToolCall,
	actualInput []ToolCall,
) (*attributionFinding, *attributionFinding) {
	expected, err := toolsInExecutionOrder(expectedInput)
	if err != nil {
		return nil, ambiguousToolEvidence("expected", err)
	}
	actual, err := toolsInExecutionOrder(actualInput)
	if err != nil {
		return nil, ambiguousToolEvidence("actual", err)
	}
	common := len(expected)
	if len(actual) < common {
		common = len(actual)
	}
	for i := 0; i < common; i++ {
		if expected[i].Name != actual[i].Name {
			return &attributionFinding{
				category: FailureWrongTool,
				reason:   "tool name or execution order differs from the expected trajectory",
				evidence: []EvidenceReference{
					makeEvidence(
						"case.tool_trajectory",
						"tool_trajectory",
						fmt.Sprintf(
							"step %d expected tool %q; actual tool %q",
							i+1,
							expected[i].Name,
							actual[i].Name,
						),
					),
				},
				priority:   1,
				confidence: 0.99,
			}, nil
		}
	}
	if len(expected) != len(actual) {
		return &attributionFinding{
			category: FailureWrongTool,
			reason:   "tool trajectory has missing or unexpected calls",
			evidence: []EvidenceReference{
				makeEvidence(
					"case.tool_trajectory",
					"tool_trajectory",
					fmt.Sprintf(
						"expected tools %s; actual tools %s",
						toolNames(expected),
						toolNames(actual),
					),
				),
			},
			priority:   1,
			confidence: 0.99,
		}, nil
	}
	for i := range expected {
		equal, compareErr := equalJSONLike(
			expected[i].Arguments,
			actual[i].Arguments,
			toolArgumentEpsilon,
		)
		if compareErr != nil {
			return nil, ambiguousToolEvidence(
				"arguments",
				fmt.Errorf("tool %q: %w", expected[i].Name, compareErr),
			)
		}
		if !equal {
			return &attributionFinding{
				category: FailureWrongArguments,
				reason:   "tool call arguments differ from the expected arguments",
				evidence: []EvidenceReference{
					makeEvidence(
						"case.tool_arguments",
						"tool_arguments",
						fmt.Sprintf(
							"tool %q expected arguments %s; actual arguments %s",
							expected[i].Name,
							summarizeJSONLike(expected[i].Arguments),
							summarizeJSONLike(actual[i].Arguments),
						),
					),
				},
				priority:   2,
				confidence: 0.98,
			}, nil
		}
	}
	return nil, nil
}

func ambiguousToolEvidence(side string, err error) *attributionFinding {
	return &attributionFinding{
		category: FailureAmbiguousEvidence,
		reason:   "tool trajectory evidence is ambiguous",
		evidence: []EvidenceReference{
			makeEvidence(
				"case.tool_trajectory",
				"tool_trajectory",
				fmt.Sprintf("%s tool trajectory: %v", side, err),
			),
		},
	}
}

func toolsInExecutionOrder(input []ToolCall) ([]ToolCall, error) {
	tools := append([]ToolCall(nil), input...)
	hasSequence := false
	hasMissingSequence := false
	seen := make(map[int]struct{}, len(tools))
	for _, item := range tools {
		if strings.TrimSpace(item.Name) == "" {
			return nil, fmt.Errorf("tool name is empty")
		}
		if item.Sequence <= 0 {
			hasMissingSequence = true
			continue
		}
		hasSequence = true
		if _, ok := seen[item.Sequence]; ok {
			return nil, fmt.Errorf("duplicate sequence %d", item.Sequence)
		}
		seen[item.Sequence] = struct{}{}
	}
	if hasSequence && hasMissingSequence {
		return nil, fmt.Errorf("mixed present and absent sequence numbers")
	}
	if hasSequence {
		sort.SliceStable(tools, func(i, j int) bool {
			return tools[i].Sequence < tools[j].Sequence
		})
	}
	return tools, nil
}

func toolNames(tools []ToolCall) string {
	names := make([]string, 0, len(tools))
	for _, item := range tools {
		names = append(names, item.Name)
	}
	data, err := json.Marshal(names)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func equalJSONLike(actual, expected any, tolerance float64) (bool, error) {
	actual, err := normalizeJSONLike(actual)
	if err != nil {
		return false, fmt.Errorf("normalize actual arguments: %w", err)
	}
	expected, err = normalizeJSONLike(expected)
	if err != nil {
		return false, fmt.Errorf("normalize expected arguments: %w", err)
	}
	return equalNormalizedValue(actual, expected, tolerance), nil
}

//nolint:gocyclo // Recursive JSON-like type normalization is clearer as one type switch.
func normalizeJSONLike(value any) (any, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		return decodeJSONValue(typed)
	case []byte:
		return decodeJSONValue(typed)
	case string:
		trimmed := strings.TrimSpace(typed)
		if json.Valid([]byte(trimmed)) &&
			(strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) {
			return decodeJSONValue([]byte(trimmed))
		}
		return typed, nil
	case json.Number:
		if !json.Valid([]byte(typed.String())) {
			return nil, fmt.Errorf("invalid JSON number %q", typed)
		}
		if _, ok := new(big.Rat).SetString(typed.String()); !ok {
			return nil, fmt.Errorf("invalid JSON number %q", typed)
		}
		return typed, nil
	case float64:
		if !isFinite(typed) {
			return nil, fmt.Errorf("floating-point value is not finite")
		}
		return typed, nil
	case float32:
		if !isFinite(float64(typed)) {
			return nil, fmt.Errorf("floating-point value is not finite")
		}
		return typed, nil
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return typed, nil
	case map[string]any:
		normalized := make(map[string]any, len(typed))
		for key, item := range typed {
			var err error
			normalized[key], err = normalizeJSONLike(item)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", key, err)
			}
		}
		return normalized, nil
	case []any:
		normalized := make([]any, len(typed))
		for i, item := range typed {
			var err error
			normalized[i], err = normalizeJSONLike(item)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
		}
		return normalized, nil
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		normalized, err := decodeJSONValue(data)
		if err != nil {
			return nil, err
		}
		return restoreFloatingValues(reflect.ValueOf(value), normalized), nil
	}
}

//nolint:gocyclo // Recursive reflected-shape restoration must cover each supported Go kind.
func restoreFloatingValues(source reflect.Value, normalized any) any {
	for source.IsValid() &&
		(source.Kind() == reflect.Interface || source.Kind() == reflect.Pointer) {
		if source.IsNil() {
			return normalized
		}
		source = source.Elem()
	}
	if !source.IsValid() {
		return normalized
	}
	switch source.Kind() {
	case reflect.Float32:
		return float32(source.Float())
	case reflect.Float64:
		return source.Float()
	case reflect.Map:
		object, ok := normalized.(map[string]any)
		if !ok {
			return normalized
		}
		for _, key := range source.MapKeys() {
			name, ok := reflectedJSONMapKey(key)
			if !ok {
				continue
			}
			if item, exists := object[name]; exists {
				object[name] = restoreFloatingValues(
					source.MapIndex(key),
					item,
				)
			}
		}
		return object
	case reflect.Slice, reflect.Array:
		items, ok := normalized.([]any)
		if !ok {
			return normalized
		}
		for i := 0; i < source.Len() && i < len(items); i++ {
			items[i] = restoreFloatingValues(source.Index(i), items[i])
		}
		return items
	case reflect.Struct:
		object, ok := normalized.(map[string]any)
		if !ok {
			return normalized
		}
		sourceType := source.Type()
		for i := 0; i < source.NumField(); i++ {
			fieldType := sourceType.Field(i)
			if fieldType.PkgPath != "" {
				continue
			}
			tagName := strings.Split(fieldType.Tag.Get("json"), ",")[0]
			if tagName == "-" {
				continue
			}
			if fieldType.Anonymous && tagName == "" {
				restored := restoreFloatingValues(source.Field(i), object)
				if restoredObject, ok := restored.(map[string]any); ok {
					object = restoredObject
				}
				continue
			}
			if tagName == "" {
				tagName = fieldType.Name
			}
			if item, exists := object[tagName]; exists {
				object[tagName] = restoreFloatingValues(
					source.Field(i),
					item,
				)
			}
		}
		return object
	default:
		return normalized
	}
}

func reflectedJSONMapKey(value reflect.Value) (string, bool) {
	for value.IsValid() &&
		(value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return "", false
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return "", false
	}
	switch value.Kind() {
	case reflect.String:
		return value.String(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16,
		reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(value.Uint(), 10), true
	default:
		return "", false
	}
}

func decodeJSONValue(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return nil, fmt.Errorf("multiple JSON values")
	} else if err != io.EOF {
		return nil, err
	}
	return value, nil
}

func equalNormalizedValue(actual, expected any, tolerance float64) bool {
	if actualNumber, ok := comparableNumberValue(actual); ok {
		expectedNumber, expectedOK := comparableNumberValue(expected)
		if !expectedOK {
			return false
		}
		if !actualNumber.floating && !expectedNumber.floating {
			return actualNumber.exact.Cmp(expectedNumber.exact) == 0
		}
		actualRat, actualOK := actualNumber.rat()
		expectedRat, expectedOK := expectedNumber.rat()
		toleranceRat := new(big.Rat).SetFloat64(tolerance)
		if !actualOK || !expectedOK || toleranceRat == nil {
			return false
		}
		difference := new(big.Rat).Sub(actualRat, expectedRat)
		difference.Abs(difference)
		return difference.Cmp(toleranceRat) <= 0
	}
	switch actualValue := actual.(type) {
	case map[string]any:
		expectedValue, ok := expected.(map[string]any)
		if !ok || len(actualValue) != len(expectedValue) {
			return false
		}
		for key, value := range actualValue {
			expectedItem, exists := expectedValue[key]
			if !exists || !equalNormalizedValue(value, expectedItem, tolerance) {
				return false
			}
		}
		return true
	case []any:
		expectedValue, ok := expected.([]any)
		if !ok || len(actualValue) != len(expectedValue) {
			return false
		}
		for i := range actualValue {
			if !equalNormalizedValue(actualValue[i], expectedValue[i], tolerance) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(actual, expected)
	}
}

type comparableNumber struct {
	exact       *big.Rat
	approximate float64
	floating    bool
}

func comparableNumberValue(value any) (comparableNumber, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, ok := new(big.Rat).SetString(typed.String())
		return comparableNumber{exact: number}, ok
	case float64:
		return comparableNumber{approximate: typed, floating: true}, isFinite(typed)
	case float32:
		number := float64(typed)
		return comparableNumber{approximate: number, floating: true}, isFinite(number)
	case int:
		return exactInteger(strconv.FormatInt(int64(typed), 10))
	case int8:
		return exactInteger(strconv.FormatInt(int64(typed), 10))
	case int16:
		return exactInteger(strconv.FormatInt(int64(typed), 10))
	case int32:
		return exactInteger(strconv.FormatInt(int64(typed), 10))
	case int64:
		return exactInteger(strconv.FormatInt(typed, 10))
	case uint:
		return exactInteger(strconv.FormatUint(uint64(typed), 10))
	case uint8:
		return exactInteger(strconv.FormatUint(uint64(typed), 10))
	case uint16:
		return exactInteger(strconv.FormatUint(uint64(typed), 10))
	case uint32:
		return exactInteger(strconv.FormatUint(uint64(typed), 10))
	case uint64:
		return exactInteger(strconv.FormatUint(typed, 10))
	default:
		return comparableNumber{}, false
	}
}

func exactInteger(value string) (comparableNumber, bool) {
	integer, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return comparableNumber{}, false
	}
	return comparableNumber{exact: new(big.Rat).SetInt(integer)}, true
}

func (number comparableNumber) rat() (*big.Rat, bool) {
	if number.floating {
		value := new(big.Rat).SetFloat64(number.approximate)
		return value, value != nil
	}
	if number.exact == nil {
		return nil, false
	}
	return new(big.Rat).Set(number.exact), true
}

func summarizeJSONLike(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

func structuredOutputIssue(expected, actual string) string {
	actualValue, err := decodeJSONValue([]byte(actual))
	if err != nil {
		return "structured output is not valid single-value JSON"
	}
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return ""
	}
	expectedValue, err := decodeJSONValue([]byte(expected))
	if err != nil {
		// A malformed oracle cannot safely impose a schema on valid output.
		return ""
	}
	return structuredShapeIssue("$", expectedValue, actualValue)
}

func structuredShapeIssue(path string, expected, actual any) string {
	expectedKind := jsonValueKind(expected)
	actualKind := jsonValueKind(actual)
	if expectedKind != actualKind {
		return fmt.Sprintf(
			"structured output %s has kind %s; expected %s",
			path,
			actualKind,
			expectedKind,
		)
	}
	expectedObject, ok := expected.(map[string]any)
	if !ok {
		return ""
	}
	actualObject := actual.(map[string]any)
	keys := make([]string, 0, len(expectedObject))
	for key := range expectedObject {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		actualField, exists := actualObject[key]
		fieldPath := path + "." + key
		if !exists {
			return fmt.Sprintf(
				"structured output is missing required field %s",
				fieldPath,
			)
		}
		if issue := structuredShapeIssue(
			fieldPath,
			expectedObject[key],
			actualField,
		); issue != "" {
			return issue
		}
	}
	return ""
}

func jsonValueKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case json.Number, float32, float64,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return "number"
	case bool:
		return "boolean"
	default:
		return "unknown"
	}
}

func missingExpectedFacts(expected []string, response string) []string {
	missing := make([]string, 0, len(expected))
	for _, fact := range expected {
		fact = strings.TrimSpace(fact)
		if fact != "" && !containsAffirmedFact(response, fact) {
			missing = append(missing, fact)
		}
	}
	return missing
}

func containsAffirmedFact(response, fact string) bool {
	responseRunes := []rune(strings.ToLower(response))
	factRunes := []rune(strings.ToLower(strings.TrimSpace(fact)))
	if len(factRunes) == 0 || len(factRunes) > len(responseRunes) {
		return false
	}
	for start := 0; start+len(factRunes) <= len(responseRunes); start++ {
		if !runesEqual(responseRunes[start:start+len(factRunes)], factRunes) {
			continue
		}
		end := start + len(factRunes)
		if !factTokenBoundary(responseRunes, factRunes, start, end) {
			continue
		}
		prefixStart := runeClauseStart(responseRunes, start)
		suffixEnd := runeClauseEnd(responseRunes, end)
		prefix := string(responseRunes[prefixStart:start])
		suffix := string(responseRunes[end:suffixEnd])
		if !phraseMentionNegated(prefix, suffix) {
			return true
		}
	}
	return false
}

func runesEqual(left, right []rune) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func factTokenBoundary(
	response []rune,
	fact []rune,
	start, end int,
) bool {
	if boundarySensitiveRune(fact[0]) &&
		start > 0 && tokenRune(response[start-1]) {
		return false
	}
	if boundarySensitiveRune(fact[len(fact)-1]) &&
		end < len(response) && tokenRune(response[end]) {
		return false
	}
	return true
}

func boundarySensitiveRune(value rune) bool {
	return tokenRune(value) && !unicode.Is(unicode.Han, value)
}

func tokenRune(value rune) bool {
	return value == '_' || unicode.IsLetter(value) || unicode.IsDigit(value)
}

func summarizeTrace(trace []TraceStep) string {
	const maxSteps = 6
	parts := make([]string, 0, min(len(trace), maxSteps))
	for i, step := range trace {
		if i >= maxSteps {
			parts = append(parts, "…")
			break
		}
		fields := []string{step.StepID}
		if step.NodeID != "" {
			fields = append(fields, "node="+step.NodeID)
		}
		if step.AgentName != "" {
			fields = append(fields, "agent="+step.AgentName)
		}
		if step.Branch != "" {
			fields = append(fields, "branch="+step.Branch)
		}
		if step.Error != "" {
			fields = append(fields, "error="+step.Error)
		}
		parts = append(parts, strings.Join(fields, ","))
	}
	return strings.Join(parts, " -> ")
}

func proseCategories(
	metric MetricResult,
	allowResponseMismatch bool,
) ([]FailureCategory, []EvidenceReference, bool) {
	categories := make([]FailureCategory, 0, 2)
	negated := make([]FailureCategory, 0, 2)
	evidence := make([]EvidenceReference, 0, 3)
	addText := func(id, kind, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		affirmedMatches, negatedMatches := classifyProse(value)
		if !allowResponseMismatch {
			affirmedMatches = removeCategory(
				affirmedMatches,
				FailureResponseMismatch,
			)
			negatedMatches = removeCategory(
				negatedMatches,
				FailureResponseMismatch,
			)
		}
		if len(affirmedMatches) == 0 && len(negatedMatches) == 0 {
			return
		}
		evidence = appendEvidence(evidence, makeEvidence(id, kind, value))
		for _, category := range affirmedMatches {
			categories = appendUniqueCategory(categories, category)
		}
		for _, category := range negatedMatches {
			negated = appendUniqueCategory(negated, category)
		}
	}
	addText("metric.reason", "metric_reason", metric.Reason)
	for i, rubric := range metric.RubricScores {
		addText(
			fmt.Sprintf("metric.rubric.%d", i),
			"rubric",
			rubric.Reason,
		)
	}
	conflict := false
	for _, category := range negated {
		if containsCategory(categories, category) {
			conflict = true
			categories = removeCategory(categories, category)
		}
	}
	return categories, evidence, conflict
}

func classifyProse(
	value string,
) ([]FailureCategory, []FailureCategory) {
	patterns := []struct {
		category FailureCategory
		phrases  []string
	}{
		{
			category: FailureWrongRoute,
			phrases: []string{
				"wrong route", "incorrect route", "route mismatch",
				"routed to the wrong", "路由错误", "错误路由", "路由不匹配",
			},
		},
		{
			category: FailureWrongTool,
			phrases: []string{
				"wrong tool", "incorrect tool", "tool mismatch",
				"unexpected tool", "missing tool", "工具错误", "错误工具",
				"工具不匹配",
			},
		},
		{
			category: FailureWrongArguments,
			phrases: []string{
				"wrong argument", "incorrect argument", "argument mismatch",
				"invalid argument", "wrong parameter", "参数错误", "参数不匹配",
			},
		},
		{
			category: FailureInvalidFormat,
			phrases: []string{
				"invalid json", "malformed json", "not valid json",
				"invalid format", "format error", "格式错误", "无效格式",
			},
		},
		{
			category: FailureKnowledgeRecall,
			phrases: []string{
				"knowledge recall", "failed to recall", "missing knowledge",
				"knowledge-recall", "知识召回", "知识不足",
			},
		},
		{
			category: FailureResponseMismatch,
			phrases: []string{
				"response mismatch", "answer mismatch", "final answer is wrong",
				"incorrect final answer", "最终回复不匹配", "答案不匹配",
			},
		},
	}
	affirmed := make([]FailureCategory, 0, 2)
	negated := make([]FailureCategory, 0, 2)
	for _, pattern := range patterns {
		for _, phrase := range pattern.phrases {
			phraseAffirmed, phraseNegated := phrasePolarities(value, phrase)
			if phraseAffirmed {
				affirmed = appendUniqueCategory(affirmed, pattern.category)
			}
			if phraseNegated {
				negated = appendUniqueCategory(negated, pattern.category)
			}
		}
	}
	return affirmed, negated
}

func phrasePolarities(value, phrase string) (bool, bool) {
	value = strings.ToLower(value)
	phrase = strings.ToLower(phrase)
	searchFrom := 0
	affirmed := false
	negated := false
	for {
		offset := strings.Index(value[searchFrom:], phrase)
		if offset < 0 {
			return affirmed, negated
		}
		index := searchFrom + offset
		phraseEnd := index + len(phrase)
		prefix, suffix := clauseContext(value, index, phraseEnd)
		if phraseMentionNegated(prefix, suffix) {
			negated = true
		} else {
			affirmed = true
		}
		searchFrom = phraseEnd
	}
}

func phraseMentionNegated(prefix, suffix string) bool {
	prefix = strings.TrimSpace(afterLastContrast(strings.ToLower(prefix)))
	suffix = strings.TrimSpace(strings.ToLower(suffix))
	for _, negation := range []string{
		"was ruled out", "is ruled out", "has been ruled out",
		"can be ruled out", "could be ruled out",
		"was not", "is not", "isn't", "wasn't",
		"is not the issue", "was not the issue",
		"可以排除", "可排除", "已排除", "已经排除",
		"不是原因", "并非", "不是", "不能", "不被", "不受",
		"不予", "不提供", "不支持",
	} {
		if strings.HasPrefix(suffix, negation) {
			return true
		}
	}
	for _, negation := range []string{
		"not", "not a", "not an", "no", "never",
		"isn't", "is not", "was not", "wasn't",
		"不是", "并非", "没有", "无", "未", "不能",
		"不被", "不受", "不予", "不提供", "不支持",
	} {
		if strings.HasSuffix(prefix, negation) {
			return true
		}
	}
	for _, scope := range []string{
		"no evidence of",
		"no evidence for",
		"no evidence that",
		"no evidence indicating",
		"no evidence suggesting",
		"no evidence to indicate",
		"no evidence to suggest",
		"no indication of",
		"no sign of",
		"without evidence of",
		"without indication of",
		"absence of evidence for",
		"ruled out",
		"rule out",
		"can rule out",
		"could rule out",
		"没有证据表明",
		"没有证据显示",
		"无证据表明",
		"无证据显示",
		"可以排除",
		"可排除",
		"已排除",
		"已经排除",
		"未发现",
	} {
		index := strings.LastIndex(prefix, scope)
		if index < 0 {
			continue
		}
		tail := strings.TrimSpace(prefix[index+len(scope):])
		if len([]rune(tail)) <= 64 {
			return true
		}
	}
	// Carry negation across a short coordinated list in the same clause.
	for _, negation := range []string{
		" not ", " no ", " isn't ", " is not ", " was not ",
		"不是", "并非", "没有",
	} {
		index := strings.LastIndex(" "+prefix, negation)
		if index < 0 {
			continue
		}
		tail := strings.TrimSpace((" " + prefix)[index+len(negation):])
		if len([]rune(tail)) > 64 {
			continue
		}
		paddedTail := " " + tail + " "
		if strings.Contains(paddedTail, " or ") ||
			strings.Contains(paddedTail, " and ") ||
			strings.Contains(tail, "或") ||
			strings.Contains(tail, "和") {
			return true
		}
	}
	return false
}

func afterLastContrast(value string) string {
	start := 0
	for _, boundary := range []string{
		" but ", " however ", " yet ", " nevertheless ", " nonetheless ",
		"但是", "但", "然而", "不过",
	} {
		if index := strings.LastIndex(value, boundary); index >= 0 {
			candidate := index + len(boundary)
			if candidate > start {
				start = candidate
			}
		}
	}
	return value[start:]
}

func clauseContext(value string, phraseStart, phraseEnd int) (string, string) {
	prefixStart := 0
	for index, current := range value[:phraseStart] {
		if isClauseBoundary(current) {
			prefixStart = index + utf8.RuneLen(current)
		}
	}
	suffixEnd := len(value)
	for index, current := range value[phraseEnd:] {
		if isClauseBoundary(current) {
			suffixEnd = phraseEnd + index
			break
		}
	}
	return value[prefixStart:phraseStart], value[phraseEnd:suffixEnd]
}

func runeClauseStart(value []rune, before int) int {
	for index := before - 1; index >= 0; index-- {
		if isClauseBoundary(value[index]) {
			return index + 1
		}
	}
	return 0
}

func runeClauseEnd(value []rune, after int) int {
	for index := after; index < len(value); index++ {
		if isClauseBoundary(value[index]) {
			return index
		}
	}
	return len(value)
}

func isClauseBoundary(value rune) bool {
	switch value {
	case ';', ',', '.', '!', '?', ':', '\n',
		'；', '，', '。', '！', '？', '：':
		return true
	default:
		return false
	}
}

func containsCategory(
	categories []FailureCategory,
	category FailureCategory,
) bool {
	for _, existing := range categories {
		if existing == category {
			return true
		}
	}
	return false
}

func removeCategory(
	categories []FailureCategory,
	category FailureCategory,
) []FailureCategory {
	filtered := categories[:0]
	for _, existing := range categories {
		if existing != category {
			filtered = append(filtered, existing)
		}
	}
	return filtered
}

func appendUniqueCategory(
	categories []FailureCategory,
	category FailureCategory,
) []FailureCategory {
	for _, existing := range categories {
		if existing == category {
			return categories
		}
	}
	return append(categories, category)
}
