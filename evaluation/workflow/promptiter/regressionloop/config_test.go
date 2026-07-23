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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	cfg := testConfig(t)
	require.NoError(t, cfg.Validate())

	cfg.TrainEvalSet.ID = "validation"
	require.ErrorContains(t, cfg.Validate(), "must be distinct")
}

func TestConfigValidateRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:    "nil config",
			mutate:  nil,
			wantErr: "config is nil",
		},
		{
			name: "missing required fields",
			mutate: func(cfg *Config) {
				cfg.AppName = ""
				cfg.PromptSource.ID = ""
			},
			wantErr: "missing required config fields",
		},
		{
			name: "negative score gain",
			mutate: func(cfg *Config) {
				cfg.Gate.MinValidationScoreGain = -0.1
			},
			wantErr: "gate.minValidationScoreGain must be non-negative",
		},
		{
			name: "negative max cost",
			mutate: func(cfg *Config) {
				cfg.Gate.MaxCost = -1
			},
			wantErr: "gate.maxCost must be non-negative",
		},
		{
			name: "negative max calls",
			mutate: func(cfg *Config) {
				cfg.Gate.MaxCalls = -1
			},
			wantErr: "gate.maxCalls must be non-negative",
		},
		{
			name: "negative max latency",
			mutate: func(cfg *Config) {
				cfg.Gate.MaxLatencyMS = -1
			},
			wantErr: "gate.maxLatencyMs must be non-negative",
		},
		{
			name: "unsupported prompt target type",
			mutate: func(cfg *Config) {
				cfg.PromptSource.TargetType = "unknown"
			},
			wantErr: "unsupported prompt target type",
		},
		{
			name: "unsupported runner mode",
			mutate: func(cfg *Config) {
				cfg.Runner.Mode = "unknown"
			},
			wantErr: "unsupported runner mode",
		},
		{
			name: "deterministic fake mode requires seed",
			mutate: func(cfg *Config) {
				cfg.Seed = 0
				cfg.Runner.Mode = RunnerModeFake
			},
			wantErr: "deterministic fake/trace mode requires a non-zero seed",
		},
		{
			name: "deterministic trace mode requires seed",
			mutate: func(cfg *Config) {
				cfg.Seed = 0
				cfg.Runner.Mode = RunnerModeTrace
			},
			wantErr: "deterministic fake/trace mode requires a non-zero seed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mutate == nil {
				var cfg *Config
				require.ErrorContains(t, cfg.Validate(), tt.wantErr)
				return
			}
			cfg := testConfig(t)
			tt.mutate(&cfg)
			require.ErrorContains(t, cfg.Validate(), tt.wantErr)
		})
	}
}

func TestConfigValidateReportsRequiredFields(t *testing.T) {
	var cfg Config
	err := cfg.Validate()
	require.Error(t, err)
	for _, field := range []string{
		"appName",
		"promptSource.id",
		"promptSource.path",
		"promptSource.targetType",
		"trainEvalSet.id",
		"trainEvalSet.path",
		"validationEvalSet.id",
		"validationEvalSet.path",
		"metrics.path",
		"promptiter.maxRounds",
		"promptiter.targetSurfaceIds",
		"runner.mode",
		"output.dir",
		"output.jsonReport",
		"output.markdownReport",
	} {
		assert.ErrorContains(t, err, field)
	}
}

func TestLoadConfigResolvesRelativePaths(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(t)
	cfg.PromptSource.Path = "baseline_prompt.txt"
	cfg.TrainEvalSet.Path = "train.evalset.json"
	cfg.ValidationEvalSet.Path = "validation.evalset.json"
	cfg.Metrics.Path = "metrics.json"
	cfg.Output.Dir = "output"
	cfg.Output.JSONReport = "optimization_report.json"
	cfg.Output.MarkdownReport = "optimization_report.md"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "baseline_prompt.txt"), []byte("prompt"), 0o644))
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	configPath := filepath.Join(dir, "promptiter.json")
	require.NoError(t, os.WriteFile(configPath, data, 0o644))

	loaded, err := LoadConfig(configPath)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "baseline_prompt.txt"), loaded.PromptSource.Path)
	assert.Equal(t, filepath.Join(dir, "output", "optimization_report.json"), loaded.Output.JSONReport)
}

func TestLoadConfigRejectsBadInput(t *testing.T) {
	_, err := LoadConfig("")
	require.ErrorContains(t, err, "config path is empty")

	_, err = LoadConfig(filepath.Join(t.TempDir(), "missing.json"))
	require.ErrorContains(t, err, "read config")

	dir := t.TempDir()
	badJSON := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(badJSON, []byte("{"), 0o644))
	_, err = LoadConfig(badJSON)
	require.ErrorContains(t, err, "decode config")

	invalid := testConfig(t)
	invalid.AppName = ""
	data, err := json.Marshal(invalid)
	require.NoError(t, err)
	invalidPath := filepath.Join(dir, "invalid.json")
	require.NoError(t, os.WriteFile(invalidPath, data, 0o644))
	_, err = LoadConfig(invalidPath)
	require.ErrorContains(t, err, "missing required config fields")
}
