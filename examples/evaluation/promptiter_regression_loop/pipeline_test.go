//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunPipelineFakeIsDeterministic(t *testing.T) {
	dataDir, err := filepath.Abs("data")
	require.NoError(t, err)
	configData, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	require.NoError(t, err)
	var cfg pipelineConfig
	require.NoError(t, json.Unmarshal(configData, &cfg))
	cfg.PromptFile = filepath.Join(dataDir, "prompts", "baseline_prompt.md")
	cfg.TrainEvalSet = filepath.Join(dataDir, "train.evalset.json")
	cfg.ValidationEvalSet = filepath.Join(dataDir, "validation.evalset.json")
	cfg.MetricsFile = filepath.Join(dataDir, "metrics.json")
	cfg.PromptIterFile = filepath.Join(dataDir, "promptiter.json")
	cfg.OutputDir = filepath.Join(t.TempDir(), "output")
	configData, err = json.Marshal(cfg)
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(configPath, configData, 0o600))

	started := time.Now()
	require.NoError(t, runPipeline(context.Background(), configPath, modeFake))
	assert.Less(t, time.Since(started), 3*time.Minute)
	first := loadReportForTest(t, filepath.Join(cfg.OutputDir, "optimization_report.json"))
	require.True(t, first.Gate.Accepted)
	assert.Len(t, first.Train.Baseline, 6)
	assert.Len(t, first.Train.Candidate, 6)
	assert.Len(t, first.Validation.Baseline, 7)
	assert.Len(t, first.Validation.Candidate, 7)
	assert.Equal(t, 54, evaluationRunCount(first))
	assert.GreaterOrEqual(t, cfg.Gate.MaxCalls, 54*(cfg.Live.MaxRetries+1))
	assert.Equal(t, 3, first.Comparison.PassK)
	assert.NotEmpty(t, first.DeterministicFingerprint)
	assert.NotEmpty(t, first.Validation.Baseline[0].Runs[0].Output)
	assert.NotEmpty(t, first.Validation.Baseline[0].Runs[0].Trace)
	var unchangedFound bool
	for _, delta := range first.Comparison.Deltas {
		if delta.ID == "validation_unchanged_greeting" {
			unchangedFound = true
			assert.Zero(t, delta.ScoreDelta)
		}
	}
	assert.True(t, unchangedFound, "expected a public optimization-no-effect case")

	require.NoError(t, runPipeline(context.Background(), configPath, modeFake))
	second := loadReportForTest(t, filepath.Join(cfg.OutputDir, "optimization_report.json"))
	assert.Equal(t, first.DeterministicFingerprint, second.DeterministicFingerprint)
}

func evaluationRunCount(report optimizationReport) int {
	groups := [][]CaseEvaluation{
		report.Train.Baseline,
		report.Train.Candidate,
		report.Validation.Baseline,
		report.Validation.Candidate,
	}
	total := 0
	for _, group := range groups {
		for _, evalCase := range group {
			total += len(evalCase.Runs)
		}
	}
	return total
}

func TestRunPipelineRejectsValidationOverfit(t *testing.T) {
	dataDir, err := filepath.Abs("data")
	require.NoError(t, err)
	configData, err := os.ReadFile(filepath.Join(dataDir, "config_overfit.json"))
	require.NoError(t, err)
	var cfg pipelineConfig
	require.NoError(t, json.Unmarshal(configData, &cfg))
	cfg.PromptFile = filepath.Join(dataDir, "prompts", "baseline_prompt.md")
	cfg.TrainEvalSet = filepath.Join(dataDir, "train.evalset.json")
	cfg.ValidationEvalSet = filepath.Join(dataDir, "validation_overfit.evalset.json")
	cfg.MetricsFile = filepath.Join(dataDir, "metrics.json")
	cfg.PromptIterFile = filepath.Join(dataDir, "promptiter.json")
	cfg.OutputDir = filepath.Join(t.TempDir(), "output")
	configData, err = json.Marshal(cfg)
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(configPath, configData, 0o600))

	require.NoError(t, runPipeline(context.Background(), configPath, modeFake))
	report := loadReportForTest(t, filepath.Join(cfg.OutputDir, "optimization_report.json"))
	assert.False(t, report.Gate.Accepted)
	assert.Negative(t, report.Comparison.MeanScoreGain)
	assert.Contains(t, report.Gate.FailedChecks, "minimum_score_gain")
	assert.False(t, report.Train.Baseline[0].Runs[0].Passed)
	assert.True(t, report.Train.Candidate[0].Runs[0].Passed)
}

func TestRunPipelineRejectsUnknownMode(t *testing.T) {
	err := runPipeline(context.Background(), "unused.json", "mystery")
	assert.ErrorContains(t, err, "unsupported mode")
}

func TestRunPipelineRejectsBlankPromptBeforeLiveRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"must not be called"}}`))
	}))
	defer server.Close()

	dataDir, err := filepath.Abs("data")
	require.NoError(t, err)
	configData, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	require.NoError(t, err)
	var cfg pipelineConfig
	require.NoError(t, json.Unmarshal(configData, &cfg))
	blankPrompt := filepath.Join(t.TempDir(), "blank_prompt.md")
	require.NoError(t, os.WriteFile(blankPrompt, []byte(" \r\n\t"), 0o600))
	cfg.PromptFile = blankPrompt
	cfg.TrainEvalSet = filepath.Join(dataDir, "train.evalset.json")
	cfg.ValidationEvalSet = filepath.Join(dataDir, "validation.evalset.json")
	cfg.MetricsFile = filepath.Join(dataDir, "metrics.json")
	cfg.PromptIterFile = filepath.Join(dataDir, "promptiter.json")
	cfg.OutputDir = filepath.Join(t.TempDir(), "must-not-be-created")
	cfg.Live.BaseURL = server.URL
	cfg.Live.APIKeyEnv = "BLANK_PROMPT_EVALUATION_API_KEY"
	cfg.Live.Optimizer.BaseURL = server.URL
	cfg.Live.Optimizer.APIKeyEnv = "BLANK_PROMPT_OPTIMIZER_API_KEY"
	configData, err = json.Marshal(cfg)
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(configPath, configData, 0o600))
	t.Setenv(cfg.Live.APIKeyEnv, "evaluation-test-key")
	t.Setenv(cfg.Live.Optimizer.APIKeyEnv, "optimizer-test-key")

	err = runPipeline(context.Background(), configPath, modeLive)

	assert.ErrorContains(t, err, "baseline prompt is empty")
	assert.Zero(t, calls.Load())
	_, statErr := os.Stat(cfg.OutputDir)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestRunPipelineRejectsInvalidLoadedInputsBeforeLiveRequests(t *testing.T) {
	tests := []struct {
		name      string
		wantError string
		mutate    func(*testing.T, *pipelineConfig, string)
	}{
		{
			name:      "unsupported PromptIter target",
			wantError: "PromptIter target",
			mutate: func(t *testing.T, cfg *pipelineConfig, dataDir string) {
				var promptIter promptIterConfig
				data, err := os.ReadFile(filepath.Join(dataDir, "promptiter.json"))
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(data, &promptIter))
				promptIter.Target = "regression-writer#instructions"
				data, err = json.Marshal(promptIter)
				require.NoError(t, err)
				cfg.PromptIterFile = filepath.Join(t.TempDir(), "promptiter.json")
				require.NoError(t, os.WriteFile(cfg.PromptIterFile, data, 0o600))
			},
		},
		{
			name:      "whitespace-equivalent validation IDs",
			wantError: "duplicate eval case ID",
			mutate: func(t *testing.T, cfg *pipelineConfig, dataDir string) {
				var validation evalSetFile
				data, err := os.ReadFile(filepath.Join(dataDir, "validation.evalset.json"))
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(data, &validation))
				duplicate := validation.EvalCases[0]
				duplicate.EvalID = " " + duplicate.EvalID + " "
				validation.EvalCases = append(validation.EvalCases, duplicate)
				data, err = json.Marshal(validation)
				require.NoError(t, err)
				cfg.ValidationEvalSet = filepath.Join(t.TempDir(), "validation.evalset.json")
				require.NoError(t, os.WriteFile(cfg.ValidationEvalSet, data, 0o600))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(
				w http.ResponseWriter,
				_ *http.Request,
			) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"error":{"message":"must not be called"}}`))
			}))
			defer server.Close()

			dataDir, err := filepath.Abs("data")
			require.NoError(t, err)
			configData, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
			require.NoError(t, err)
			var cfg pipelineConfig
			require.NoError(t, json.Unmarshal(configData, &cfg))
			cfg.PromptFile = filepath.Join(dataDir, "prompts", "baseline_prompt.md")
			cfg.TrainEvalSet = filepath.Join(dataDir, "train.evalset.json")
			cfg.ValidationEvalSet = filepath.Join(dataDir, "validation.evalset.json")
			cfg.MetricsFile = filepath.Join(dataDir, "metrics.json")
			cfg.PromptIterFile = filepath.Join(dataDir, "promptiter.json")
			cfg.OutputDir = filepath.Join(t.TempDir(), "must-not-be-created")
			cfg.Live.BaseURL = server.URL
			cfg.Live.APIKeyEnv = "INVALID_INPUT_EVALUATION_API_KEY"
			cfg.Live.Optimizer.BaseURL = server.URL
			cfg.Live.Optimizer.APIKeyEnv = "INVALID_INPUT_OPTIMIZER_API_KEY"
			test.mutate(t, &cfg, dataDir)
			configData, err = json.Marshal(cfg)
			require.NoError(t, err)
			configPath := filepath.Join(t.TempDir(), "config.json")
			require.NoError(t, os.WriteFile(configPath, configData, 0o600))
			t.Setenv(cfg.Live.APIKeyEnv, "evaluation-test-key")
			t.Setenv(cfg.Live.Optimizer.APIKeyEnv, "optimizer-test-key")

			err = runPipeline(context.Background(), configPath, modeLive)

			assert.ErrorContains(t, err, test.wantError)
			assert.Zero(t, calls.Load())
			_, statErr := os.Stat(cfg.OutputDir)
			assert.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}

func TestRunPipelineLiveOptimizerFailsClosedWithAuditReport(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"test-response",
			"object":"chat.completion",
			"created":1,
			"model":"test-model",
			"choices":[{
				"index":0,
				"message":{"role":"assistant","content":"not valid optimizer json"},
				"finish_reason":"stop"
			}],
			"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}
		}`))
	}))
	defer server.Close()

	dataDir, err := filepath.Abs("data")
	require.NoError(t, err)
	configData, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	require.NoError(t, err)
	var cfg pipelineConfig
	require.NoError(t, json.Unmarshal(configData, &cfg))
	cfg.PromptFile = filepath.Join(dataDir, "prompts", "baseline_prompt.md")
	cfg.TrainEvalSet = filepath.Join(dataDir, "train.evalset.json")
	cfg.ValidationEvalSet = filepath.Join(dataDir, "validation.evalset.json")
	cfg.MetricsFile = filepath.Join(dataDir, "metrics.json")
	cfg.PromptIterFile = filepath.Join(dataDir, "promptiter.json")
	cfg.OutputDir = filepath.Join(t.TempDir(), "output")
	cfg.Live.Model = "test-model"
	cfg.Live.BaseURL = server.URL
	cfg.Live.APIKeyEnv = "PROMPTITER_TEST_API_KEY"
	cfg.Live.MaxRetries = 0
	cfg.Live.Optimizer.Model = ""
	cfg.Live.Optimizer.BaseURL = ""
	cfg.Live.Optimizer.MaxRetries = 0
	configData, err = json.Marshal(cfg)
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(configPath, configData, 0o600))
	t.Setenv(cfg.Live.APIKeyEnv, "test-key")

	err = runPipeline(context.Background(), configPath, modeLive)
	require.Error(t, err)
	assert.ErrorContains(t, err, "run PromptIter")
	report := loadReportForTest(
		t,
		filepath.Join(cfg.OutputDir, "optimization_report.json"),
	)
	assert.Equal(t, pipelineStatusFailed, report.Status)
	assert.Equal(t, candidateSourceLiveLLM, report.CandidateSource)
	assert.False(t, report.PromptIter.Completed)
	assert.Equal(t, strings.TrimSpace(string(mustReadFile(t, cfg.PromptFile))), report.SelectedPrompt)
	assert.Contains(t, report.Gate.FailedChecks, "optimizer_completed")
	assert.Equal(t, 1, report.Resources.Optimizer.Usage.Calls)
	assert.Greater(t, calls.Load(), int32(1))
}

func TestRunPipelineRedactsOptimizerCredentialFromErrorReports(t *testing.T) {
	evaluationServer := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"evaluation-response",
			"object":"chat.completion",
			"created":1,
			"model":"evaluation-test-model",
			"choices":[{
				"index":0,
				"message":{"role":"assistant","content":"safe response"},
				"finish_reason":"stop"
			}],
			"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}
		}`))
	}))
	defer evaluationServer.Close()

	const optimizerSecret = "optimizer-secret-value-12345678"
	var optimizerCalls atomic.Int32
	optimizerServer := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		optimizerCalls.Add(1)
		assert.Equal(t, "Bearer "+optimizerSecret, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(
			w,
			`{"error":{"message":"gateway echoed %s","type":"server_error"}}`,
			optimizerSecret,
		)
	}))
	defer optimizerServer.Close()

	dataDir, err := filepath.Abs("data")
	require.NoError(t, err)
	configData, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	require.NoError(t, err)
	var cfg pipelineConfig
	require.NoError(t, json.Unmarshal(configData, &cfg))
	cfg.PromptFile = filepath.Join(dataDir, "prompts", "baseline_prompt.md")
	cfg.TrainEvalSet = filepath.Join(dataDir, "train.evalset.json")
	cfg.ValidationEvalSet = filepath.Join(dataDir, "validation.evalset.json")
	cfg.MetricsFile = filepath.Join(dataDir, "metrics.json")
	cfg.PromptIterFile = filepath.Join(dataDir, "promptiter.json")
	cfg.OutputDir = filepath.Join(t.TempDir(), "output")
	cfg.Live.Model = "evaluation-test-model"
	cfg.Live.BaseURL = evaluationServer.URL
	cfg.Live.APIKeyEnv = "PROMPTITER_EVALUATION_REDACTION_TEST_API_KEY"
	cfg.Live.MaxRetries = 0
	cfg.Live.Optimizer.Model = "optimizer-test-model"
	cfg.Live.Optimizer.BaseURL = optimizerServer.URL
	cfg.Live.Optimizer.APIKeyEnv = "PROMPTITER_OPTIMIZER_REDACTION_TEST_API_KEY"
	cfg.Live.Optimizer.InputCNYPerMillion = 1
	cfg.Live.Optimizer.OutputCNYPerMillion = 2
	cfg.Live.Optimizer.MaxRetries = 0
	configData, err = json.Marshal(cfg)
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(configPath, configData, 0o600))
	t.Setenv(cfg.Live.APIKeyEnv, "evaluation-secret-value-12345678")
	t.Setenv(cfg.Live.Optimizer.APIKeyEnv, optimizerSecret)

	runErr := runPipeline(context.Background(), configPath, modeLive)

	require.Error(t, runErr)
	assert.NotContains(t, runErr.Error(), optimizerSecret)
	assert.Equal(t, int32(1), optimizerCalls.Load())
	jsonReport := mustReadFile(
		t,
		filepath.Join(cfg.OutputDir, "optimization_report.json"),
	)
	markdownReport := mustReadFile(
		t,
		filepath.Join(cfg.OutputDir, "optimization_report.md"),
	)
	assert.NotContains(t, string(jsonReport), optimizerSecret)
	assert.NotContains(t, string(markdownReport), optimizerSecret)
	assert.Contains(t, string(jsonReport), sensitiveRedaction)
	assert.Contains(t, string(markdownReport), sensitiveRedaction)
}

func TestFinalizeReportRedactsLoadedSecretsFromEveryAuditField(t *testing.T) {
	const evaluationSecret = "evaluation-secret-value-12345678"
	const optimizerSecret = "optimizer-secret-value-87654321"
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		assert.Equal(t, "Bearer "+evaluationSecret, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(
			w,
			`{"error":{"message":"gateway echoed %s","type":"server_error"}}`,
			evaluationSecret,
		)
	}))
	defer server.Close()

	cfg, err := loadConfig("data/config.json")
	require.NoError(t, err)
	cfg.OutputDir = t.TempDir()
	generator, err := newLiveGenerator(liveConfig{
		Model: "evaluation-test-model", BaseURL: server.URL,
		APIKeyEnv: "EVALUATION_TEST_API_KEY", TimeoutSeconds: 2,
		MaxRetries: 0, InputCNYPerMillion: 1, OutputCNYPerMillion: 2,
	}, gateFileConfig{MaxCalls: 2, MaxTokens: 10_000, MaxCostCNY: 1}, evaluationSecret)
	require.NoError(t, err)

	run, runErr := generateCase(
		context.Background(),
		generator,
		cfg.Prompt,
		cfg.Train.EvalCases[0],
	)
	require.Error(t, runErr)
	require.Contains(t, run.Error, evaluationSecret)
	report := &optimizationReport{
		SchemaVersion:   "1.1",
		Status:          pipelineStatusFailed,
		Error:           "top-level " + optimizerSecret,
		Mode:            modeLive,
		CandidateSource: candidateSourceLiveLLM,
		PromptIter: promptIterAudit{
			Error:           "optimizer error " + optimizerSecret,
			CandidatePrompt: "candidate echoed " + optimizerSecret,
			Rounds: []promptIterRound{{
				CandidatePrompt: "round candidate " + evaluationSecret,
				PatchReason:     "reason " + optimizerSecret,
				Reason:          "acceptance " + evaluationSecret,
			}},
		},
		Train: evaluationPair{
			Baseline: []CaseEvaluation{{ID: "echoed-error", Runs: []CaseRun{run}}},
		},
		Gate: GateResult{Checks: []GateCheck{{
			Name: "secret-check", Detail: "detail " + evaluationSecret,
		}}},
		SelectedPrompt: "selected " + optimizerSecret,
	}

	require.NoError(t, finalizeAndWriteReport(
		cfg,
		report,
		evaluationSecret,
		optimizerSecret,
	))
	jsonReport := mustReadFile(
		t,
		filepath.Join(cfg.OutputDir, "optimization_report.json"),
	)
	markdownReport := mustReadFile(
		t,
		filepath.Join(cfg.OutputDir, "optimization_report.md"),
	)
	for _, contents := range [][]byte{jsonReport, markdownReport} {
		assert.NotContains(t, string(contents), evaluationSecret)
		assert.NotContains(t, string(contents), optimizerSecret)
		assert.Contains(t, string(contents), sensitiveRedaction)
	}
}

func TestRunPipelineValidatesAllLiveCredentialsBeforeRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"must not be called"}}`))
	}))
	defer server.Close()

	dataDir, err := filepath.Abs("data")
	require.NoError(t, err)
	configData, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	require.NoError(t, err)
	var cfg pipelineConfig
	require.NoError(t, json.Unmarshal(configData, &cfg))
	cfg.PromptFile = filepath.Join(dataDir, "prompts", "baseline_prompt.md")
	cfg.TrainEvalSet = filepath.Join(dataDir, "train.evalset.json")
	cfg.ValidationEvalSet = filepath.Join(dataDir, "validation.evalset.json")
	cfg.MetricsFile = filepath.Join(dataDir, "metrics.json")
	cfg.PromptIterFile = filepath.Join(dataDir, "promptiter.json")
	cfg.OutputDir = filepath.Join(t.TempDir(), "must-not-be-created")
	cfg.Live.Model = "test-model"
	cfg.Live.BaseURL = server.URL
	cfg.Live.APIKeyEnv = "PROMPTITER_EVALUATION_TEST_API_KEY"
	cfg.Live.MaxRetries = 0
	cfg.Live.Optimizer.APIKeyEnv = "PROMPTITER_MISSING_OPTIMIZER_TEST_API_KEY"
	cfg.Live.Optimizer.MaxRetries = 0
	configData, err = json.Marshal(cfg)
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(configPath, configData, 0o600))
	t.Setenv(cfg.Live.APIKeyEnv, "evaluation-test-key")
	t.Setenv(cfg.Live.Optimizer.APIKeyEnv, "")

	err = runPipeline(context.Background(), configPath, modeLive)

	assert.ErrorContains(t, err, cfg.Live.Optimizer.APIKeyEnv+" is empty")
	assert.Zero(t, calls.Load())
	assert.NoDirExists(t, cfg.OutputDir)
}

func TestRunPipelineRejectsUndersizedCallBudgetBeforeEvaluation(t *testing.T) {
	dataDir, err := filepath.Abs("data")
	require.NoError(t, err)
	configData, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	require.NoError(t, err)
	var cfg pipelineConfig
	require.NoError(t, json.Unmarshal(configData, &cfg))
	cfg.PromptFile = filepath.Join(dataDir, "prompts", "baseline_prompt.md")
	cfg.TrainEvalSet = filepath.Join(dataDir, "train.evalset.json")
	cfg.ValidationEvalSet = filepath.Join(dataDir, "validation.evalset.json")
	cfg.MetricsFile = filepath.Join(dataDir, "metrics.json")
	cfg.PromptIterFile = filepath.Join(dataDir, "promptiter.json")
	cfg.OutputDir = filepath.Join(t.TempDir(), "must-not-be-created")
	cfg.Gate.MaxCalls = 164
	configData, err = json.Marshal(cfg)
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(configPath, configData, 0o600))

	err = runPipeline(context.Background(), configPath, modeFake)
	assert.ErrorContains(t, err, "cannot cover 165 required live calls")
	assert.NoDirExists(t, cfg.OutputDir)
}

func TestReportFingerprintIgnoresNestedRunLatencyWithoutMutation(t *testing.T) {
	report := optimizationReport{
		DurationMillis: 99,
		Train: evaluationPair{
			Baseline:  []CaseEvaluation{{ID: "train", Runs: []CaseRun{{LatencyMillis: 11}}}},
			Candidate: []CaseEvaluation{{ID: "train", Runs: []CaseRun{{LatencyMillis: 12}}}},
		},
		Validation: evaluationPair{
			Baseline:  []CaseEvaluation{{ID: "validation", Runs: []CaseRun{{LatencyMillis: 13}}}},
			Candidate: []CaseEvaluation{{ID: "validation", Runs: []CaseRun{{LatencyMillis: 14}}}},
		},
	}
	other := report
	other.Train = evaluationPair{
		Baseline:  []CaseEvaluation{{ID: "train", Runs: []CaseRun{{LatencyMillis: 101}}}},
		Candidate: []CaseEvaluation{{ID: "train", Runs: []CaseRun{{LatencyMillis: 102}}}},
	}
	other.Validation = evaluationPair{
		Baseline:  []CaseEvaluation{{ID: "validation", Runs: []CaseRun{{LatencyMillis: 103}}}},
		Candidate: []CaseEvaluation{{ID: "validation", Runs: []CaseRun{{LatencyMillis: 104}}}},
	}

	first, err := reportFingerprint(&report)
	require.NoError(t, err)
	second, err := reportFingerprint(&other)
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.Equal(t, int64(11), report.Train.Baseline[0].Runs[0].LatencyMillis)
	assert.Equal(t, int64(14), report.Validation.Candidate[0].Runs[0].LatencyMillis)
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func loadReportForTest(t *testing.T, path string) optimizationReport {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var report optimizationReport
	require.NoError(t, json.Unmarshal(data, &report))
	return report
}
