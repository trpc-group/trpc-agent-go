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
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestLoadMetricsAndPromptBoundaries(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, data, 0o600))
		return path
	}

	metrics, err := LoadMetrics(write("metrics.json", []byte(`[
  {"metricName":"final_response_match","threshold":0.5,"weight":1},
  {"metricName":"route_accuracy","threshold":1,"weight":2,"hardFail":true}
]`)))
	require.NoError(t, err)
	require.Len(t, metrics, 2)
	assert.Equal(t, metricRoute, metrics[1].MetricName)

	metricCases := []struct {
		name    string
		content string
		want    string
	}{
		{name: "missing", content: "", want: "load metrics"},
		{name: "empty", content: `[]`, want: "metrics are empty"},
		{name: "empty name", content: `[{"metricName":"","threshold":0.5,"weight":1}]`, want: "name is empty"},
		{name: "duplicate", content: `[{"metricName":"route_accuracy","threshold":1,"weight":1},{"metricName":"route_accuracy","threshold":1,"weight":1}]`, want: "duplicate metric"},
		{name: "unsupported", content: `[{"metricName":"accuracy","threshold":1,"weight":1}]`, want: "unsupported metric"},
		{name: "threshold", content: `[{"metricName":"route_accuracy","threshold":2,"weight":1}]`, want: "threshold must be"},
		{name: "weight", content: `[{"metricName":"route_accuracy","threshold":1,"weight":0}]`, want: "weight must be"},
	}
	for _, test := range metricCases {
		t.Run("metrics_"+test.name, func(t *testing.T) {
			path := filepath.Join(dir, "does-not-exist.json")
			if test.content != "" {
				path = write("metrics-"+test.name+".json", []byte(test.content))
			}
			_, err := LoadMetrics(path)
			require.Error(t, err)
			assert.ErrorContains(t, err, test.want)
		})
	}
	assert.ErrorContains(t, validateMetrics([]MetricConfig{{
		MetricName: metricRoute,
		Threshold:  math.NaN(),
		Weight:     1,
	}}), "threshold must be")
	assert.ErrorContains(t, validateMetrics([]MetricConfig{{
		MetricName: metricRoute,
		Threshold:  1,
		Weight:     math.Inf(1),
	}}), "weight must be")

	prompt, err := LoadPrompt(write("prompt.txt", []byte("  answer carefully  \n")))
	require.NoError(t, err)
	assert.Equal(t, "answer carefully", prompt)
	_, err = LoadPrompt(filepath.Join(dir, "missing-prompt.txt"))
	assert.ErrorContains(t, err, "read prompt")
	_, err = LoadPrompt(write("empty.txt", []byte(" \n\t")))
	assert.ErrorContains(t, err, "prompt is empty")
	_, err = LoadPrompt(write("invalid.txt", []byte{0xff}))
	assert.ErrorContains(t, err, "not valid UTF-8")
}

func TestTraceValueAndStructuredOutputBoundaries(t *testing.T) {
	stringsValue, ok := traceStrings([]string{"a", "b"})
	require.True(t, ok)
	assert.Equal(t, []string{"a", "b"}, stringsValue)
	stringsValue, ok = traceStrings([]any{"a", "b"})
	require.True(t, ok)
	assert.Equal(t, []string{"a", "b"}, stringsValue)
	_, ok = traceStrings([]any{"a", 2})
	assert.False(t, ok)
	_, ok = traceStrings("a")
	assert.False(t, ok)

	integerCases := []struct {
		name  string
		value any
		want  int
		ok    bool
	}{
		{name: "int", value: 2, want: 2, ok: true},
		{name: "negative int", value: -1, want: -1},
		{name: "int64", value: int64(3), want: 3, ok: true},
		{name: "negative int64", value: int64(-1)},
		{name: "float", value: float64(4), want: 4, ok: true},
		{name: "fraction", value: 4.5},
		{name: "number", value: json.Number("5"), want: 5, ok: true},
		{name: "invalid number", value: json.Number("5.5")},
		{name: "string", value: "5"},
	}
	for _, test := range integerCases {
		t.Run(test.name, func(t *testing.T) {
			got, ok := traceInteger(test.value)
			assert.Equal(t, test.ok, ok)
			assert.Equal(t, test.want, got)
		})
	}

	assert.True(t, normalizedStringSetEqual([]string{" Alpha ", "beta"}, []string{"BETA", "alpha"}))
	assert.False(t, normalizedStringSetEqual([]string{"alpha", "alpha"}, []string{"alpha"}))
	assert.False(t, normalizedStringSetEqual([]string{""}, []string{""}))
	assert.False(t, normalizedStringSetEqual([]string{"alpha"}, []string{"beta"}))

	structuredFalse := false
	structuredTrue := true
	structuredCases := []struct {
		name   string
		format string
		output FakeOutput
		want   bool
	}{
		{name: "empty", output: FakeOutput{}, want: true},
		{name: "json", format: "json", output: FakeOutput{Response: `{"ok":true}`}, want: true},
		{name: "invalid json", format: "json", output: FakeOutput{Response: `{`}},
		{name: "json override", format: "json", output: FakeOutput{Response: `{}`, StructuredValid: &structuredFalse}},
		{name: "xml", format: "xml", output: FakeOutput{Response: `<root><item/></root>`}, want: true},
		{name: "multiple xml roots", format: "xml", output: FakeOutput{Response: `<a/><b/>`}},
		{name: "xml text outside root", format: "xml", output: FakeOutput{Response: `text<a/>`}},
		{name: "invalid xml", format: "xml", output: FakeOutput{Response: `<a>`}},
		{name: "yaml", format: "yaml", output: FakeOutput{Response: "key: value\n"}, want: true},
		{name: "empty yaml", format: "yaml", output: FakeOutput{Response: "  "}},
		{name: "multiple yaml", format: "yaml", output: FakeOutput{Response: "a: 1\n---\nb: 2\n"}},
		{name: "custom true", format: "custom", output: FakeOutput{StructuredValid: &structuredTrue}, want: true},
		{name: "custom missing", format: "custom", output: FakeOutput{}},
	}
	for _, test := range structuredCases {
		t.Run("structured_"+test.name, func(t *testing.T) {
			assert.Equal(t, test.want, determineStructuredValidity(test.format, test.output))
		})
	}
}

func TestGateAndSelectionHelperBoundaries(t *testing.T) {
	negativeInt := -1
	negativeInt64 := int64(-1)
	negativeFloat := -1.0
	invalidPolicies := []GatePolicy{
		{MinValidationScoreGain: math.NaN()},
		{MinValidationScoreGain: -1},
		{MaxCriticalScoreDrop: math.Inf(1)},
		{MaxCriticalScoreDrop: -1},
		{MaxNewFailures: &negativeInt},
		{MaxPerCaseScoreDrop: &negativeFloat},
		{MaxCostUSD: &negativeFloat},
		{MaxCostIncreaseRatio: &negativeFloat},
		{MaxModelCalls: &negativeInt},
		{MaxTotalCalls: &negativeInt},
		{MaxLatencyMS: &negativeInt64},
	}
	for i, policy := range invalidPolicies {
		t.Run("invalid_policy_"+string(rune('a'+i)), func(t *testing.T) {
			assert.Error(t, validateGatePolicy(policy))
		})
	}
	assert.NoError(t, validateGatePolicy(GatePolicy{}))

	worst, ids := worstCaseDrop([]CaseDelta{
		{CaseID: "b", ScoreDelta: -0.4},
		{CaseID: "a", ScoreDelta: -0.4},
		{CaseID: "improved", ScoreDelta: 0.5},
	})
	assert.InDelta(t, 0.4, worst, 1e-9)
	assert.Equal(t, []string{"a", "b"}, ids)
	assert.Equal(t, "evaluation coverage is incomplete: [missing]", comparableReason(&DeltaSummary{
		CoverageIssues: []string{"missing"},
	}))

	ratio, measurable := costIncreaseRatio(0, 0)
	assert.True(t, measurable)
	assert.Zero(t, ratio)
	_, measurable = costIncreaseRatio(0, 1)
	assert.False(t, measurable)
	ratio, measurable = costIncreaseRatio(2, 3)
	assert.True(t, measurable)
	assert.InDelta(t, 0.5, ratio, 1e-9)

	accepted := &roundSelection{decision: GateDecision{Accepted: true}, round: 2}
	rejected := &roundSelection{decision: GateDecision{Accepted: false}, round: 1}
	assert.True(t, betterSelection(accepted, nil))
	assert.True(t, betterSelection(accepted, rejected))
	assert.False(t, betterSelection(rejected, accepted))

	incumbent := &roundSelection{
		decision:   GateDecision{Accepted: true},
		evaluation: EvaluationPair{Validation: EvaluationSummary{OverallScore: 0.5}},
		delta:      DeltaPair{Validation: DeltaSummary{NewHardFails: 1, NewFailures: 2}},
		usage:      Usage{CostUSD: 2},
		round:      2,
	}
	candidate := *incumbent
	candidate.evaluation.Validation.OverallScore = 0.6
	assert.True(t, betterSelection(&candidate, incumbent))
	candidate = *incumbent
	candidate.delta.Validation.NewHardFails = 0
	assert.True(t, betterSelection(&candidate, incumbent))
	candidate = *incumbent
	candidate.delta.Validation.NewFailures = 1
	assert.True(t, betterSelection(&candidate, incumbent))
	candidate = *incumbent
	candidate.usage.CostUSD = 1
	assert.True(t, betterSelection(&candidate, incumbent))
	candidate = *incumbent
	candidate.round = 1
	assert.True(t, betterSelection(&candidate, incumbent))
}

func TestValidateCandidateBoundaries(t *testing.T) {
	config := newReportPipelineTestConfig()
	optimizer, err := NewDeterministicPromptIter(config)
	require.NoError(t, err)
	pipeline := &Pipeline{config: config}
	newCandidate := func(t *testing.T) *Candidate {
		t.Helper()
		candidate, err := optimizer.Propose(context.Background(), OptimizeRequest{
			Round:          1,
			BaselinePrompt: "baseline prompt",
			Train:          &EvaluationSummary{},
		})
		require.NoError(t, err)
		return candidate
	}

	assert.NoError(t, pipeline.validateCandidate(newCandidate(t), 1, map[string]struct{}{}))
	assert.ErrorContains(t, pipeline.validateCandidate(nil, 1, nil), "nil candidate")

	tests := []struct {
		name   string
		mutate func(*Candidate)
		round  int
		seen   map[string]struct{}
		want   string
	}{
		{name: "invalid id", mutate: func(candidate *Candidate) { candidate.ID = "invalid id" }, round: 1, want: "id"},
		{name: "reserved id", mutate: func(candidate *Candidate) { candidate.ID = "baseline" }, round: 1, want: "reserved"},
		{name: "wrong configured id", mutate: func(candidate *Candidate) { candidate.ID = "different" }, round: 1, want: "configured round"},
		{name: "duplicate id", round: 1, seen: map[string]struct{}{"accepted": {}}, want: "already evaluated"},
		{name: "wrong round", mutate: func(candidate *Candidate) { candidate.Round = 2 }, round: 1, want: "does not match"},
		{name: "empty prompt", mutate: func(candidate *Candidate) { candidate.Prompt = " " }, round: 1, want: "prompt is empty"},
		{name: "invalid utf8", mutate: func(candidate *Candidate) { candidate.Reason = string([]byte{0xff}) }, round: 1, want: "valid UTF-8"},
		{name: "missing marker", mutate: func(candidate *Candidate) { candidate.Prompt = "plain prompt" }, round: 1, want: "exactly one"},
		{name: "duplicate marker", mutate: func(candidate *Candidate) { candidate.Prompt += "\n" + candidate.Prompt }, round: 1, want: "exactly one"},
		{name: "marker id", mutate: func(candidate *Candidate) {
			candidate.Prompt = "prompt\n\n[[trpc-promptiter-candidate:overfit;seed:424242]]"
		}, round: 1, want: "does not match id"},
		{name: "marker seed", mutate: func(candidate *Candidate) {
			candidate.Prompt = "prompt\n\n[[trpc-promptiter-candidate:accepted;seed:7]]"
		}, round: 1, want: "does not match config seed"},
		{name: "hash", mutate: func(candidate *Candidate) { candidate.PromptHash = "wrong" }, round: 1, want: "computed hash"},
		{name: "surface", mutate: func(candidate *Candidate) { candidate.SurfaceID = "wrong" }, round: 1, want: "does not match target"},
		{name: "reason", mutate: func(candidate *Candidate) { candidate.Reason = " " }, round: 1, want: "reason is empty"},
		{name: "profile", mutate: func(candidate *Candidate) { candidate.Profile = nil }, round: 1, want: "profile and patch set"},
		{name: "patch set", mutate: func(candidate *Candidate) { candidate.PatchSet = nil }, round: 1, want: "profile and patch set"},
		{name: "structure", mutate: func(candidate *Candidate) { candidate.Profile.StructureID = "wrong" }, round: 1, want: "exactly one target"},
		{name: "overrides", mutate: func(candidate *Candidate) { candidate.Profile.Overrides = nil }, round: 1, want: "exactly one target"},
		{name: "override surface", mutate: func(candidate *Candidate) { candidate.Profile.Overrides[0].SurfaceID = "wrong" }, round: 1, want: "override does not match"},
		{name: "override prompt", mutate: func(candidate *Candidate) {
			wrong := "wrong"
			candidate.Profile.Overrides[0].Value.Text = &wrong
		}, round: 1, want: "override does not match"},
		{name: "patch count", mutate: func(candidate *Candidate) { candidate.PatchSet.Patches = nil }, round: 1, want: "exactly one patch"},
		{name: "patch surface", mutate: func(candidate *Candidate) { candidate.PatchSet.Patches[0].SurfaceID = "wrong" }, round: 1, want: "patch does not match"},
		{name: "patch reason", mutate: func(candidate *Candidate) { candidate.PatchSet.Patches[0].Reason = "wrong" }, round: 1, want: "patch does not match"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := newCandidate(t)
			if test.mutate != nil {
				test.mutate(candidate)
			}
			err := pipeline.validateCandidate(candidate, test.round, test.seen)
			require.Error(t, err)
			assert.ErrorContains(t, err, test.want)
		})
	}
}

func TestConfigValidationBoundaries(t *testing.T) {
	valid := newReportPipelineTestConfig()
	require.NoError(t, validateConfig(&valid))
	assert.ErrorContains(t, validateConfig(nil), "config is nil")

	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "schema", mutate: func(config *Config) { config.SchemaVersion = " " }, want: "schemaVersion"},
		{name: "mode", mutate: func(config *Config) { config.Mode = "remote" }, want: "unsupported"},
		{name: "rounds zero", mutate: func(config *Config) { config.MaxRounds = 0 }, want: "greater than 0"},
		{name: "rounds exceed", mutate: func(config *Config) { config.MaxRounds = 3 }, want: "exceeds"},
		{name: "surface id", mutate: func(config *Config) { config.Surface.NodeID = " " }, want: "are required"},
		{name: "surface type", mutate: func(config *Config) { config.Surface.Type = "model" }, want: "not a supported"},
		{name: "candidate empty id", mutate: func(config *Config) { config.Candidates[0].ID = "" }, want: "id is empty"},
		{name: "candidate invalid id", mutate: func(config *Config) { config.Candidates[0].ID = "bad id" }, want: "may contain only"},
		{name: "candidate reserved id", mutate: func(config *Config) { config.Candidates[0].ID = "baseline" }, want: "reserved"},
		{name: "candidate duplicate id", mutate: func(config *Config) { config.Candidates[1].ID = config.Candidates[0].ID }, want: "duplicate candidate"},
		{name: "candidate prompt", mutate: func(config *Config) { config.Candidates[0].AppendPrompt = " " }, want: "appendPrompt is empty"},
		{name: "candidate marker", mutate: func(config *Config) {
			config.Candidates[0].AppendPrompt = promptVariantMarkerPrefix
		}, want: "reserved variant marker"},
		{name: "candidate reason", mutate: func(config *Config) { config.Candidates[0].Reason = " " }, want: "reason is empty"},
		{name: "candidate category", mutate: func(config *Config) {
			config.Candidates[0].AddressCategories = []FailureCategory{"unknown"}
		}, want: "unknown failure category"},
		{name: "candidate duplicate category", mutate: func(config *Config) {
			config.Candidates[0].AddressCategories = []FailureCategory{FailureRouteError, FailureRouteError}
		}, want: "duplicate failure category"},
		{name: "engine name", mutate: func(config *Config) { config.FakeEngine.Name = " " }, want: "name and version"},
		{name: "fallback invalid", mutate: func(config *Config) { config.FakeEngine.FallbackVariant = "bad id" }, want: "fallbackVariant is invalid"},
		{name: "fallback non baseline", mutate: func(config *Config) { config.FakeEngine.FallbackVariant = "accepted" }, want: "must be"},
		{name: "negative gain", mutate: func(config *Config) { config.Gate.MinValidationScoreGain = -1 }, want: "cannot be negative"},
		{name: "critical empty", mutate: func(config *Config) { config.Gate.CriticalCaseIDs = []string{" "} }, want: "empty id"},
		{name: "critical duplicate", mutate: func(config *Config) {
			config.Gate.CriticalCaseIDs = []string{"case", "case"}
		}, want: "duplicate critical case"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := cloneConfig(valid)
			test.mutate(&config)
			assert.ErrorContains(t, validateConfig(&config), test.want)
		})
	}

	configForFile := cloneConfig(valid)
	configForFile.FakeEngine.FallbackVariant = ""
	data, err := json.Marshal(configForFile)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "promptiter.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	loaded, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "baseline", loaded.FakeEngine.FallbackVariant)
	_, err = LoadConfig(filepath.Join(t.TempDir(), "missing.json"))
	assert.ErrorContains(t, err, "load promptiter config")
}

func TestUsageArithmeticBoundaries(t *testing.T) {
	invalid := []Usage{
		{ModelCalls: -1},
		{ToolCalls: -1},
		{InputTokens: -1},
		{OutputTokens: -1},
		{CostUSD: -1},
		{CostUSD: math.NaN()},
		{CostUSD: math.Inf(1)},
		{LatencyMS: -1},
	}
	for i, usage := range invalid {
		t.Run("invalid_"+string(rune('a'+i)), func(t *testing.T) {
			assert.Error(t, validateUsage(usage))
		})
	}
	assert.NoError(t, validateUsage(Usage{}))

	sum, err := (Usage{
		ModelCalls: 1, ToolCalls: 2, InputTokens: 3, OutputTokens: 4, CostUSD: 0.5, LatencyMS: 6,
	}).AddChecked(Usage{
		ModelCalls: 6, ToolCalls: 5, InputTokens: 4, OutputTokens: 3, CostUSD: 0.25, LatencyMS: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, Usage{
		ModelCalls: 7, ToolCalls: 7, InputTokens: 7, OutputTokens: 7, CostUSD: 0.75, LatencyMS: 7,
	}, sum)

	_, err = (Usage{ModelCalls: -1}).AddChecked(Usage{})
	assert.ErrorContains(t, err, "left usage")
	_, err = (Usage{}).AddChecked(Usage{ToolCalls: -1})
	assert.ErrorContains(t, err, "right usage")

	maxInt := int(^uint(0) >> 1)
	maxInt64 := int64(^uint64(0) >> 1)
	overflows := []struct {
		name  string
		left  Usage
		right Usage
		want  string
	}{
		{name: "model", left: Usage{ModelCalls: maxInt}, right: Usage{ModelCalls: 1}, want: "model calls"},
		{name: "tool", left: Usage{ToolCalls: maxInt}, right: Usage{ToolCalls: 1}, want: "tool calls"},
		{name: "input", left: Usage{InputTokens: maxInt}, right: Usage{InputTokens: 1}, want: "input tokens"},
		{name: "output", left: Usage{OutputTokens: maxInt}, right: Usage{OutputTokens: 1}, want: "output tokens"},
		{name: "latency", left: Usage{LatencyMS: maxInt64}, right: Usage{LatencyMS: 1}, want: "latency"},
		{name: "cost", left: Usage{CostUSD: math.MaxFloat64}, right: Usage{CostUSD: math.MaxFloat64}, want: "cost overflow"},
	}
	for _, test := range overflows {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.left.AddChecked(test.right)
			assert.ErrorContains(t, err, test.want)
		})
	}

	_, err = checkedAddInt(-1, 0)
	assert.ErrorContains(t, err, "negative")
	_, err = checkedAddInt64(-1, 0)
	assert.ErrorContains(t, err, "negative")
}

func TestEvaluatorMetricBoundaries(t *testing.T) {
	metric := MetricConfig{MetricName: metricRoute, Threshold: 1, Weight: 1}
	_, err := NewLocalEvaluator(nil, "baseline")
	assert.ErrorContains(t, err, "metrics are empty")
	_, err = NewLocalEvaluator([]MetricConfig{metric}, "baseline", "fake", "trace")
	assert.ErrorContains(t, err, "at most one runtime mode")
	_, err = NewLocalEvaluator([]MetricConfig{metric}, "baseline", "remote")
	assert.ErrorContains(t, err, "unsupported local evaluator mode")
	evaluator, err := NewLocalEvaluator([]MetricConfig{metric}, "")
	require.NoError(t, err)
	assert.Equal(t, "fake", evaluator.RuntimeMode())
	assert.Equal(t, "baseline", evaluator.fallbackVariant)
	evaluator, err = NewLocalEvaluator([]MetricConfig{metric}, "baseline", "trace")
	require.NoError(t, err)
	assert.Equal(t, "trace", evaluator.RuntimeMode())

	ctx := context.Background()
	emptyExpected := &evalset.Invocation{}
	responseExpected := &evalset.Invocation{FinalResponse: &model.Message{Content: "same answer"}}
	trueValue := true
	rubricScore := 1.2
	tests := []struct {
		name         string
		metric       string
		expected     *evalset.Invocation
		expectations Expectations
		output       FakeOutput
		structured   bool
		want         float64
	}{
		{name: "response absent", metric: metricFinalResponse, expected: emptyExpected, want: 1},
		{name: "response exact", metric: metricFinalResponse, expected: responseExpected, output: FakeOutput{Response: "same answer"}, want: 1},
		{name: "tools empty", metric: metricToolTrajectory, expected: emptyExpected, want: 1},
		{name: "route absent", metric: metricRoute, expected: emptyExpected, want: 1},
		{name: "route match", metric: metricRoute, expected: emptyExpected, expectations: Expectations{Route: "billing"}, output: FakeOutput{Route: "billing"}, want: 1},
		{name: "route mismatch", metric: metricRoute, expected: emptyExpected, expectations: Expectations{Route: "billing"}, output: FakeOutput{Route: "support"}},
		{name: "format absent", metric: metricStructuredOutput, expected: emptyExpected, want: 1},
		{name: "format valid", metric: metricStructuredOutput, expected: emptyExpected, expectations: Expectations{ResponseFormat: "json"}, structured: true, want: 1},
		{name: "format invalid", metric: metricStructuredOutput, expected: emptyExpected, expectations: Expectations{ResponseFormat: "json"}},
		{name: "knowledge complete", metric: metricKnowledgeRecall, expected: emptyExpected, expectations: Expectations{RequiredFacts: []string{"fact"}}, output: FakeOutput{RetrievedFacts: []string{"FACT"}}, want: 1},
		{name: "knowledge incomplete", metric: metricKnowledgeRecall, expected: emptyExpected, expectations: Expectations{RequiredFacts: []string{"fact"}}, output: FakeOutput{}},
		{name: "rubric supplied", metric: metricLLMRubric, expected: emptyExpected, output: FakeOutput{RubricScore: &rubricScore, StructuredValid: &trueValue}, want: 1},
		{name: "rubric absent", metric: metricLLMRubric, expected: emptyExpected, want: 1},
		{name: "unsupported", metric: "unknown", expected: emptyExpected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, _, err := scoreMetric(ctx, test.metric, test.expected, test.expectations, test.output, test.structured)
			require.NoError(t, err)
			assert.InDelta(t, test.want, got, 1e-9)
		})
	}
}
