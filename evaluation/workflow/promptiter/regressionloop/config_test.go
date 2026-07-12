//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigValidateRequiresCoreInputs(t *testing.T) {
	cfg := Config{
		AppName:             "app",
		PromptSource:        "prompt.txt",
		MetricsPath:         "metrics.json",
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          "report.json",
		OutputMarkdown:      "report.md",
		TargetSurfaceIDs:    []string{"agent#instruction"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
	}
	require.NoError(t, cfg.Validate())

	cfg.MetricsPath = ""
	assert.ErrorContains(t, cfg.Validate(), "metrics path is empty")

	cfg.MetricsPath = "metrics.json"
	cfg.Attribution = AttributionConfig{
		MetricCategoryHints: map[string]FailureCategory{"custom": "not_a_category"},
	}
	assert.ErrorContains(t, cfg.Validate(), "unknown failure category")

	cfg.Attribution = AttributionConfig{}
	cfg.Gate.HardFailMetricNames = []string{" "}
	assert.ErrorContains(t, cfg.Validate(), "gate hard fail metric name is empty")

	cfg.Gate.HardFailMetricNames = nil
	cfg.TargetSurfaceIDs = []string{"agent#instruction", " "}
	assert.ErrorContains(t, cfg.Validate(), "target surface id is empty")
}

func TestConfigValidateReportsAllMissingCoreInputs(t *testing.T) {
	err := (Config{}).Validate()
	require.Error(t, err)
	for _, want := range []string{
		"app name is empty",
		"prompt source is empty",
		"metrics path is empty",
		"train eval set id is empty",
		"validation eval set id is empty",
		"output JSON path is empty",
		"output markdown path is empty",
		"target surface ids are empty",
		"promptiter max rounds must be greater than 0",
	} {
		assert.ErrorContains(t, err, want)
	}

	err = (Config{Gate: GateConfig{RequireEngineAccepted: true}}).Validate()
	assert.ErrorContains(t, err, "engine acceptance requires promptiter rounds")

	cfg := Config{
		AppName:             "app",
		PromptSource:        "prompt.txt",
		MetricsPath:         "metrics.json",
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          "report.json",
		OutputMarkdown:      "report.md",
		TargetSurfaceIDs:    []string{"agent#instruction"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
		Attribution: AttributionConfig{
			MetricCategoryHints: map[string]FailureCategory{"": FailureRouteError},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "empty metric name")
}

func TestLoadMetricDefinitionsAcceptsArrayAndWrappedMetrics(t *testing.T) {
	dir := t.TempDir()
	arrayPath := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(arrayPath, []byte(`[
		{"metricName":"final_response","threshold":1,"failureCategory":"final_response_mismatch"},
		{"metricName":"tool_trajectory","evaluatorName":"tool","threshold":0.8}
	]`), 0o644))
	defs, err := LoadMetricDefinitions(arrayPath)
	require.NoError(t, err)
	require.Len(t, defs, 2)
	assert.Equal(t, []string{"final_response", "tool_trajectory"}, metricNames(defs))
	assert.Equal(t, FailureFinalResponseMismatch, defs[0].FailureCategory)

	wrappedPath := filepath.Join(dir, "wrapped.json")
	require.NoError(t, os.WriteFile(wrappedPath, []byte(`{"metrics":[{"metricName":"json_format"}]}`), 0o644))
	defs, err = LoadMetricDefinitions(wrappedPath)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	assert.Equal(t, "json_format", defs[0].MetricName)
}

func TestLoadMetricDefinitionsRejectsInvalidMetrics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(path, []byte(`[{"metricName":"m"},{"metricName":"m"}]`), 0o644))
	_, err := LoadMetricDefinitions(path)
	assert.ErrorContains(t, err, "duplicate metric")

	require.NoError(t, os.WriteFile(path, []byte(`[{"metricName":"m","failureCategory":"bad"}]`), 0o644))
	_, err = LoadMetricDefinitions(path)
	assert.ErrorContains(t, err, "unknown failureCategory")

	require.NoError(t, os.WriteFile(path, []byte(`[{"metricName":""}]`), 0o644))
	_, err = LoadMetricDefinitions(path)
	assert.ErrorContains(t, err, "empty metricName")

	require.NoError(t, os.WriteFile(path, []byte(`{bad json`), 0o644))
	_, err = LoadMetricDefinitions(path)
	assert.ErrorContains(t, err, "decode metrics path")

	_, err = LoadMetricDefinitions(filepath.Join(dir, "missing.json"))
	assert.ErrorContains(t, err, "read metrics path")
}

func TestBuildRunRequestCarriesLossHintsAndPolicies(t *testing.T) {
	target := 0.9
	cfg := Config{
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		TargetSurfaceIDs:    []string{"agent#instruction"},
		PromptIter: PromptIterConfig{
			MaxRounds:                  2,
			MinScoreGain:               0.1,
			MaxRoundsWithoutAcceptance: 1,
			TargetScore:                &target,
			EvalCaseParallelism:        3,
		},
	}
	req := cfg.BuildRunRequest(nil)
	require.Len(t, req.Train, 1)
	require.Len(t, req.Validation, 1)
	assert.Equal(t, "train", req.Train[0].EvalSetID)
	assert.Equal(t, "validation", req.Validation[0].EvalSetID)
	assert.Equal(t, 2, req.MaxRounds)
	assert.Equal(t, 0.1, req.AcceptancePolicy.MinScoreGain)
	assert.Equal(t, 3, req.EvaluationOptions.EvalCaseParallelism)
	require.NotNil(t, req.StopPolicy.TargetScore)
	assert.Equal(t, target, *req.StopPolicy.TargetScore)
}

func TestDurationJSONRoundTrip(t *testing.T) {
	data, err := json.Marshal(Duration{Duration: 150 * time.Millisecond})
	require.NoError(t, err)
	assert.JSONEq(t, `"150ms"`, string(data))

	var parsed Duration
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, 150*time.Millisecond, parsed.Duration)

	require.NoError(t, json.Unmarshal([]byte(`1000000`), &parsed))
	assert.Equal(t, time.Millisecond, parsed.Duration)

	assert.Error(t, json.Unmarshal([]byte(`"not-a-duration"`), &parsed))
	assert.Error(t, json.Unmarshal([]byte(`{}`), &parsed))
}

func TestGateConfigOmitsUnsetMaxLatency(t *testing.T) {
	data, err := json.Marshal(GateConfig{})
	require.NoError(t, err)
	assert.NotContains(t, string(data), "maxLatency")

	latency := Duration{Duration: time.Second}
	data, err = json.Marshal(GateConfig{MaxLatency: &latency})
	require.NoError(t, err)
	assert.Contains(t, string(data), `"maxLatency":"1s"`)
}
