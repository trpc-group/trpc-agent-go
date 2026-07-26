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
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

type attributionCorpus struct {
	Cases []struct {
		ID           string           `json:"id"`
		Scored       bool             `json:"scored"`
		Want         FailureCategory  `json:"want"`
		Case         CaseResult       `json:"case"`
		Reason       string           `json:"reason"`
		SourceStatus EvaluationStatus `json:"sourceStatus"`
		Recover      bool             `json:"recover"`
		ExactSafety  bool             `json:"exactSafety"`
	} `json:"cases"`
}

type toolMatrixCorpus struct {
	Cases []struct {
		ID       string     `json:"id"`
		Expected []ToolCall `json:"expected"`
		Actual   []ToolCall `json:"actual"`
		Mismatch bool       `json:"mismatch"`
	} `json:"cases"`
}

type gateCorpusCase struct {
	ID                     string                    `json:"id"`
	Direction              ScoreDirection            `json:"direction"`
	MetricDirection        ScoreDirection            `json:"metricDirection"`
	PolicyMetricDirections map[string]ScoreDirection `json:"policyMetricDirections"`
	Comparison             string                    `json:"comparison"`
	Before                 float64                   `json:"before"`
	After                  float64                   `json:"after"`
	ReportedGain           float64                   `json:"reportedGain"`
	MinimumGain            float64                   `json:"minimumGain"`
	ModelCallStopThreshold int64                     `json:"modelCallStopThreshold"`
	CallsAvailable         bool                      `json:"callsAvailable"`
	Calls                  int64                     `json:"calls"`
	HardFailure            bool                      `json:"hardFailure"`
	Critical               bool                      `json:"critical"`
	Kind                   ChangeKind                `json:"kind"`
	MetricBefore           *float64                  `json:"metricBefore"`
	MetricAfter            *float64                  `json:"metricAfter"`
	MetricDelta            *float64                  `json:"metricDelta"`
	BalancerBefore         *float64                  `json:"balancerBefore"`
	BalancerAfter          *float64                  `json:"balancerAfter"`
	ExtraMetric            bool                      `json:"extraMetric"`
	Want                   DecisionStatus            `json:"want"`
	ExactSafety            bool                      `json:"exactSafety"`
}

type gateCorpus struct {
	Cases []gateCorpusCase `json:"cases"`
}

type invalidDeltaCorpus struct {
	Cases []struct {
		ID               string           `json:"id"`
		InventoryCaseIDs []string         `json:"inventoryCaseIds"`
		BeforeCaseIDs    []string         `json:"beforeCaseIds"`
		AfterCaseIDs     []string         `json:"afterCaseIds"`
		BeforeStatus     EvaluationStatus `json:"beforeStatus"`
		AfterStatus      EvaluationStatus `json:"afterStatus"`
		WantError        string           `json:"wantError"`
	} `json:"cases"`
}

func TestAttributionCorpusMeetsAccuracyAndAbstentionRequirements(t *testing.T) {
	var corpus attributionCorpus
	readCorpus(t, "attribution_corpus.json", &corpus)
	require.NotEmpty(t, corpus.Cases)
	correct := 0
	confusion := make(map[string]int)
	for _, fixture := range corpus.Cases {
		metric := failedMetric("quality", fixture.Reason)
		snapshot, item := boundAttributionInput(fixture.Case, metric)
		if fixture.SourceStatus != "" {
			snapshot.Status = fixture.SourceStatus
		}
		if fixture.Recover {
			initial := AttributeFailure(AttributionInput{
				Snapshot: snapshot,
				Case:     item,
				Metric:   metric,
			})
			require.Equal(
				t,
				FailureInsufficient,
				initial.PrimaryCategory,
				"pre-recovery corpus case %s",
				fixture.ID,
			)
			snapshot.Status = EvaluationCompleted
		}
		got := AttributeFailure(AttributionInput{
			Snapshot: snapshot,
			Case:     item,
			Metric:   metric,
		})
		confusion[string(fixture.Want)+" -> "+string(got.PrimaryCategory)]++
		if got.PrimaryCategory == fixture.Want {
			correct++
		} else {
			t.Logf(
				"attribution corpus case %q: got %q, want %q",
				fixture.ID,
				got.PrimaryCategory,
				fixture.Want,
			)
		}
		// Abstention and explicitly locked adversarial cases remain in the
		// aggregate denominator and are also checked exactly.
		if !fixture.Scored || fixture.ExactSafety {
			require.Equal(
				t,
				fixture.Want,
				got.PrimaryCategory,
				"corpus case %s",
				fixture.ID,
			)
			require.NotEmpty(t, got.Reason, "corpus case %s", fixture.ID)
		}
	}
	accuracy := float64(correct) / float64(len(corpus.Cases))
	t.Logf(
		"attribution corpus accuracy: %d/%d = %.3f",
		correct,
		len(corpus.Cases),
		accuracy,
	)
	logConfusionMatrix(t, "attribution", confusion)
	require.GreaterOrEqual(t, accuracy, 0.75)
}

func TestInvalidDeltaCorpusFailsClosed(t *testing.T) {
	var corpus invalidDeltaCorpus
	readCorpus(t, "delta_invalid_corpus.json", &corpus)
	require.NotEmpty(t, corpus.Cases)
	policy := GatePolicy{
		PrimaryMetric: "quality",
		MetricDirections: map[string]ScoreDirection{
			"quality": ScoreHigherIsBetter,
		},
		Epsilon: 1e-9,
	}
	for _, fixture := range corpus.Cases {
		before := testSnapshot(
			"before-"+fixture.ID,
			fixture.InventoryCaseIDs,
			[]string{"quality"},
		)
		after := testSnapshot(
			"after-"+fixture.ID,
			fixture.InventoryCaseIDs,
			[]string{"quality"},
		)
		beforeCases := make([]CaseResult, 0, len(fixture.BeforeCaseIDs))
		for _, caseID := range fixture.BeforeCaseIDs {
			beforeCases = append(beforeCases, testCase(caseID, true, 1))
		}
		afterCases := make([]CaseResult, 0, len(fixture.AfterCaseIDs))
		for _, caseID := range fixture.AfterCaseIDs {
			afterCases = append(afterCases, testCase(caseID, true, 1))
		}
		setSnapshotCases(before, 1, beforeCases...)
		setSnapshotCases(after, 1, afterCases...)
		before.Status = fixture.BeforeStatus
		after.Status = fixture.AfterStatus
		_, err := CalculateDelta("vs_released", before, after, policy)
		require.ErrorContains(
			t,
			err,
			fixture.WantError,
			"corpus case %s",
			fixture.ID,
		)
	}
}

func TestSearchReleaseToolMatrixIsExhaustive(t *testing.T) {
	var corpus toolMatrixCorpus
	readCorpus(t, "tool_matrix.json", &corpus)
	require.Len(t, corpus.Cases, 16)
	for _, fixture := range corpus.Cases {
		finding, ambiguous := compareTools(fixture.Expected, fixture.Actual)
		require.Nil(t, ambiguous, "corpus case %s", fixture.ID)
		if fixture.Mismatch {
			require.NotNil(t, finding, "corpus case %s", fixture.ID)
			require.Equal(
				t,
				FailureWrongTool,
				finding.category,
				"corpus case %s",
				fixture.ID,
			)
			continue
		}
		require.Nil(t, finding, "corpus case %s", fixture.ID)
	}
}

func TestReleaseDecisionCorpusMeetsAccuracyAndExactSafetyRequirements(t *testing.T) {
	var corpus gateCorpus
	readCorpus(t, "gate_corpus.json", &corpus)
	require.NotEmpty(t, corpus.Cases)
	correct := 0
	truePositive := 0
	falsePositive := 0
	trueNegative := 0
	falseNegative := 0
	for _, fixture := range corpus.Cases {
		direction := fixture.Direction
		if direction == "" {
			direction = ScoreHigherIsBetter
		}
		comparison := fixture.Comparison
		if comparison == "" {
			comparison = "vs_released"
		}
		policyDirections := make(
			map[string]ScoreDirection,
			len(fixture.PolicyMetricDirections),
		)
		for metricName, configuredDirection := range fixture.PolicyMetricDirections {
			policyDirections[metricName] = configuredDirection
		}
		if len(policyDirections) == 0 {
			policyDirections["quality"] = direction
		}
		policy := GatePolicy{
			PrimaryMetric:          "quality",
			MetricDirections:       policyDirections,
			Epsilon:                1e-9,
			MinValidationGain:      fixture.MinimumGain,
			NoNewHardFailures:      true,
			NoCriticalRegressions:  true,
			ModelCallStopThreshold: fixture.ModelCallStopThreshold,
		}
		delta := gateCorpusDelta(fixture, direction, comparison)
		usage := ResourceUsage{
			ModelCalls: Count{
				Available: fixture.CallsAvailable,
				Value:     fixture.Calls,
			},
		}
		got := DecideRelease(policy, delta, usage)
		wantAccepted := fixture.Want == DecisionAccepted
		gotAccepted := got.Status == DecisionAccepted
		switch {
		case wantAccepted && gotAccepted:
			truePositive++
		case !wantAccepted && gotAccepted:
			falsePositive++
		case !wantAccepted && !gotAccepted:
			trueNegative++
		case wantAccepted && !gotAccepted:
			falseNegative++
		}
		if got.Status == fixture.Want {
			correct++
		} else {
			t.Logf(
				"gate corpus case %q: got %q (%v), want %q",
				fixture.ID,
				got.Status,
				got.Reasons,
				fixture.Want,
			)
		}
		if fixture.ExactSafety {
			require.Equal(
				t,
				fixture.Want,
				got.Status,
				"safety corpus case %s: %v",
				fixture.ID,
				got.Reasons,
			)
		}
	}
	accuracy := float64(correct) / float64(len(corpus.Cases))
	t.Logf(
		"release-decision corpus accuracy: %d/%d = %.3f",
		correct,
		len(corpus.Cases),
		accuracy,
	)
	t.Logf(
		"release binary confusion: TP=%d FP=%d TN=%d FN=%d",
		truePositive,
		falsePositive,
		trueNegative,
		falseNegative,
	)
	require.GreaterOrEqual(t, accuracy, 0.80)
}

func gateCorpusDelta(
	fixture gateCorpusCase,
	direction ScoreDirection,
	comparison string,
) DeltaSummary {
	metricDirection := fixture.MetricDirection
	if metricDirection == "" {
		metricDirection = direction
	}
	metricBefore := fixture.Before
	if fixture.MetricBefore != nil {
		metricBefore = *fixture.MetricBefore
	}
	metricAfter := fixture.After
	if fixture.MetricAfter != nil {
		metricAfter = *fixture.MetricAfter
	}
	rawMetricDelta := metricAfter - metricBefore
	if fixture.MetricDelta != nil {
		rawMetricDelta = *fixture.MetricDelta
		if fixture.MetricAfter == nil {
			metricAfter = metricBefore + rawMetricDelta
		}
	}

	kind := fixture.Kind
	if kind == "" {
		gain := metricAfter - metricBefore
		if direction == ScoreLowerIsBetter {
			gain = -gain
		}
		switch {
		case gain > 1e-9:
			kind = ChangeImproved
		case gain < -1e-9:
			kind = ChangeRegressed
		default:
			kind = ChangeUnchanged
		}
	}
	beforeStatus := "passed"
	afterStatus := "passed"
	beforePassed := true
	afterPassed := true
	switch kind {
	case ChangeNewlyPassing:
		beforeStatus = "failed"
		beforePassed = false
	case ChangeNewlyFailing:
		afterStatus = "failed"
		afterPassed = false
	}
	delta := DeltaSummary{
		Comparison:         comparison,
		BeforeProfileHash:  "released",
		AfterProfileHash:   "candidate",
		BeforeOverallScore: fixture.Before,
		AfterOverallScore:  fixture.After,
		ScoreDelta:         fixture.ReportedGain,
		Cases: []CaseDelta{
			{
				EvalSetID:    "validation",
				CaseID:       fixture.ID,
				BeforeStatus: beforeStatus,
				AfterStatus:  afterStatus,
				BeforePassed: beforePassed,
				AfterPassed:  afterPassed,
				PrimaryKind:  kind,
				HardFailure:  fixture.HardFailure,
				Critical:     fixture.Critical,
				Metrics: []MetricDelta{
					{
						MetricName:   "quality",
						BeforeScore:  metricBefore,
						AfterScore:   metricAfter,
						Delta:        rawMetricDelta,
						Direction:    metricDirection,
						BeforeStatus: beforeStatus,
						AfterStatus:  afterStatus,
					},
				},
			},
		},
	}
	if fixture.ExtraMetric {
		delta.Cases[0].Metrics = append(
			delta.Cases[0].Metrics,
			MetricDelta{
				MetricName:   "unexpected",
				BeforeScore:  1,
				AfterScore:   1,
				Delta:        0,
				Direction:    ScoreHigherIsBetter,
				BeforeStatus: beforeStatus,
				AfterStatus:  afterStatus,
			},
		)
	}
	incrementDeltaKind(&delta, kind)
	if fixture.BalancerBefore != nil && fixture.BalancerAfter != nil {
		balancerGain := *fixture.BalancerAfter - *fixture.BalancerBefore
		if direction == ScoreLowerIsBetter {
			balancerGain = -balancerGain
		}
		balancerKind := ChangeUnchanged
		switch {
		case balancerGain > 1e-9:
			balancerKind = ChangeImproved
		case balancerGain < -1e-9:
			balancerKind = ChangeRegressed
		}
		delta.Cases = append(delta.Cases, CaseDelta{
			EvalSetID:    "validation",
			CaseID:       fixture.ID + "-balancer",
			BeforeStatus: "passed",
			AfterStatus:  "passed",
			BeforePassed: true,
			AfterPassed:  true,
			PrimaryKind:  balancerKind,
			Metrics: []MetricDelta{{
				MetricName:   "quality",
				BeforeScore:  *fixture.BalancerBefore,
				AfterScore:   *fixture.BalancerAfter,
				Delta:        *fixture.BalancerAfter - *fixture.BalancerBefore,
				Direction:    direction,
				BeforeStatus: "passed",
				AfterStatus:  "passed",
			}},
		})
		incrementDeltaKind(&delta, balancerKind)
	}
	return delta
}

func incrementDeltaKind(delta *DeltaSummary, kind ChangeKind) {
	switch kind {
	case ChangeNewlyPassing:
		delta.NewlyPassing++
	case ChangeNewlyFailing:
		delta.NewlyFailing++
	case ChangeImproved:
		delta.Improved++
	case ChangeRegressed:
		delta.Regressed++
	case ChangeUnchanged:
		delta.Unchanged++
	}
}

func readCorpus(t *testing.T, name string, target any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, target))
}

func logConfusionMatrix(
	t *testing.T,
	name string,
	confusion map[string]int,
) {
	t.Helper()
	keys := make([]string, 0, len(confusion))
	for key := range confusion {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		t.Logf("%s confusion: %s = %d", name, key, confusion[key])
	}
}
