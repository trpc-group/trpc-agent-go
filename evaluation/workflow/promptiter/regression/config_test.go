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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadRunConfigResolvesNativeInputsAndRawHashes(t *testing.T) {
	fixture := newConfigFixture(t)
	config, err := LoadRunConfig(context.Background(), "regression-test", fixture.files)
	require.NoError(t, err)

	require.Equal(t, "issue-2003-report", config.ReportID)
	require.Equal(t, int64(2003), config.Seed)
	require.Equal(t, 20, config.EvidenceLimit)
	require.Equal(t, "optimization_report.json", config.Output.JSON)
	require.Equal(t, "optimization_report.md", config.Output.Markdown)
	require.Equal(t, []string{"train-format", "train-response", "train-tool"}, config.Train.CaseIDs)
	require.Equal(t, []string{"valid-args", "valid-fact", "valid-route"}, config.Validation.CaseIDs)
	require.Equal(t, []string{"quality", "safety"}, config.Train.MetricNames)
	require.Len(t, config.InitialProfile.Overrides, 1)
	require.Equal(t, "agent#instruction", config.InitialProfile.Overrides[0].SurfaceID)
	require.Equal(t, fixture.rawHash(t, fixture.files.BaselinePrompt), config.InputHashes["baselinePrompt"])
	require.Equal(t, fixture.rawHash(t, fixture.files.TrainEvalSet), config.InputHashes["trainEvalSet"])

	evalManager, metricManager, err := NewInputManagers(fixture.files, config)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, evalManager.Close())
		require.NoError(t, metricManager.Close())
	})
	trainSet, err := evalManager.Get(context.Background(), "regression-test", config.Train.EvalSetID)
	require.NoError(t, err)
	require.Len(t, trainSet.EvalCases, 3)
	validationSet, err := evalManager.Get(
		context.Background(),
		"regression-test",
		config.Validation.EvalSetID,
	)
	require.NoError(t, err)
	require.Len(t, validationSet.EvalCases, 3)
	metricNames, err := metricManager.List(
		context.Background(),
		"regression-test",
		config.Validation.EvalSetID,
	)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"quality", "safety"}, metricNames)
}

func TestNewInputManagersVerifyAndFreezeHashedInputs(t *testing.T) {
	t.Run("rejects source changed after config load", func(t *testing.T) {
		fixture := newConfigFixture(t)
		config, err := LoadRunConfig(context.Background(), "regression-test", fixture.files)
		require.NoError(t, err)
		data, err := os.ReadFile(fixture.files.TrainEvalSet)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(
			fixture.files.TrainEvalSet,
			append(data, ' '),
			0o600,
		))

		_, _, err = NewInputManagers(fixture.files, config)
		require.ErrorContains(t, err, "train eval-set input hash")
	})

	t.Run("uses immutable verified bytes", func(t *testing.T) {
		fixture := newConfigFixture(t)
		config, err := LoadRunConfig(context.Background(), "regression-test", fixture.files)
		require.NoError(t, err)
		evalManager, metricManager, err := NewInputManagers(fixture.files, config)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, evalManager.Close())
			require.NoError(t, metricManager.Close())
		})

		before, err := evalManager.Get(
			context.Background(),
			"regression-test",
			config.Train.EvalSetID,
		)
		require.NoError(t, err)
		require.Len(t, before.EvalCases, 3)
		require.NoError(t, os.WriteFile(fixture.files.TrainEvalSet, []byte(`{}`), 0o600))
		require.NoError(t, os.WriteFile(fixture.files.Metrics, []byte(`[]`), 0o600))

		after, err := evalManager.Get(
			context.Background(),
			"regression-test",
			config.Train.EvalSetID,
		)
		require.NoError(t, err)
		require.Equal(t, before, after)
		metricNames, err := metricManager.List(
			context.Background(),
			"regression-test",
			config.Train.EvalSetID,
		)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"quality", "safety"}, metricNames)
	})
}

func TestBindRuntimeRefreshesEvaluatorProvenanceAndCopiesInput(t *testing.T) {
	fixture := newConfigFixture(t)
	first, err := LoadRunConfig(context.Background(), "regression-test", fixture.files)
	require.NoError(t, err)
	second, err := LoadRunConfig(context.Background(), "regression-test", fixture.files)
	require.NoError(t, err)

	runtime := RuntimeConfig{
		Engine: "deterministic",
		Seed:   first.Seed,
		Model:  map[string]any{"name": "model-a", "temperature": 0},
		Evaluator: map[string]any{
			"appName": "regression-test",
			"name":    "evaluator-a",
		},
	}
	require.NoError(t, BindRuntime(first, runtime))
	require.NoError(t, BindRuntime(second, runtime))
	require.Equal(t, first.EvaluatorConfigHash, second.EvaluatorConfigHash)
	require.Equal(t, first.sourceConfigHash, second.sourceConfigHash)
	require.NotEqual(t, first.RunID, second.RunID)
	firstRunID := first.RunID
	firstFingerprint, err := RuntimeConfigFingerprint(first.Runtime)
	require.NoError(t, err)
	secondFingerprint, err := RuntimeConfigFingerprint(second.Runtime)
	require.NoError(t, err)
	require.Equal(t, firstFingerprint, secondFingerprint)

	runtime.Model["name"] = "model-b"
	require.Equal(t, "model-a", first.Runtime.Model["name"])
	require.NoError(t, BindRuntime(second, runtime))
	require.NotEqual(t, first.EvaluatorConfigHash, second.EvaluatorConfigHash)
	require.NotEqual(t, firstRunID, second.RunID)
	require.NotEqual(t, firstFingerprint, mustRuntimeFingerprint(t, second.Runtime))
}

func TestLoadRunConfigProvenanceCoversAllInputsAndGatePolicy(t *testing.T) {
	for _, input := range []string{
		"trainEvalSet",
		"validationEvalSet",
		"metrics",
		"baselinePrompt",
		"promptIterConfig",
		"regressionConfig",
	} {
		t.Run(input+" changes run id", func(t *testing.T) {
			fixture := newConfigFixture(t)
			before, err := LoadRunConfig(context.Background(), "regression-test", fixture.files)
			require.NoError(t, err)
			path := map[string]string{
				"trainEvalSet":      fixture.files.TrainEvalSet,
				"validationEvalSet": fixture.files.ValidationEvalSet,
				"metrics":           fixture.files.Metrics,
				"baselinePrompt":    fixture.files.BaselinePrompt,
				"promptIterConfig":  fixture.files.PromptIterConfig,
				"regressionConfig":  fixture.files.RegressionConfig,
			}[input]
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, append(data, ' '), 0o600))
			after, err := LoadRunConfig(context.Background(), "regression-test", fixture.files)
			require.NoError(t, err)
			require.NotEqual(t, before.RunID, after.RunID)
			require.NotEqual(t, before.sourceConfigHash, after.sourceConfigHash)
		})
	}

	fixture := newConfigFixture(t)
	before, err := LoadRunConfig(context.Background(), "regression-test", fixture.files)
	require.NoError(t, err)
	fixture.gate()["epsilon"] = 0.000002
	fixture.writeRegression(t)
	after, err := LoadRunConfig(context.Background(), "regression-test", fixture.files)
	require.NoError(t, err)
	require.NotEqual(t, before.MetricPolicyHash, after.MetricPolicyHash)
	require.NotEqual(t, before.EvaluatorConfigHash, after.EvaluatorConfigHash)
}

func TestCustomConfigLoadersAreStrict(t *testing.T) {
	t.Run("unknown field", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "promptiter.json")
		require.NoError(t, os.WriteFile(path, []byte(`{
			"schemaVersion":"1.0",
			"seed":2003,
			"policy":{
				"maxOuterRounds":2,
				"searchMinScoreGain":0.1,
				"internalValidationStrategy":"train_all",
				"targetSurfaceIds":["agent#instruction"]
			},
			"unexpected":true
		}`), 0o600))
		_, err := LoadPromptIterConfig(path)
		require.ErrorContains(t, err, "unknown field")
	})

	t.Run("trailing value", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "regression.json")
		require.NoError(t, os.WriteFile(path, []byte(`{
			"schemaVersion":"1.0",
			"reportId":"report",
			"generatedAt":"2026-01-01T00:00:00Z",
			"gate":{
				"primaryMetric":"quality",
				"metricDirections":{"quality":"higher_is_better"},
				"epsilon":0.000001,
				"minValidationGain":0,
				"noNewHardFailures":true,
				"noCriticalRegressions":true
			},
			"evidenceLimit":10,
			"output":{"json":"report.json","markdown":"report.md"}
		} true`), 0o600))
		_, err := LoadRegressionConfig(path)
		require.ErrorContains(t, err, "unexpected trailing JSON value")
	})

	t.Run("schema version", func(t *testing.T) {
		fixture := newConfigFixture(t)
		fixture.promptIter["schemaVersion"] = "2.0"
		fixture.writePromptIter(t)
		_, err := LoadPromptIterConfig(fixture.files.PromptIterConfig)
		require.ErrorContains(t, err, "unsupported schema version")
	})

	t.Run("negative minimum validation gain", func(t *testing.T) {
		fixture := newConfigFixture(t)
		fixture.gate()["minValidationGain"] = -0.01
		fixture.writeRegression(t)
		_, err := LoadRegressionConfig(fixture.files.RegressionConfig)
		require.ErrorContains(t, err, "minValidationGain")
	})

	t.Run("multiple target surfaces", func(t *testing.T) {
		fixture := newConfigFixture(t)
		fixture.policy()["targetSurfaceIds"] = []string{"agent#instruction", "router#instruction"}
		fixture.writePromptIter(t)
		_, err := LoadPromptIterConfig(fixture.files.PromptIterConfig)
		require.ErrorContains(t, err, "exactly one")
	})

	for _, field := range []string{
		"primaryMetric",
		"metricDirections",
		"epsilon",
		"minValidationGain",
		"noNewHardFailures",
		"noCriticalRegressions",
		"maxCumulativeModelCalls",
	} {
		t.Run("missing release gate field "+field, func(t *testing.T) {
			fixture := newConfigFixture(t)
			delete(fixture.gate(), field)
			fixture.writeRegression(t)
			_, err := LoadRegressionConfig(fixture.files.RegressionConfig)
			require.ErrorContains(t, err, `missing required field "`+field+`"`)
		})
	}

	t.Run("missing promptiter policy field", func(t *testing.T) {
		fixture := newConfigFixture(t)
		delete(fixture.policy(), "searchMinScoreGain")
		fixture.writePromptIter(t)
		_, err := LoadPromptIterConfig(fixture.files.PromptIterConfig)
		require.ErrorContains(t, err, `missing required field "searchMinScoreGain"`)
	})

	t.Run("missing output field", func(t *testing.T) {
		fixture := newConfigFixture(t)
		delete(fixture.regression["output"].(map[string]any), "markdown")
		fixture.writeRegression(t)
		_, err := LoadRegressionConfig(fixture.files.RegressionConfig)
		require.ErrorContains(t, err, `missing required field "markdown"`)
	})
}

func TestLoadRunConfigRejectsInvalidInventoriesAndLeakage(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*configFixture)
		wantError string
	}{
		{
			name: "duplicate train case id",
			mutate: func(f *configFixture) {
				f.trainCases[1]["evalId"] = f.trainCases[0]["evalId"]
				f.writeEvalSets(t)
			},
			wantError: "duplicate eval case id",
		},
		{
			name: "same case id across heldout boundary",
			mutate: func(f *configFixture) {
				f.validationCases[0]["evalId"] = f.trainCases[0]["evalId"]
				f.writeEvalSets(t)
			},
			wantError: "held-out leakage: case id",
		},
		{
			name: "same normalized input across heldout boundary",
			mutate: func(f *configFixture) {
				f.validationCases[0]["conversation"] = f.trainCases[0]["conversation"]
				f.writeEvalSets(t)
			},
			wantError: "same normalized input",
		},
		{
			name: "unknown internal validation case",
			mutate: func(f *configFixture) {
				f.policy()["internalValidationStrategy"] = "train_case_ids"
				f.policy()["internalValidationCaseIds"] = []string{"missing-train-case"}
				f.writePromptIter(t)
			},
			wantError: "not in the train inventory",
		},
		{
			name: "unknown critical case",
			mutate: func(f *configFixture) {
				f.regression["criticalCaseIds"] = []string{"missing-validation-case"}
				f.writeRegression(t)
			},
			wantError: "not in validation inventory",
		},
		{
			name: "duplicate native metric",
			mutate: func(f *configFixture) {
				f.metrics = append(f.metrics, cloneJSONMap(f.metrics[0]))
				f.writeMetrics(t)
			},
			wantError: "duplicate native metric name",
		},
		{
			name: "unknown gate metric",
			mutate: func(f *configFixture) {
				f.gate()["primaryMetric"] = "missing"
				f.gate()["metricDirections"] = map[string]any{
					"quality": "higher_is_better",
					"safety":  "higher_is_better",
					"missing": "higher_is_better",
				}
				f.writeRegression(t)
			},
			wantError: "primary metric",
		},
		{
			name: "output collides with input",
			mutate: func(f *configFixture) {
				f.regression["output"].(map[string]any)["json"] = filepath.Base(f.files.TrainEvalSet)
				f.writeRegression(t)
			},
			wantError: "collides with trainEvalSet input",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newConfigFixture(t)
			test.mutate(fixture)
			_, err := LoadRunConfig(context.Background(), "regression-test", fixture.files)
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

type configFixture struct {
	files           InputFiles
	trainCases      []map[string]any
	validationCases []map[string]any
	metrics         []map[string]any
	promptIter      map[string]any
	regression      map[string]any
}

func newConfigFixture(t *testing.T) *configFixture {
	t.Helper()
	dir := t.TempDir()
	fixture := &configFixture{
		files: InputFiles{
			TrainEvalSet:      filepath.Join(dir, "train.evalset.json"),
			ValidationEvalSet: filepath.Join(dir, "validation.evalset.json"),
			Metrics:           filepath.Join(dir, "metrics.json"),
			BaselinePrompt:    filepath.Join(dir, "baseline_prompt.txt"),
			PromptIterConfig:  filepath.Join(dir, "promptiter.json"),
			RegressionConfig:  filepath.Join(dir, "regression.json"),
		},
		trainCases: []map[string]any{
			nativeCase("train-response", "Answer the support question exactly."),
			nativeCase("train-tool", "Find the current order status."),
			nativeCase("train-format", "Return the account result as JSON."),
		},
		validationCases: []map[string]any{
			nativeCase("valid-args", "Find order A-17 in Singapore."),
			nativeCase("valid-route", "Route this refund request."),
			nativeCase("valid-fact", "State the Acme return window."),
		},
		metrics: []map[string]any{
			{
				"metricName": "quality",
				"threshold":  0.7,
				"criterion":  map[string]any{},
			},
			{
				"metricName": "safety",
				"threshold":  0.9,
				"criterion":  map[string]any{},
			},
		},
		promptIter: map[string]any{
			"schemaVersion": "1.0",
			"seed":          2003,
			"policy": map[string]any{
				"maxOuterRounds":             2,
				"searchMinScoreGain":         0.05,
				"internalValidationStrategy": "train_all",
				"targetSurfaceIds":           []string{"agent#instruction"},
			},
		},
		regression: map[string]any{
			"schemaVersion": "1.0",
			"reportId":      "issue-2003-report",
			"generatedAt":   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			"gate": map[string]any{
				"primaryMetric": "quality",
				"metricDirections": map[string]any{
					"quality": "higher_is_better",
					"safety":  "higher_is_better",
				},
				"epsilon":                 0.000001,
				"minValidationGain":       0.05,
				"noNewHardFailures":       true,
				"noCriticalRegressions":   true,
				"maxCumulativeModelCalls": 100,
			},
			"criticalCaseIds":    []string{"valid-route"},
			"hardFailureCaseIds": []string{"valid-args"},
			"evidenceLimit":      20,
			"output": map[string]any{
				"json":     "optimization_report.json",
				"markdown": "optimization_report.md",
			},
		},
	}
	require.NoError(t, os.WriteFile(fixture.files.BaselinePrompt, []byte("baseline instruction\n"), 0o600))
	fixture.writeEvalSets(t)
	fixture.writeMetrics(t)
	fixture.writePromptIter(t)
	fixture.writeRegression(t)
	return fixture
}

func nativeCase(id, input string) map[string]any {
	return map[string]any{
		"evalId": id,
		"conversation": []any{
			map[string]any{
				"invocationId": id + "-invocation",
				"userContent": map[string]any{
					"role":    "user",
					"content": input,
				},
				"finalResponse": map[string]any{
					"role":    "assistant",
					"content": "expected",
				},
			},
		},
	}
}

func (f *configFixture) policy() map[string]any {
	return f.promptIter["policy"].(map[string]any)
}

func (f *configFixture) gate() map[string]any {
	return f.regression["gate"].(map[string]any)
}

func (f *configFixture) writeEvalSets(t *testing.T) {
	t.Helper()
	writeJSON(t, f.files.TrainEvalSet, map[string]any{
		"evalSetId": "issue-2003-train",
		"evalCases": f.trainCases,
	})
	writeJSON(t, f.files.ValidationEvalSet, map[string]any{
		"evalSetId": "issue-2003-validation",
		"evalCases": f.validationCases,
	})
}

func (f *configFixture) writeMetrics(t *testing.T) {
	t.Helper()
	writeJSON(t, f.files.Metrics, f.metrics)
}

func (f *configFixture) writePromptIter(t *testing.T) {
	t.Helper()
	writeJSON(t, f.files.PromptIterConfig, f.promptIter)
}

func (f *configFixture) writeRegression(t *testing.T) {
	t.Helper()
	writeJSON(t, f.files.RegressionConfig, f.regression)
}

func (f *configFixture) rawHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return hashBytes(data)
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func cloneJSONMap(source map[string]any) map[string]any {
	data, _ := json.Marshal(source)
	var clone map[string]any
	_ = json.Unmarshal(data, &clone)
	return clone
}

func mustRuntimeFingerprint(t *testing.T, runtime RuntimeConfig) string {
	t.Helper()
	fingerprint, err := RuntimeConfigFingerprint(runtime)
	require.NoError(t, err)
	return fingerprint
}
