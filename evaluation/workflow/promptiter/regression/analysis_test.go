//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAttributeFailureCategoriesAndBinding(t *testing.T) {
	tests := []struct {
		name string
		item CaseResult
		want FailureCategory
	}{
		{
			name: "wrong route wins over downstream symptoms",
			item: CaseResult{
				Route:         "search-agent",
				ExpectedRoute: "support-agent",
				ExpectedTools: []ToolCall{
					{Sequence: 1, Name: "lookup"},
				},
				ToolTrajectory: []ToolCall{
					{Sequence: 1, Name: "web_search"},
				},
				ExpectedResponse: "supported",
				FinalResponse:    "unknown",
			},
			want: FailureWrongRoute,
		},
		{
			name: "wrong tool including order",
			item: CaseResult{
				ExpectedTools: []ToolCall{
					{Sequence: 1, Name: "search"},
					{Sequence: 2, Name: "release"},
				},
				ToolTrajectory: []ToolCall{
					{Sequence: 1, Name: "release"},
					{Sequence: 2, Name: "search"},
				},
			},
			want: FailureWrongTool,
		},
		{
			name: "wrong arguments",
			item: CaseResult{
				ExpectedTools: []ToolCall{
					{
						Sequence:  1,
						Name:      "lookup",
						Arguments: map[string]any{"id": "A-17"},
					},
				},
				ToolTrajectory: []ToolCall{
					{
						Sequence:  1,
						Name:      "lookup",
						Arguments: map[string]any{"id": "A-71"},
					},
				},
			},
			want: FailureWrongArguments,
		},
		{
			name: "invalid structured output",
			item: CaseResult{
				ExpectStructured: true,
				StructuredOutput: `{"status":`,
			},
			want: FailureInvalidFormat,
		},
		{
			name: "knowledge recall",
			item: CaseResult{
				ExpectedFacts: []string{"7 days"},
				FinalResponse: "Returns are available.",
			},
			want: FailureKnowledgeRecall,
		},
		{
			name: "response mismatch",
			item: CaseResult{
				ExpectedResponse: "shipped",
				FinalResponse:    "processing",
			},
			want: FailureResponseMismatch,
		},
		{
			name: "insufficient evidence",
			item: CaseResult{},
			want: FailureInsufficient,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metric := failedMetric(
				"quality",
				"metric failed; tool, route, format, and knowledge are not diagnoses",
			)
			snapshot, item := boundAttributionInput(test.item, metric)
			got := AttributeFailure(AttributionInput{
				Snapshot: snapshot,
				Case:     item,
				Metric:   metric,
			})
			require.Equal(t, test.want, got.PrimaryCategory)
			require.NotEmpty(t, got.Reason)
			require.NotEmpty(t, got.Evidence)
			require.Equal(t, "quality", got.MetricName)
			require.Equal(t, snapshot.Provenance.RunID, got.EvaluationRunID)
			require.Equal(t, snapshot.Provenance.ProfileHash, got.ProfileHash)
			require.LessOrEqual(t, len(got.Evidence), maxEvidenceReferences)
		})
	}
}

func TestAttributeFailureStructuralEvidencePrecedenceAndNegation(t *testing.T) {
	metric := failedMetric(
		"quality",
		"tools and arguments are correct; this is not a route or invalid format issue; final answer is wrong",
	)
	snapshot, item := boundAttributionInput(CaseResult{
		Route:            "search",
		ExpectedRoute:    "release",
		ExpectedResponse: "A",
		FinalResponse:    "B",
	}, metric)
	got := AttributeFailure(AttributionInput{
		Snapshot: snapshot,
		Case:     item,
		Metric:   metric,
	})
	require.Equal(t, FailureWrongRoute, got.PrimaryCategory)
	require.Contains(t, got.SecondaryCategories, FailureResponseMismatch)
	require.NotContains(t, got.SecondaryCategories, FailureInvalidFormat)
}

func TestAttributeFailureDoesNotClassifyPostpositiveNegation(t *testing.T) {
	for _, reason := range []string{
		"Wrong tool was ruled out; the final answer is wrong.",
		"There is no evidence of a wrong tool; the final answer is wrong.",
		"Without evidence of an argument mismatch, the final answer is wrong.",
	} {
		t.Run(reason, func(t *testing.T) {
			metric := failedMetric("quality", reason)
			snapshot, item := boundAttributionInput(CaseResult{
				ExpectedResponse: "expected",
				FinalResponse:    "actual",
			}, metric)
			got := AttributeFailure(AttributionInput{
				Snapshot: snapshot,
				Case:     item,
				Metric:   metric,
			})
			require.Equal(t, FailureResponseMismatch, got.PrimaryCategory)
		})
	}
}

func TestAttributeFailureRequiresExpectedResponseOracle(t *testing.T) {
	t.Run("actual response alone is not an oracle", func(t *testing.T) {
		metric := failedMetric("quality", "The final answer is wrong.")
		snapshot, item := boundAttributionInput(CaseResult{
			FinalResponse: "an observed answer",
		}, metric)
		got := AttributeFailure(AttributionInput{
			Snapshot: snapshot,
			Case:     item,
			Metric:   metric,
		})
		require.Equal(t, FailureInsufficient, got.PrimaryCategory)
	})

	t.Run("explicit oracle supports response mismatch", func(t *testing.T) {
		metric := failedMetric("quality", "The final answer is wrong.")
		snapshot, item := boundAttributionInput(CaseResult{
			ExpectedResponse: "expected",
			FinalResponse:    "actual",
		}, metric)
		got := AttributeFailure(AttributionInput{
			Snapshot: snapshot,
			Case:     item,
			Metric:   metric,
		})
		require.Equal(t, FailureResponseMismatch, got.PrimaryCategory)
	})

	t.Run("matching oracle overrides evaluator prose", func(t *testing.T) {
		metric := failedMetric("quality", "The final answer is wrong.")
		snapshot, item := boundAttributionInput(CaseResult{
			ExpectedResponse: "same",
			FinalResponse:    "same",
		}, metric)
		got := AttributeFailure(AttributionInput{
			Snapshot: snapshot,
			Case:     item,
			Metric:   metric,
		})
		require.Equal(t, FailureInsufficient, got.PrimaryCategory)
	})
}

func TestAttributeFailureValidatesExpectedStructuredJSONShape(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		actual   string
		want     FailureCategory
	}{
		{
			name:     "top level kind",
			expected: `{"status":"ok"}`,
			actual:   `["ok"]`,
			want:     FailureInvalidFormat,
		},
		{
			name:     "required object field",
			expected: `{"status":"ok","count":1}`,
			actual:   `{"status":"ok"}`,
			want:     FailureInvalidFormat,
		},
		{
			name:     "object field type",
			expected: `{"status":"ok","count":1}`,
			actual:   `{"status":"ok","count":"1"}`,
			want:     FailureInvalidFormat,
		},
		{
			name:     "same shape different value",
			expected: `{"status":"ok","count":1}`,
			actual:   `{"status":"failed","count":2}`,
			want:     FailureResponseMismatch,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metric := failedMetric("quality", "")
			snapshot, item := boundAttributionInput(CaseResult{
				ExpectStructured: true,
				ExpectedResponse: test.expected,
				FinalResponse:    test.actual,
				StructuredOutput: test.actual,
			}, metric)
			got := AttributeFailure(AttributionInput{
				Snapshot: snapshot,
				Case:     item,
				Metric:   metric,
			})
			require.Equal(t, test.want, got.PrimaryCategory)
		})
	}
}

func TestAttributeFailureKnowledgeFactsRequireAffirmedBoundedMatch(t *testing.T) {
	tests := []struct {
		name     string
		fact     string
		response string
		want     FailureCategory
	}{
		{
			name:     "large numeric substring",
			fact:     "9007199254740993",
			response: "id=19007199254740993",
			want:     FailureKnowledgeRecall,
		},
		{
			name:     "word substring",
			fact:     "cat",
			response: "The values were concatenated.",
			want:     FailureKnowledgeRecall,
		},
		{
			name:     "english contradiction",
			fact:     "2.3.1",
			response: "The stable release is not 2.3.1.",
			want:     FailureKnowledgeRecall,
		},
		{
			name:     "chinese contradiction",
			fact:     "退款",
			response: "该商品不支持退款。",
			want:     FailureKnowledgeRecall,
		},
		{
			name:     "chinese postpositive contradiction",
			fact:     "退款",
			response: "该商品的退款不被支持。",
			want:     FailureKnowledgeRecall,
		},
		{
			name:     "chinese modal contradiction",
			fact:     "退款",
			response: "该商品不能退款。",
			want:     FailureKnowledgeRecall,
		},
		{
			name:     "affirmed numeric fact",
			fact:     "2.3.1",
			response: "The stable release is 2.3.1.",
			want:     FailureInsufficient,
		},
		{
			name:     "affirmed chinese fact",
			fact:     "稳定版",
			response: "当前稳定版为 2.3.1。",
			want:     FailureInsufficient,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metric := failedMetric("quality", "")
			snapshot, item := boundAttributionInput(CaseResult{
				ExpectedFacts: []string{test.fact},
				FinalResponse: test.response,
			}, metric)
			got := AttributeFailure(AttributionInput{
				Snapshot: snapshot,
				Case:     item,
				Metric:   metric,
			})
			require.Equal(t, test.want, got.PrimaryCategory)
		})
	}
}

func TestAttributeFailureClauseAwareNegationAndConflict(t *testing.T) {
	for _, reason := range []string{
		"Wrong tool was ruled out；the final answer is wrong。",
		"There is no evidence of a wrong tool；the final answer is wrong。",
		"There is no evidence indicating a wrong tool；the final answer is wrong。",
		"没有证据表明工具错误；最终回复不匹配。",
		"可以排除工具错误；最终回复不匹配。",
	} {
		t.Run(reason, func(t *testing.T) {
			metric := failedMetric("quality", reason)
			snapshot, item := boundAttributionInput(CaseResult{
				ExpectedResponse: "expected",
				FinalResponse:    "actual",
			}, metric)
			got := AttributeFailure(AttributionInput{
				Snapshot: snapshot,
				Case:     item,
				Metric:   metric,
			})
			require.Equal(t, FailureResponseMismatch, got.PrimaryCategory)
			require.NotContains(t, got.SecondaryCategories, FailureWrongTool)
		})
	}

	for _, reason := range []string{
		"Wrong tool was ruled out；however，the final evaluator confirms a wrong tool。",
		"可以排除工具错误；但复核确认工具错误。",
	} {
		t.Run(reason, func(t *testing.T) {
			metric := failedMetric("quality", reason)
			snapshot, item := boundAttributionInput(CaseResult{}, metric)
			got := AttributeFailure(AttributionInput{
				Snapshot: snapshot,
				Case:     item,
				Metric:   metric,
			})
			require.Equal(t, FailureAmbiguousEvidence, got.PrimaryCategory)
			require.Equal(t, EvidenceAmbiguous, got.EvidenceSufficiency)
		})
	}
}

func TestEqualJSONLikeUsesExactJSONNumbersAndFloatTolerance(t *testing.T) {
	equal, err := equalJSONLike(
		json.RawMessage(`{"id":9007199254740992}`),
		json.RawMessage(`{"id":9007199254740993}`),
		toolArgumentEpsilon,
	)
	require.NoError(t, err)
	require.False(t, equal)

	equal, err = equalJSONLike(
		json.RawMessage(`{"value":1.0000001}`),
		json.RawMessage(`{"value":1.0000002}`),
		toolArgumentEpsilon,
	)
	require.NoError(t, err)
	require.False(t, equal)

	equal, err = equalJSONLike(
		map[string]any{"value": 1.0},
		map[string]any{"value": 1.0 + toolArgumentEpsilon/2},
		toolArgumentEpsilon,
	)
	require.NoError(t, err)
	require.True(t, equal)

	equal, err = equalJSONLike(
		map[string]float64{"value": 1.0},
		map[string]float64{"value": 1.0 + toolArgumentEpsilon/2},
		toolArgumentEpsilon,
	)
	require.NoError(t, err)
	require.True(t, equal)

	equal, err = equalJSONLike(
		[]float64{1.0},
		[]float64{1.0 + toolArgumentEpsilon/2},
		toolArgumentEpsilon,
	)
	require.NoError(t, err)
	require.True(t, equal)

	type typedArguments struct {
		Value float64 `json:"value"`
	}
	equal, err = equalJSONLike(
		typedArguments{Value: 1.0},
		typedArguments{Value: 1.0 + toolArgumentEpsilon/2},
		toolArgumentEpsilon,
	)
	require.NoError(t, err)
	require.True(t, equal)

	equal, err = equalJSONLike(
		json.RawMessage(`{"id":9007199254740993}`),
		map[string]any{"id": float64(9007199254740992)},
		toolArgumentEpsilon,
	)
	require.NoError(t, err)
	require.False(t, equal)
}

func TestAttributeFailureAmbiguousAndProvenanceMismatch(t *testing.T) {
	t.Run("conflicting evaluator evidence abstains", func(t *testing.T) {
		metric := failedMetric(
			"quality",
			"wrong tool and argument mismatch are both asserted",
		)
		snapshot, item := boundAttributionInput(CaseResult{}, metric)
		got := AttributeFailure(AttributionInput{
			Snapshot: snapshot,
			Case:     item,
			Metric:   metric,
		})
		require.Equal(t, FailureAmbiguousEvidence, got.PrimaryCategory)
		require.Equal(t, EvidenceAmbiguous, got.EvidenceSufficiency)
	})
	t.Run("case from another eval set is rejected", func(t *testing.T) {
		metric := failedMetric("quality", "wrong tool")
		snapshot, item := boundAttributionInput(CaseResult{}, metric)
		item.EvalSetID = "train"
		got := AttributeFailure(AttributionInput{
			Snapshot: snapshot,
			Case:     item,
			Metric:   metric,
		})
		require.Equal(t, FailureInsufficient, got.PrimaryCategory)
		require.Contains(t, got.Reason, "binding failed")
	})
	t.Run("duplicate snapshot evidence is ambiguous", func(t *testing.T) {
		metric := failedMetric("quality", "wrong tool")
		snapshot, item := boundAttributionInput(CaseResult{}, metric)
		snapshot.Cases = append(snapshot.Cases, item)
		got := AttributeFailure(AttributionInput{
			Snapshot: snapshot,
			Case:     item,
			Metric:   metric,
		})
		require.Equal(t, FailureAmbiguousEvidence, got.PrimaryCategory)
	})
}

func TestAttributeFailureEvidenceIsBoundedAndRedacted(t *testing.T) {
	secret := "sk-1234567890SECRET"
	metric := failedMetric(
		"quality",
		`wrong tool; "authorization":"Bearer `+secret+`"`,
	)
	snapshot, item := boundAttributionInput(CaseResult{
		ExpectedResponse: strings.Repeat("expected ", 80),
		FinalResponse: strings.Repeat("actual ", 80) +
			` {"api_key":"` + secret + `"}`,
	}, metric)
	got := AttributeFailure(AttributionInput{
		Snapshot: snapshot,
		Case:     item,
		Metric:   metric,
	})
	for _, evidence := range got.Evidence {
		require.NotContains(t, evidence.Summary, secret)
		require.LessOrEqual(
			t,
			len([]rune(evidence.Summary)),
			maxEvidenceRunes+1,
		)
	}
}

func TestCalculateDeltaKindsCompositeIdentityAndEpsilon(t *testing.T) {
	before := testSnapshot(
		"before",
		[]string{"new-pass", "new-fail", "improve", "regress", "same"},
		[]string{"quality", "safety"},
	)
	after := testSnapshot(
		"after",
		append([]string(nil), before.Inventory.CaseIDs...),
		append([]string(nil), before.Inventory.MetricNames...),
	)
	setSnapshotCases(before, 0.62,
		testCase("new-pass", false, 0.2, 0.2),
		testCase("new-fail", true, 0.9, 0.9),
		testCase("improve", true, 0.5, 0.5),
		testCase("regress", true, 0.8, 0.8),
		testCase("same", true, 0.7, 0.7),
	)
	// Deliberately permute result order; matching must use evalSet+case identity.
	setSnapshotCases(after, 0.60,
		testCase("regress", true, 0.7, 0.7),
		testCase("same", true, 0.7+5e-7, 0.7),
		testCase("new-pass", true, 0.8, 0.8),
		testCase("improve", true, 0.6, 0.6),
		testCase("new-fail", false, 0.2, 0.2),
	)
	before.Cases[4].HardFailure = true
	after.Cases[1].HardFailure = true
	policy := GatePolicy{
		PrimaryMetric: "quality",
		MetricDirections: map[string]ScoreDirection{
			"quality": ScoreHigherIsBetter,
			"safety":  ScoreHigherIsBetter,
		},
		Epsilon: 1e-6,
	}
	got, err := CalculateDelta("vs_released", before, after, policy)
	require.NoError(t, err)
	require.Equal(t, 1, got.NewlyPassing)
	require.Equal(t, 1, got.NewlyFailing)
	require.Equal(t, 1, got.Improved)
	require.Equal(t, 1, got.Regressed)
	require.Equal(t, 1, got.Unchanged)
	require.Equal(t, []ChangeKind{
		ChangeNewlyPassing,
		ChangeNewlyFailing,
		ChangeImproved,
		ChangeRegressed,
		ChangeUnchanged,
	}, []ChangeKind{
		got.Cases[0].PrimaryKind,
		got.Cases[1].PrimaryKind,
		got.Cases[2].PrimaryKind,
		got.Cases[3].PrimaryKind,
		got.Cases[4].PrimaryKind,
	})
	require.Equal(t, "validation", got.Cases[0].EvalSetID)
	require.Len(t, got.Cases[0].Metrics, 2)
	require.True(
		t,
		got.Cases[4].HardFailure,
		"case classification must survive even without a newly-failing transition",
	)
}

func TestCalculateDeltaLowerIsBetterOrientsOnlySummaryGain(t *testing.T) {
	before := testSnapshot("before", []string{"case"}, []string{"latency"})
	after := testSnapshot("after", before.Inventory.CaseIDs, before.Inventory.MetricNames)
	beforeCase := metricCase(
		"case",
		true,
		MetricResult{
			MetricName: "latency",
			Score:      0.8,
			Status:     "passed",
			Passed:     true,
			Threshold:  1,
			Direction:  ScoreLowerIsBetter,
		},
	)
	afterCase := metricCase(
		"case",
		true,
		MetricResult{
			MetricName: "latency",
			Score:      0.6,
			Status:     "passed",
			Passed:     true,
			Threshold:  1,
			Direction:  ScoreLowerIsBetter,
		},
	)
	setSnapshotCases(before, 0.8, beforeCase)
	setSnapshotCases(after, 0.6, afterCase)
	policy := GatePolicy{
		PrimaryMetric: "latency",
		MetricDirections: map[string]ScoreDirection{
			"latency": ScoreLowerIsBetter,
		},
		Epsilon: 1e-9,
	}
	got, err := CalculateDelta("vs_initial", before, after, policy)
	require.NoError(t, err)
	require.InDelta(t, 0.2, got.ScoreDelta, 1e-12)
	require.InDelta(t, -0.2, got.Cases[0].Metrics[0].Delta, 1e-12)
	require.Equal(t, ChangeImproved, got.Cases[0].PrimaryKind)
}

func TestCalculateDeltaFailsClosed(t *testing.T) {
	policy := GatePolicy{
		PrimaryMetric: "quality",
		MetricDirections: map[string]ScoreDirection{
			"quality": ScoreHigherIsBetter,
			"safety":  ScoreHigherIsBetter,
		},
		Epsilon: DefaultEpsilon,
	}
	validPair := func(metricNames ...string) (*EvaluationSnapshot, *EvaluationSnapshot) {
		before := testSnapshot("before", []string{"case"}, metricNames)
		after := testSnapshot("after", before.Inventory.CaseIDs, before.Inventory.MetricNames)
		scores := make([]float64, len(metricNames))
		for i := range scores {
			scores[i] = 1
		}
		setSnapshotCases(before, 1, testCase("case", true, scores...))
		setSnapshotCases(after, 1, testCase("case", true, scores...))
		return before, after
	}
	t.Run("both omit expected case", func(t *testing.T) {
		before := testSnapshot(
			"before",
			[]string{"case-a", "case-b"},
			[]string{"quality"},
		)
		after := testSnapshot(
			"after",
			before.Inventory.CaseIDs,
			before.Inventory.MetricNames,
		)
		setSnapshotCases(before, 1, testCase("case-a", true, 1))
		setSnapshotCases(after, 1, testCase("case-a", true, 1))
		_, err := CalculateDelta("vs_released", before, after, policy)
		require.ErrorContains(t, err, "case-b")
	})
	t.Run("candidate omits expected case", func(t *testing.T) {
		before := testSnapshot(
			"before",
			[]string{"case-a", "case-b"},
			[]string{"quality"},
		)
		after := testSnapshot(
			"after",
			before.Inventory.CaseIDs,
			before.Inventory.MetricNames,
		)
		setSnapshotCases(
			before,
			1,
			testCase("case-a", true, 1),
			testCase("case-b", true, 1),
		)
		setSnapshotCases(after, 1, testCase("case-a", true, 1))
		_, err := CalculateDelta("vs_released", before, after, policy)
		require.ErrorContains(t, err, "case-b")
	})
	t.Run("before snapshot is not evaluable", func(t *testing.T) {
		before, after := validPair("quality")
		before.Status = EvaluationNotEvaluable
		_, err := CalculateDelta("vs_released", before, after, policy)
		require.ErrorContains(t, err, string(EvaluationNotEvaluable))
	})
	t.Run("candidate snapshot run failed", func(t *testing.T) {
		before, after := validPair("quality")
		after.Status = EvaluationRunFailed
		_, err := CalculateDelta("vs_released", before, after, policy)
		require.ErrorContains(t, err, string(EvaluationRunFailed))
	})
	t.Run("explicit no-tool expectation drift", func(t *testing.T) {
		before, after := validPair("quality")
		after.Cases[0].ExpectNoTools = !before.Cases[0].ExpectNoTools
		_, err := CalculateDelta("vs_released", before, after, policy)
		require.ErrorContains(t, err, "explicit no-tool expectation changed")
	})
	t.Run("metric inventory incomplete", func(t *testing.T) {
		before, after := validPair("quality", "safety")
		after.Cases[0].Metrics = after.Cases[0].Metrics[:1]
		_, err := CalculateDelta("vs_released", before, after, policy)
		require.ErrorContains(t, err, "metric inventory mismatch")
	})
	t.Run("non finite score", func(t *testing.T) {
		before, after := validPair("quality")
		after.Cases[0].Metrics[0].Score = math.NaN()
		_, err := CalculateDelta("vs_released", before, after, policy)
		require.ErrorContains(t, err, "finite")
	})
	t.Run("provenance mismatch", func(t *testing.T) {
		before, after := validPair("quality")
		after.Provenance.MetricsHash = "different"
		_, err := CalculateDelta("vs_released", before, after, policy)
		require.ErrorContains(t, err, "provenance mismatch")
	})
	t.Run("not evaluated status", func(t *testing.T) {
		before, after := validPair("quality")
		after.Cases[0].Metrics[0].Status = "not_evaluated"
		_, err := CalculateDelta("vs_released", before, after, policy)
		require.ErrorContains(t, err, "non-comparable")
	})
	t.Run("case provenance mismatch", func(t *testing.T) {
		before, after := validPair("quality")
		after.Cases[0].EvalSetID = "train"
		_, err := CalculateDelta("vs_released", before, after, policy)
		require.ErrorContains(t, err, "eval set")
	})
	t.Run("seed mismatch", func(t *testing.T) {
		before, after := validPair("quality")
		after.Provenance.Seed++
		_, err := CalculateDelta("vs_released", before, after, policy)
		require.ErrorContains(t, err, "seed")
	})
}

func TestDecideReleaseSafetyGates(t *testing.T) {
	policy := GatePolicy{
		PrimaryMetric:          "quality",
		MetricDirections:       map[string]ScoreDirection{"quality": ScoreHigherIsBetter},
		MinValidationGain:      0.05,
		NoNewHardFailures:      true,
		NoCriticalRegressions:  true,
		ModelCallStopThreshold: 10,
		Epsilon:                1e-9,
	}
	calls := ResourceUsage{
		ModelCalls: Count{Available: true, Value: 10},
	}
	t.Run("accept exact gain and model-call threshold boundaries", func(t *testing.T) {
		decision := DecideRelease(policy, decisionDelta(0.05), calls)
		require.Equal(t, DecisionAccepted, decision.Status)
	})
	t.Run("accept floating boundary within epsilon", func(t *testing.T) {
		delta := decisionDelta(0.05 - 5e-10)
		decision := DecideRelease(policy, delta, calls)
		require.Equal(t, DecisionAccepted, decision.Status)
	})
	t.Run("validation regression is always rejected", func(t *testing.T) {
		relaxed := policy
		relaxed.MinValidationGain = 0
		decision := DecideRelease(relaxed, decisionDelta(-0.01), calls)
		require.Equal(t, DecisionRejected, decision.Status)
		require.Contains(t, decision.Reasons[0], "validation_regression")
	})
	t.Run("new hard failure", func(t *testing.T) {
		delta := decisionDelta(0.2)
		delta.Improved = 0
		delta.NewlyFailing = 1
		delta.Cases[0].BeforeStatus = "passed"
		delta.Cases[0].AfterStatus = "failed"
		delta.Cases[0].BeforePassed = true
		delta.Cases[0].AfterPassed = false
		delta.Cases[0].PrimaryKind = ChangeNewlyFailing
		delta.Cases[0].HardFailure = true
		delta.Cases[0].Metrics[0].AfterStatus = "failed"
		decision := DecideRelease(policy, delta, calls)
		require.Equal(t, DecisionRejected, decision.Status)
		require.Contains(t, decision.Reasons[0], "hard")
	})
	t.Run("critical secondary metric regression while still passing", func(t *testing.T) {
		delta := decisionDelta(0.2)
		delta.Cases[0].Critical = true
		delta.Cases[0].Metrics = append(
			delta.Cases[0].Metrics,
			MetricDelta{
				MetricName:   "safety",
				BeforeScore:  0.9,
				AfterScore:   0.8,
				Delta:        -0.1,
				Direction:    ScoreHigherIsBetter,
				BeforeStatus: "passed",
				AfterStatus:  "passed",
			},
		)
		criticalPolicy := policy
		criticalPolicy.MetricDirections = map[string]ScoreDirection{
			"quality": ScoreHigherIsBetter,
			"safety":  ScoreHigherIsBetter,
		}
		decision := DecideRelease(criticalPolicy, delta, calls)
		require.Equal(t, DecisionRejected, decision.Status)
		require.Contains(t, decision.Reasons[0], "critical")
	})
	t.Run("unknown configured measurement", func(t *testing.T) {
		decision := DecideRelease(
			policy,
			decisionDelta(0.2),
			ResourceUsage{},
		)
		require.Equal(t, DecisionNotEvaluable, decision.Status)
	})
	t.Run("over threshold", func(t *testing.T) {
		over := ResourceUsage{
			ModelCalls: Count{Available: true, Value: 11},
		}
		decision := DecideRelease(policy, decisionDelta(0.2), over)
		require.Equal(t, DecisionRejected, decision.Status)
		require.Contains(t, decision.Reasons[0], "threshold")
	})
	t.Run("non finite delta is not evaluable", func(t *testing.T) {
		delta := decisionDelta(0.2)
		delta.ScoreDelta = math.Inf(1)
		decision := DecideRelease(policy, delta, calls)
		require.Equal(t, DecisionNotEvaluable, decision.Status)
		require.Nil(t, decision.ScoreDelta)
	})
	t.Run("hard classification without a new failure remains evaluable", func(t *testing.T) {
		delta := decisionDelta(0.2)
		delta.Cases[0].HardFailure = true
		decision := DecideRelease(policy, delta, calls)
		require.Equal(t, DecisionAccepted, decision.Status)
	})
}

func TestDecideReleaseRejectsForgedCaseDeltas(t *testing.T) {
	calls := ResourceUsage{
		ModelCalls: Count{Available: true, Value: 1},
	}
	higherPolicy := GatePolicy{
		PrimaryMetric:          "quality",
		MetricDirections:       map[string]ScoreDirection{"quality": ScoreHigherIsBetter},
		MinValidationGain:      0,
		NoNewHardFailures:      true,
		NoCriticalRegressions:  true,
		ModelCallStopThreshold: 10,
		Epsilon:                1e-9,
	}

	t.Run("critical forged raw delta and improved kind", func(t *testing.T) {
		delta := decisionDelta(0.2)
		delta.Unchanged = 0
		delta.Improved = 1
		delta.Cases[0].Critical = true
		delta.Cases[0].PrimaryKind = ChangeImproved
		delta.Cases[0].Metrics[0].BeforeScore = 0.9
		delta.Cases[0].Metrics[0].AfterScore = 0.1
		delta.Cases[0].Metrics[0].Delta = 0.8

		decision := DecideRelease(higherPolicy, delta, calls)
		require.Equal(t, DecisionNotEvaluable, decision.Status)
		require.Contains(t, strings.Join(decision.Reasons, " "), "after-before")
	})

	t.Run("forged primary kind", func(t *testing.T) {
		delta := decisionDelta(0.2)
		delta.Improved = 0
		delta.Regressed = 1
		delta.Cases[0].PrimaryKind = ChangeRegressed
		delta.Cases[0].Metrics[0].AfterScore = 0.7
		delta.Cases[0].Metrics[0].Delta = 0.2

		decision := DecideRelease(higherPolicy, delta, calls)
		require.Equal(t, DecisionNotEvaluable, decision.Status)
		require.Contains(t, strings.Join(decision.Reasons, " "), "recomputed")
	})

	t.Run("lower is better direction mismatch", func(t *testing.T) {
		policy := higherPolicy
		policy.PrimaryMetric = "latency"
		policy.MetricDirections = map[string]ScoreDirection{
			"latency": ScoreLowerIsBetter,
		}
		delta := decisionDelta(0)
		delta.BeforeOverallScore = 0.8
		delta.AfterOverallScore = 0.6
		delta.ScoreDelta = 0.2
		delta.Unchanged = 0
		delta.Regressed = 1
		delta.Cases[0].PrimaryKind = ChangeRegressed
		delta.Cases[0].Metrics[0] = MetricDelta{
			MetricName:   "latency",
			BeforeScore:  0.8,
			AfterScore:   0.6,
			Delta:        -0.2,
			Direction:    ScoreHigherIsBetter,
			BeforeStatus: "passed",
			AfterStatus:  "passed",
		}

		decision := DecideRelease(policy, delta, calls)
		require.Equal(t, DecisionNotEvaluable, decision.Status)
		require.Contains(t, strings.Join(decision.Reasons, " "), "does not match policy")
	})

	t.Run("missing policy metric", func(t *testing.T) {
		policy := higherPolicy
		policy.PrimaryMetric = "latency"
		policy.MetricDirections = map[string]ScoreDirection{
			"latency": ScoreLowerIsBetter,
			"safety":  ScoreHigherIsBetter,
		}
		delta := decisionDelta(0)
		delta.BeforeOverallScore = 0.8
		delta.AfterOverallScore = 0.6
		delta.ScoreDelta = 0.2
		delta.Unchanged = 0
		delta.Improved = 1
		delta.Cases[0].PrimaryKind = ChangeImproved
		delta.Cases[0].Metrics[0] = MetricDelta{
			MetricName:   "latency",
			BeforeScore:  0.8,
			AfterScore:   0.6,
			Delta:        -0.2,
			Direction:    ScoreLowerIsBetter,
			BeforeStatus: "passed",
			AfterStatus:  "passed",
		}

		decision := DecideRelease(policy, delta, calls)
		require.Equal(t, DecisionNotEvaluable, decision.Status)
		require.Contains(t, strings.Join(decision.Reasons, " "), "inventory")
	})

	t.Run("extra metric outside policy", func(t *testing.T) {
		delta := decisionDelta(0.2)
		delta.Cases[0].Metrics = append(
			delta.Cases[0].Metrics,
			MetricDelta{
				MetricName:   "unexpected",
				BeforeScore:  0.5,
				AfterScore:   0.5,
				Delta:        0,
				Direction:    ScoreHigherIsBetter,
				BeforeStatus: "passed",
				AfterStatus:  "passed",
			},
		)

		decision := DecideRelease(higherPolicy, delta, calls)
		require.Equal(t, DecisionNotEvaluable, decision.Status)
		require.Contains(t, strings.Join(decision.Reasons, " "), "inventory")
	})

	t.Run("forged aggregate gain", func(t *testing.T) {
		delta := decisionDelta(0)
		delta.AfterOverallScore = 0.7
		delta.ScoreDelta = 0.2

		decision := DecideRelease(higherPolicy, delta, calls)
		require.Equal(t, DecisionNotEvaluable, decision.Status)
		require.Contains(t, strings.Join(decision.Reasons, " "), "primary metric mean")
	})
}

// testSnapshot is shared with other regression package tests.
func testSnapshot(
	profile string,
	caseIDs []string,
	metricNames []string,
) *EvaluationSnapshot {
	return &EvaluationSnapshot{
		Status: EvaluationCompleted,
		Provenance: EvaluationProvenance{
			RunID:               "run-" + profile,
			ProfileHash:         profile,
			EvalSetID:           "validation",
			EvalSetHash:         "evalset-hash",
			MetricsHash:         "metrics-hash",
			Split:               "heldout_validation",
			Seed:                2003,
			EvaluatorConfigHash: "evaluator-hash",
			MetricPolicyHash:    "metric-policy-hash",
		},
		Inventory: ExpectedInventory{
			CaseIDs:     append([]string(nil), caseIDs...),
			MetricNames: append([]string(nil), metricNames...),
		},
	}
}

// testCase is shared with other regression package tests.
func testCase(id string, passed bool, scores ...float64) CaseResult {
	names := []string{"quality", "safety"}
	metrics := make([]MetricResult, 0, len(scores))
	for i, score := range scores {
		metrics = append(metrics, MetricResult{
			MetricName: names[i],
			Score:      score,
			Status:     statusForPassed(passed),
			Passed:     passed,
			Threshold:  0.5,
			Direction:  ScoreHigherIsBetter,
		})
	}
	return CaseResult{
		EvalSetID:     "validation",
		CaseID:        id,
		Status:        statusForPassed(passed),
		Passed:        passed,
		PrimaryMetric: "quality",
		Metrics:       metrics,
	}
}

func metricCase(
	id string,
	passed bool,
	metrics ...MetricResult,
) CaseResult {
	primaryMetric := ""
	if len(metrics) > 0 {
		primaryMetric = metrics[0].MetricName
	}
	return CaseResult{
		EvalSetID:     "validation",
		CaseID:        id,
		Status:        statusForPassed(passed),
		Passed:        passed,
		PrimaryMetric: primaryMetric,
		Metrics:       append([]MetricResult(nil), metrics...),
	}
}

func setSnapshotCases(
	snapshot *EvaluationSnapshot,
	overallScore float64,
	cases ...CaseResult,
) {
	snapshot.Cases = append([]CaseResult(nil), cases...)
	snapshot.OverallScore = overallScore
	snapshot.Passed = 0
	snapshot.Failed = 0
	for _, item := range snapshot.Cases {
		if item.Passed {
			snapshot.Passed++
		} else {
			snapshot.Failed++
		}
	}
}

func failedMetric(name, reason string) MetricResult {
	return MetricResult{
		MetricName: name,
		Score:      0,
		Status:     "failed",
		Passed:     false,
		Threshold:  0.5,
		Direction:  ScoreHigherIsBetter,
		Reason:     reason,
	}
}

func boundAttributionInput(
	item CaseResult,
	metric MetricResult,
) (*EvaluationSnapshot, CaseResult) {
	item.EvalSetID = "validation"
	item.CaseID = "case"
	item.Status = "failed"
	item.Passed = false
	item.PrimaryMetric = metric.MetricName
	item.Metrics = []MetricResult{metric}
	snapshot := testSnapshot(
		"profile-a",
		[]string{item.CaseID},
		[]string{metric.MetricName},
	)
	setSnapshotCases(snapshot, metric.Score, item)
	return snapshot, item
}

func decisionDelta(gain float64) DeltaSummary {
	kind := ChangeUnchanged
	if gain > DefaultEpsilon {
		kind = ChangeImproved
	} else if gain < -DefaultEpsilon {
		kind = ChangeRegressed
	}
	delta := DeltaSummary{
		Comparison:         "vs_released",
		BeforeProfileHash:  "before",
		AfterProfileHash:   "after",
		BeforeOverallScore: 0.5,
		AfterOverallScore:  0.5 + gain,
		ScoreDelta:         gain,
		Cases: []CaseDelta{
			{
				EvalSetID:    "validation",
				CaseID:       "case",
				BeforeStatus: "passed",
				AfterStatus:  "passed",
				BeforePassed: true,
				AfterPassed:  true,
				PrimaryKind:  kind,
				Metrics: []MetricDelta{
					{
						MetricName:   "quality",
						BeforeScore:  0.5,
						AfterScore:   0.5 + gain,
						Delta:        gain,
						Direction:    ScoreHigherIsBetter,
						BeforeStatus: "passed",
						AfterStatus:  "passed",
					},
				},
			},
		},
	}
	switch kind {
	case ChangeImproved:
		delta.Improved = 1
	case ChangeRegressed:
		delta.Regressed = 1
	default:
		delta.Unchanged = 1
	}
	return delta
}

func statusForPassed(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}
