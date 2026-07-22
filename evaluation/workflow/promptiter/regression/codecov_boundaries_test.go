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
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

func TestValidateEvalSetFailClosedBoundaries(t *testing.T) {
	newSet := func() *EvalSet {
		threshold := 0.8
		return &EvalSet{
			EvalSetID:     "set",
			PassThreshold: &threshold,
			EvalCases: []EvalCase{{
				EvalID:       "case",
				Conversation: []*evalset.Invocation{{}},
				FakeResponses: map[string]FakeOutput{
					"baseline": {},
				},
			}},
		}
	}

	require.NoError(t, validateEvalSet(newSet()))
	assert.ErrorContains(t, validateEvalSet(nil), "eval set is nil")

	tests := []struct {
		name   string
		mutate func(*EvalSet)
		want   string
	}{
		{name: "set id", mutate: func(set *EvalSet) { set.EvalSetID = " " }, want: "evalSetId is empty"},
		{name: "cases", mutate: func(set *EvalSet) { set.EvalCases = nil }, want: "evalCases are empty"},
		{name: "threshold nan", mutate: func(set *EvalSet) { *set.PassThreshold = math.NaN() }, want: "passThreshold"},
		{name: "threshold negative", mutate: func(set *EvalSet) { *set.PassThreshold = -1 }, want: "passThreshold"},
		{name: "threshold high", mutate: func(set *EvalSet) { *set.PassThreshold = 2 }, want: "passThreshold"},
		{name: "case id", mutate: func(set *EvalSet) { set.EvalCases[0].EvalID = " " }, want: "evalId is empty"},
		{name: "duplicate case", mutate: func(set *EvalSet) {
			set.EvalCases = append(set.EvalCases, set.EvalCases[0])
		}, want: "duplicate evalId"},
		{name: "conversation count", mutate: func(set *EvalSet) { set.EvalCases[0].Conversation = nil }, want: "exactly one"},
		{name: "expected invocation", mutate: func(set *EvalSet) {
			set.EvalCases[0].Conversation = []*evalset.Invocation{nil}
		}, want: "no expected invocation"},
		{name: "nil expected tool", mutate: func(set *EvalSet) {
			set.EvalCases[0].Conversation[0].Tools = []*evalset.Tool{nil}
		}, want: "nil or unnamed"},
		{name: "unnamed expected tool", mutate: func(set *EvalSet) {
			set.EvalCases[0].Conversation[0].Tools = []*evalset.Tool{{Name: " "}}
		}, want: "nil or unnamed"},
		{name: "responses", mutate: func(set *EvalSet) { set.EvalCases[0].FakeResponses = nil }, want: "fakeResponses are empty"},
		{name: "documents", mutate: func(set *EvalSet) {
			set.EvalCases[0].Expectations.MinRetrievedDocuments = -1
		}, want: "minRetrievedDocuments"},
		{name: "format", mutate: func(set *EvalSet) {
			set.EvalCases[0].Expectations.ResponseFormat = "toml"
		}, want: "responseFormat"},
		{name: "empty fact", mutate: func(set *EvalSet) {
			set.EvalCases[0].Expectations.RequiredFacts = []string{" "}
		}, want: "required fact is empty"},
		{name: "duplicate fact", mutate: func(set *EvalSet) {
			set.EvalCases[0].Expectations.RequiredFacts = []string{"Fact", " fact "}
		}, want: "duplicate required fact"},
		{name: "variant id", mutate: func(set *EvalSet) {
			set.EvalCases[0].FakeResponses = map[string]FakeOutput{"bad id": {}}
		}, want: "variant id"},
		{name: "retrieved documents", mutate: func(set *EvalSet) {
			set.EvalCases[0].FakeResponses["baseline"] = FakeOutput{RetrievedDocuments: -1}
		}, want: "retrievedDocuments"},
		{name: "prompt hash", mutate: func(set *EvalSet) {
			set.EvalCases[0].FakeResponses["baseline"] = FakeOutput{PromptSemanticSHA256: "BAD"}
		}, want: "promptSemanticSha256"},
		{name: "rubric nan", mutate: func(set *EvalSet) {
			value := math.NaN()
			set.EvalCases[0].FakeResponses["baseline"] = FakeOutput{RubricScore: &value}
		}, want: "rubricScore"},
		{name: "rubric negative", mutate: func(set *EvalSet) {
			value := -1.0
			set.EvalCases[0].FakeResponses["baseline"] = FakeOutput{RubricScore: &value}
		}, want: "rubricScore"},
		{name: "rubric high", mutate: func(set *EvalSet) {
			value := 2.0
			set.EvalCases[0].FakeResponses["baseline"] = FakeOutput{RubricScore: &value}
		}, want: "rubricScore"},
		{name: "usage", mutate: func(set *EvalSet) {
			set.EvalCases[0].FakeResponses["baseline"] = FakeOutput{Usage: Usage{ModelCalls: -1}}
		}, want: "usage"},
		{name: "nil output tool", mutate: func(set *EvalSet) {
			set.EvalCases[0].FakeResponses["baseline"] = FakeOutput{Tools: []*evalset.Tool{nil}}
		}, want: "nil or unnamed"},
		{name: "unnamed output tool", mutate: func(set *EvalSet) {
			set.EvalCases[0].FakeResponses["baseline"] = FakeOutput{Tools: []*evalset.Tool{{Name: " "}}}
		}, want: "nil or unnamed"},
		{name: "trace id", mutate: func(set *EvalSet) {
			set.EvalCases[0].FakeResponses["baseline"] = FakeOutput{Trace: []TraceStep{{Kind: "llm"}}}
		}, want: "id is empty"},
		{name: "duplicate trace id", mutate: func(set *EvalSet) {
			set.EvalCases[0].FakeResponses["baseline"] = FakeOutput{Trace: []TraceStep{{StepID: "one"}, {StepID: "one"}}}
		}, want: "duplicate trace step"},
		{name: "invalid trace", mutate: func(set *EvalSet) {
			set.EvalCases[0].FakeResponses["baseline"] = FakeOutput{
				Response: "actual",
				Trace:    []TraceStep{{StepID: "one", Kind: "llm", Status: "completed", Message: "different"}},
			}
		}, want: "trace"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			set := newSet()
			test.mutate(set)
			assert.ErrorContains(t, validateEvalSet(set), test.want)
		})
	}

	defaulted := newSet()
	defaulted.PassThreshold = nil
	require.NoError(t, validateEvalSet(defaulted))
	require.NotNil(t, defaulted.PassThreshold)
	assert.Equal(t, 0.8, *defaulted.PassThreshold)
}

func TestJSONLoadingBoundaries(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, data, 0o600))
		return path
	}
	target := struct {
		Known string `json:"known"`
	}{}

	assert.Error(t, readJSON(filepath.Join(dir, "missing"), &target, true))
	assert.ErrorContains(t, readJSON(write("utf8.json", []byte{0xff}), &target, true), "UTF-8")
	assert.ErrorContains(t, readJSON(write("duplicate.json", []byte(`{"known":"a","known":"b"}`)), &target, true), "duplicate key")
	assert.ErrorContains(t, readJSON(write("unknown.json", []byte(`{"unknown":1}`)), &target, true), "unknown field")
	assert.NoError(t, readJSON(write("nonstrict.json", []byte(`{"unknown":1}`)), &target, false))
	assert.ErrorContains(t, readJSON(write("multiple.json", []byte(`{"known":"a"} {"known":"b"}`)), &target, true), "multiple JSON values")
	assert.ErrorContains(t, readJSON(write("trailing.json", []byte(`{"known":"a"} {`)), &target, true), "multiple JSON values")
	assert.Error(t, rejectDuplicateJSONKeys([]byte(`{"nested":[{"x":1,"x":2}]}`)))
	assert.Error(t, rejectDuplicateJSONKeys([]byte(`{"nested":`)))
	assert.Error(t, rejectDuplicateJSONKeys([]byte(`[1,`)))
	assert.NoError(t, rejectDuplicateJSONKeys([]byte(`[1,{"x":2}]`)))
}

func TestDeltaValidationFailClosedBoundaries(t *testing.T) {
	validPair := func() (*EvaluationSummary, *EvaluationSummary) {
		return deltaTestSummary("validation", 0.6, deltaTestCase("case", 0.6, true, false)),
			deltaTestSummary("validation", 0.7, deltaTestCase("case", 0.7, true, false))
	}
	baseline, candidate := validPair()
	_, err := ComputeDelta(nil, candidate)
	assert.ErrorContains(t, err, "summaries are required")
	_, err = ComputeDelta(baseline, nil)
	assert.ErrorContains(t, err, "summaries are required")

	baseline, candidate = validPair()
	candidate.EvalSetID = "other"
	_, err = ComputeDelta(baseline, candidate)
	assert.ErrorContains(t, err, "eval set mismatch")
	baseline, candidate = validPair()
	baseline.OverallScore = math.NaN()
	_, err = ComputeDelta(baseline, candidate)
	assert.ErrorContains(t, err, "overall scores")
	baseline, candidate = validPair()
	candidate.OverallScore = math.NaN()
	_, err = ComputeDelta(baseline, candidate)
	assert.ErrorContains(t, err, "overall scores")
	baseline, candidate = validPair()
	candidate.PassThreshold = 0.9
	_, err = ComputeDelta(baseline, candidate)
	assert.ErrorContains(t, err, "pass threshold mismatch")
	baseline, candidate = validPair()
	baseline.PassThreshold = -1
	candidate.PassThreshold = -1
	_, err = ComputeDelta(baseline, candidate)
	assert.ErrorContains(t, err, "baseline summary")

	invalidSummaryCases := []struct {
		name      string
		candidate bool
		mutate    func(*EvaluationSummary)
		want      string
	}{
		{name: "baseline empty", mutate: func(summary *EvaluationSummary) { summary.Cases = nil }, want: "evaluation has no cases"},
		{name: "candidate macro", candidate: true, mutate: func(summary *EvaluationSummary) { summary.OverallScore = 0.9 }, want: "macro average"},
		{name: "candidate duplicate", candidate: true, mutate: func(summary *EvaluationSummary) {
			summary.Cases = append(summary.Cases, summary.Cases[0])
			summary.OverallScore = summary.Cases[0].Score
		}, want: "duplicate case id"},
	}
	for _, test := range invalidSummaryCases {
		t.Run(test.name, func(t *testing.T) {
			baseline, candidate := validPair()
			if test.candidate {
				test.mutate(candidate)
			} else {
				test.mutate(baseline)
			}
			_, err := ComputeDelta(baseline, candidate)
			assert.ErrorContains(t, err, test.want)
		})
	}

	baseline, candidate = validPair()
	candidate.Cases[0].Critical = true
	delta, err := ComputeDelta(baseline, candidate)
	require.NoError(t, err)
	assert.False(t, delta.Complete)
	assert.Contains(t, delta.CoverageIssues, "case case critical flag changed")

	metricCase := func(id string, includeSecond bool) CaseResult {
		metrics := []MetricResult{{MetricName: "one", Score: 0.6, Threshold: 0.5, Weight: 1, Passed: true}}
		if includeSecond {
			metrics = append(metrics, MetricResult{MetricName: "two", Score: 0.6, Threshold: 0.5, Weight: 1, Passed: true})
		}
		return CaseResult{CaseID: id, Score: 0.6, Passed: true, MetricResults: metrics}
	}
	baseline = deltaTestSummary("validation", 0.6, metricCase("case", true))
	candidate = deltaTestSummary("validation", 0.6, metricCase("case", false))
	delta, err = ComputeDelta(baseline, candidate)
	require.NoError(t, err)
	assert.False(t, delta.Complete)
	assert.Contains(t, delta.CoverageIssues, "case case metric two is missing from one side")

	for _, change := range []struct {
		name   string
		mutate func(*MetricResult)
	}{
		{name: "threshold", mutate: func(metric *MetricResult) { metric.Threshold = 0.4 }},
		{name: "weight", mutate: func(metric *MetricResult) { metric.Weight = 2 }},
		{name: "hard fail", mutate: func(metric *MetricResult) { metric.HardFail = true }},
	} {
		t.Run("metric config "+change.name, func(t *testing.T) {
			baseline := deltaTestSummary("validation", 0.6, deltaTestCase("case", 0.6, true, false))
			candidateCase := deltaTestCase("case", 0.6, true, false)
			change.mutate(&candidateCase.MetricResults[0])
			candidate := deltaTestSummary("validation", 0.6, candidateCase)
			delta, err := ComputeDelta(baseline, candidate)
			require.NoError(t, err)
			assert.False(t, delta.Complete)
		})
	}
}

func TestCaseAndMetricValidationBoundaries(t *testing.T) {
	valid := func() CaseResult { return deltaTestCase("case", 0.6, true, false) }
	tests := []struct {
		name   string
		mutate func(*CaseResult)
		want   string
	}{
		{name: "score", mutate: func(result *CaseResult) { result.Score = 2 }, want: "score must be"},
		{name: "metrics", mutate: func(result *CaseResult) { result.MetricResults = nil }, want: "no metric results"},
		{name: "metric name", mutate: func(result *CaseResult) { result.MetricResults[0].MetricName = "" }, want: "metric name is empty"},
		{name: "duplicate metric", mutate: func(result *CaseResult) {
			result.MetricResults = append(result.MetricResults, result.MetricResults[0])
		}, want: "duplicate metric"},
		{name: "metric threshold", mutate: func(result *CaseResult) { result.MetricResults[0].Threshold = 2 }, want: "threshold must be"},
		{name: "metric weight", mutate: func(result *CaseResult) { result.MetricResults[0].Weight = 0 }, want: "weight must be"},
		{name: "metric pass", mutate: func(result *CaseResult) { result.MetricResults[0].Passed = false }, want: "pass status"},
		{name: "error metric score", mutate: func(result *CaseResult) {
			result.Error = "failed"
			result.MetricResults[0].Passed = false
		}, want: "score must be zero"},
		{name: "weighted score", mutate: func(result *CaseResult) { result.Score = 0.7 }, want: "weighted metric score"},
		{name: "hard fail", mutate: func(result *CaseResult) { result.HardFail = true }, want: "hard-fail status"},
		{name: "case pass", mutate: func(result *CaseResult) { result.Passed = false }, want: "pass status"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := valid()
			test.mutate(&result)
			assert.ErrorContains(t, validateCaseScores(result, 0.5), test.want)
		})
	}

	overflow := valid()
	overflow.MetricResults = []MetricResult{
		{MetricName: "one", Score: 0.6, Threshold: 0.5, Weight: math.MaxFloat64, Passed: true},
		{MetricName: "two", Score: 0.6, Threshold: 0.5, Weight: math.MaxFloat64, Passed: true},
	}
	assert.ErrorContains(t, validateCaseScores(overflow, 0.5), "total weight")

	assert.Error(t, validateMetricResult(MetricResult{Score: 2, Threshold: 0.5, Weight: 1}, false))
	assert.Error(t, validateMetricResult(MetricResult{Score: 0.5, Threshold: math.NaN(), Weight: 1}, false))
	assert.Error(t, validateMetricResult(MetricResult{Score: 0.5, Threshold: 0.5, Weight: math.NaN()}, false))
	assert.Error(t, validateMetricResult(MetricResult{Score: 0.5, Threshold: 0.5, Weight: 1}, false))
	_, err := indexCases([]CaseResult{{}})
	assert.ErrorContains(t, err, "case id is empty")
	_, err = indexMetrics([]MetricResult{{}})
	assert.ErrorContains(t, err, "metric name is empty")
}

func TestGateInputAndBudgetBoundaries(t *testing.T) {
	baseline := deltaTestSummary("validation", 0.6, deltaTestCase("case", 0.6, true, false))
	candidate := deltaTestSummary("validation", 0.7, deltaTestCase("case", 0.7, true, false))
	delta := deltaTestDelta(t, baseline, candidate)
	valid := GateInput{
		Delta:               delta,
		BaselineValidation:  baseline,
		CandidateValidation: candidate,
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
	}

	_, err := EvaluateGate(GatePolicy{MinValidationScoreGain: -1}, valid)
	assert.ErrorContains(t, err, "min validation score gain")
	for _, input := range []GateInput{
		{},
		{Delta: delta},
		{Delta: delta, BaselineValidation: baseline},
	} {
		_, err := EvaluateGate(GatePolicy{}, input)
		assert.ErrorContains(t, err, "are required")
	}

	input := valid
	input.BaselineValidation = &EvaluationSummary{}
	_, err = EvaluateGate(GatePolicy{}, input)
	assert.ErrorContains(t, err, "baseline validation")
	input = valid
	input.CandidateValidation = &EvaluationSummary{}
	_, err = EvaluateGate(GatePolicy{}, input)
	assert.ErrorContains(t, err, "candidate validation")
	input = valid
	input.BaselineUsage.ModelCalls = -1
	_, err = EvaluateGate(GatePolicy{}, input)
	assert.ErrorContains(t, err, "baseline usage")
	input = valid
	input.CandidateUsage.ToolCalls = -1
	_, err = EvaluateGate(GatePolicy{}, input)
	assert.ErrorContains(t, err, "candidate usage")
	input = valid
	input.BaselineValidation.Usage.ModelCalls = 1
	_, err = EvaluateGate(GatePolicy{}, input)
	assert.ErrorContains(t, err, "baseline gate usage")
	baseline.Usage.ModelCalls = 0
	input = valid
	input.CandidateValidation.Usage.ToolCalls = 1
	_, err = EvaluateGate(GatePolicy{}, input)
	assert.ErrorContains(t, err, "candidate gate usage")
	candidate.Usage.ToolCalls = 0

	mismatchBaseline := deltaTestSummary("one", 0.6, deltaTestCase("case", 0.6, true, false))
	mismatchCandidate := deltaTestSummary("two", 0.7, deltaTestCase("case", 0.7, true, false))
	input = valid
	input.BaselineValidation = mismatchBaseline
	input.CandidateValidation = mismatchCandidate
	_, err = EvaluateGate(GatePolicy{}, input)
	assert.ErrorContains(t, err, "recompute validation delta")

	zero := 0
	zeroFloat := 0.0
	zeroLatency := int64(0)
	regressed := deltaTestSummary("validation", 0.4, deltaTestCase("case", 0.4, false, false))
	regressionDelta, err := ComputeDelta(baseline, regressed)
	require.NoError(t, err)
	decision, err := EvaluateGate(GatePolicy{
		MaxNewFailures:         &zero,
		RejectNewHardFails:     true,
		CriticalCaseIDs:        []string{"case", "unknown"},
		MaxPerCaseScoreDrop:    &zeroFloat,
		MaxCostUSD:             &zeroFloat,
		MaxCostIncreaseRatio:   &zeroFloat,
		MaxModelCalls:          &zero,
		MaxTotalCalls:          &zero,
		MaxLatencyMS:           &zeroLatency,
		MaxCriticalScoreDrop:   0,
		MinValidationScoreGain: 0,
	}, GateInput{
		Delta:               regressionDelta,
		BaselineValidation:  baseline,
		CandidateValidation: regressed,
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
		CandidateUsage: Usage{
			ModelCalls: 2,
			ToolCalls:  2,
			CostUSD:    2,
			LatencyMS:  2,
		},
	})
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.NotEmpty(t, decision.Reasons)

	maxInt := int(^uint(0) >> 1)
	input = valid
	input.CandidateUsage = Usage{ModelCalls: maxInt, ToolCalls: 1}
	limit := maxInt
	_, err = EvaluateGate(GatePolicy{MaxTotalCalls: &limit}, input)
	assert.ErrorContains(t, err, "integer overflow")
}

func TestSummaryAndOptimizerBoundaries(t *testing.T) {
	assert.ErrorContains(t, validateSummaryForGate(nil), "summary is nil")
	invalidSummaries := []*EvaluationSummary{
		{},
		{EvalSetID: "set"},
		deltaTestSummary("set", math.NaN(), deltaTestCase("case", 0.6, true, false)),
		deltaTestSummary("set", 0.6, deltaTestCase("case", 0.6, true, false)),
		deltaTestSummary("set", 0.7, deltaTestCase("case", 0.6, true, false)),
	}
	invalidSummaries[3].PassThreshold = math.NaN()
	for _, summary := range invalidSummaries {
		assert.Error(t, validateSummaryForGate(summary))
	}

	config := newReportPipelineTestConfig()
	invalidConfig := cloneConfig(config)
	invalidConfig.SchemaVersion = ""
	_, err := NewDeterministicPromptIter(invalidConfig)
	assert.Error(t, err)
	optimizer, err := NewDeterministicPromptIter(config)
	require.NoError(t, err)
	validRequest := OptimizeRequest{Round: 1, BaselinePrompt: "prompt", Train: &EvaluationSummary{}}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = optimizer.Propose(canceled, validRequest)
	assert.ErrorIs(t, err, context.Canceled)
	for _, request := range []OptimizeRequest{
		{Round: 0, BaselinePrompt: "prompt", Train: &EvaluationSummary{}},
		{Round: 3, BaselinePrompt: "prompt", Train: &EvaluationSummary{}},
		{Round: 1, BaselinePrompt: " ", Train: &EvaluationSummary{}},
		{Round: 1, BaselinePrompt: "prompt"},
	} {
		_, err := optimizer.Propose(context.Background(), request)
		assert.Error(t, err)
	}

	for _, prompt := range []string{
		"plain",
		"[[trpc-promptiter-candidate:id;seed:1",
		"[[trpc-promptiter-candidate:id]]",
		"[[trpc-promptiter-candidate:id;seed:nope]]",
		"[[trpc-promptiter-candidate:bad id;seed:1]]",
		"[[trpc-promptiter-candidate:id;seed:1]] trailing",
	} {
		_, _, ok := promptVariantMetadata(prompt)
		assert.False(t, ok, prompt)
	}
	rules := failureDerivedRules([]FailureCategory{
		FailureFinalResponseMismatch,
		FailureToolCallError,
		FailureToolParameterError,
		FailureRouteError,
		FailureFormatError,
		FailureKnowledgeRetrievalInsufficient,
		"unknown",
	})
	assert.NotEmpty(t, rules)
}

func TestCodecovFinalMarginBoundaries(t *testing.T) {
	assert.ErrorContains(t, validateMetrics([]MetricConfig{
		{MetricName: metricRoute, Threshold: 1, Weight: math.MaxFloat64},
		{MetricName: metricFinalResponse, Threshold: 1, Weight: math.MaxFloat64},
	}), "total metric weight")

	configPath := filepath.Join(t.TempDir(), "invalid-config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{}`), 0o600))
	_, err := LoadConfig(configPath)
	assert.ErrorContains(t, err, "validate promptiter config")
	assert.Nil(t, expectedInvocation(nil))
	_, err = textSimilarity(nil, "expected", "actual")
	assert.ErrorContains(t, err, "context is nil")

	baseline := deltaTestSummary("validation", 0.6, deltaTestCase("case", 0.6, true, false))
	candidate := deltaTestSummary("validation", 0.7, deltaTestCase("case", 0.7, true, false))
	delta := deltaTestDelta(t, baseline, candidate)
	zeroRatio := 0.0
	decision, err := EvaluateGate(GatePolicy{MaxCostIncreaseRatio: &zeroRatio}, GateInput{
		Delta:               delta,
		BaselineValidation:  baseline,
		CandidateValidation: candidate,
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
		BaselineUsage:       Usage{CostUSD: 1},
		CandidateUsage:      Usage{CostUSD: 2},
	})
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.Contains(t, deltaTestCheck(t, decision, "max_cost_increase_ratio").Reason, "exceeds")

	duplicateSummary := deltaTestSummary("validation", 0.6,
		deltaTestCase("duplicate", 0.6, true, false),
		deltaTestCase("duplicate", 0.6, true, false),
	)
	assert.ErrorContains(t, validateSummaryForGate(duplicateSummary), "duplicate case id")

	invalidExtra := deltaTestCase("extra", 0.7, true, false)
	invalidExtra.MetricResults = append(invalidExtra.MetricResults, invalidExtra.MetricResults[0])
	candidateWithExtra := deltaTestSummary("validation", 0.65,
		deltaTestCase("case", 0.6, true, false),
		invalidExtra,
	)
	_, err = ComputeDelta(baseline, candidateWithExtra)
	assert.ErrorContains(t, err, "case \"extra\" candidate")

	invalidCandidate := deltaTestCase("case", 0.7, true, false)
	invalidCandidate.MetricResults = append(invalidCandidate.MetricResults, invalidCandidate.MetricResults[0])
	_, err = ComputeDelta(baseline, deltaTestSummary("validation", 0.7, invalidCandidate))
	assert.ErrorContains(t, err, "case \"case\" candidate")

	errorResult := CaseResult{
		CaseID:   "case",
		Score:    0.5,
		Passed:   false,
		HardFail: true,
		Error:    "execution failed",
		MetricResults: []MetricResult{{
			MetricName: "quality",
			Score:      0,
			Threshold:  0.5,
			Weight:     1,
			Passed:     false,
		}},
	}
	assert.ErrorContains(t, validateCaseScores(errorResult, 0.5), "case score must be zero")

	validMetric := MetricResult{MetricName: "quality", Score: 0.6, Threshold: 0.5, Weight: 1, Passed: true}
	duplicateMetrics := []MetricResult{validMetric, validMetric}
	_, _, _, err = compareMetrics("case",
		CaseResult{MetricResults: duplicateMetrics},
		CaseResult{MetricResults: []MetricResult{validMetric}},
	)
	assert.ErrorContains(t, err, "baseline metrics")
	_, _, _, err = compareMetrics("case",
		CaseResult{MetricResults: []MetricResult{validMetric}},
		CaseResult{MetricResults: duplicateMetrics},
	)
	assert.ErrorContains(t, err, "candidate metrics")

	extraMetric := MetricResult{MetricName: "extra", Score: 0.6, Threshold: 0.5, Weight: 1, Passed: true}
	_, complete, issues, err := compareMetrics("case",
		CaseResult{MetricResults: []MetricResult{validMetric}},
		CaseResult{MetricResults: []MetricResult{validMetric, extraMetric}},
	)
	require.NoError(t, err)
	assert.False(t, complete)
	assert.NotEmpty(t, issues)

	invalidMetric := validMetric
	invalidMetric.Threshold = 2
	_, _, _, err = compareMetrics("case",
		CaseResult{MetricResults: []MetricResult{invalidMetric}},
		CaseResult{MetricResults: []MetricResult{validMetric}},
	)
	assert.ErrorContains(t, err, "baseline metric")
	_, _, _, err = compareMetrics("case",
		CaseResult{MetricResults: []MetricResult{validMetric}},
		CaseResult{MetricResults: []MetricResult{invalidMetric}},
	)
	assert.ErrorContains(t, err, "candidate metric")
}
