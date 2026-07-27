//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	path := writeTestFile(t, `{
		"mode":"deterministic",
		"seed":1,
		"maxRounds":1,
		"trainEvalSetId":"train",
		"searchEvalSetId":"train",
		"validationEvalSetId":"validation",
		"metricFileId":"metrics",
		"minValidationGain":0,
		"maxHardFailures":0,
		"maxCaseScoreDrop":0,
		"maxModelCalls":0,
		"maxModelCallz":1,
		"evalCaseParallelism":1,
		"parallelInference":false,
		"parallelEvaluation":false,
		"candidate":{},
		"judge":{},
		"worker":{}
	}`)

	_, err := loadConfig(path)
	require.ErrorContains(t, err, "unknown field")
}

func TestLoadConfigPreservesExplicitZeroBudget(t *testing.T) {
	path := writeTestFile(t, validConfigJSON(`"maxModelCalls":0,`))

	cfg, err := loadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.MaxModelCalls)
	assert.Zero(t, *cfg.MaxModelCalls)
}

func TestLoadConfigRejectsTrailingJSONValue(t *testing.T) {
	path := writeTestFile(t, validConfigJSON("")+` {}`)

	_, err := loadConfig(path)
	require.ErrorContains(t, err, "trailing JSON value")
}

func TestValidateConfigRejectsHeldOutSearchInput(t *testing.T) {
	cfg := validConfig()
	cfg.SearchEvalSetID = cfg.ValidationEvalSetID

	require.ErrorContains(t, cfg.validate(), "held-out validation")
}

func TestValidateConfigBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config)
		message string
	}{
		{
			name: "unsupported mode",
			mutate: func(cfg *config) {
				cfg.Mode = "replay"
			},
			message: "mode",
		},
		{
			name: "nonpositive rounds",
			mutate: func(cfg *config) {
				cfg.MaxRounds = 0
			},
			message: "max rounds",
		},
		{
			name: "search differs from train",
			mutate: func(cfg *config) {
				cfg.SearchEvalSetID = "search"
			},
			message: "training evaluation set",
		},
		{
			name: "negative budget",
			mutate: func(cfg *config) {
				value := -1
				cfg.MaxToolCalls = &value
			},
			message: "max tool calls",
		},
		{
			name: "critical rule without condition",
			mutate: func(cfg *config) {
				cfg.Critical = []criticalRule{{EvalCaseID: "case-1"}}
			},
			message: "condition",
		},
		{
			name: "live role without model",
			mutate: func(cfg *config) {
				cfg.Mode = modeLive
				cfg.Candidate = validLiveRole()
				cfg.Candidate.Model = ""
				cfg.Judge = validLiveRole()
				cfg.Worker = validLiveRole()
			},
			message: "candidate model",
		},
		{
			name: "custom credentials without base URL",
			mutate: func(cfg *config) {
				cfg.Mode = modeLive
				cfg.Candidate = validLiveRole()
				cfg.Candidate.APIKeyEnv = "CANDIDATE_API_KEY"
				cfg.Judge = validLiveRole()
				cfg.Worker = validLiveRole()
			},
			message: "candidate base URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)

			require.ErrorContains(t, cfg.validate(), tt.message)
		})
	}
}

func TestValidateConfigAllowsDeterministicRolesWithoutCredentials(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.validate())
}

func validConfig() config {
	return config{
		Mode:                modeDeterministic,
		Seed:                1,
		MaxRounds:           1,
		TrainEvalSetID:      "train",
		SearchEvalSetID:     "train",
		ValidationEvalSetID: "validation",
		MetricFileID:        "metrics",
		MinValidationGain:   0,
		MaxHardFailures:     0,
		MaxCaseScoreDrop:    0,
		EvalCaseParallelism: 1,
	}
}

func validLiveRole() roleConfig {
	return roleConfig{
		Model:     "gpt-test",
		APIKeyEnv: "OPENAI_API_KEY",
	}
}

func validConfigJSON(extra string) string {
	return `{
		"mode":"deterministic",
		"seed":1,
		"maxRounds":1,
		"trainEvalSetId":"train",
		"searchEvalSetId":"train",
		"validationEvalSetId":"validation",
		"metricFileId":"metrics",
		"minValidationGain":0,
		"maxHardFailures":0,
		"maxCaseScoreDrop":0,
		` + extra + `
		"evalCaseParallelism":1,
		"parallelInference":false,
		"parallelEvaluation":false,
		"candidate":{},
		"judge":{},
		"worker":{}
	}`
}

func writeTestFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}
