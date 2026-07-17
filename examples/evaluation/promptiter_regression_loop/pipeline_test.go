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
	"os"
	"path/filepath"
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
	cfg.Gate.MaxCalls = 161
	configData, err = json.Marshal(cfg)
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(configPath, configData, 0o600))

	err = runPipeline(context.Background(), configPath, modeFake)
	assert.ErrorContains(t, err, "cannot cover 162 required live calls")
	assert.NoDirExists(t, cfg.OutputDir)
}

func loadReportForTest(t *testing.T, path string) optimizationReport {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var report optimizationReport
	require.NoError(t, json.Unmarshal(data, &report))
	return report
}
