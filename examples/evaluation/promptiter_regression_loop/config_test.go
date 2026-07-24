//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigConsumesPromptIterAndMetricsFiles(t *testing.T) {
	cfg, err := loadConfig("data/config.json")
	require.NoError(t, err)
	assert.Equal(t, "regression-writer#instruction", cfg.PromptIter.Target)
	assert.Equal(t, cfg.Gate.PassK, cfg.PromptIter.CandidateValidationRuns)
	assert.Len(t, cfg.Metrics.Metrics, 4)
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "root",
			data: `{"unknown":true}`,
		},
		{
			name: "gate budget typo",
			data: `{"gate":{"maxCall":165}}`,
		},
		{
			name: "live typo",
			data: `{"live":{"timeoutSecond":45}}`,
		},
		{
			name: "optimizer typo",
			data: `{"live":{"optimizer":{"maxOutputToken":1024}}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			require.NoError(t, os.WriteFile(path, []byte(test.data), 0o600))

			_, err := loadConfig(path)

			assert.ErrorContains(t, err, "unknown field")
		})
	}
}

func TestValidateDatasetIsolationRejectsLeakage(t *testing.T) {
	shared := caseSpec{
		EvalID: "train-case",
		Conversation: []invocationSpec{{
			UserContent:   messageSpec{Content: "same input"},
			FinalResponse: messageSpec{Content: "same output"},
		}},
	}
	t.Run("same id", func(t *testing.T) {
		err := validateDatasetIsolation(
			evalSetFile{EvalCases: []caseSpec{shared}},
			evalSetFile{EvalCases: []caseSpec{shared}},
		)
		assert.ErrorContains(t, err, "share case ID")
	})
	t.Run("same content under different id", func(t *testing.T) {
		validation := shared
		validation.EvalID = "validation-case"
		err := validateDatasetIsolation(
			evalSetFile{EvalCases: []caseSpec{shared}},
			evalSetFile{EvalCases: []caseSpec{validation}},
		)
		assert.ErrorContains(t, err, "duplicates train case")
	})
	t.Run("same input with different expected response", func(t *testing.T) {
		validation := shared
		validation.EvalID = "validation-case"
		validation.Conversation = append([]invocationSpec(nil), shared.Conversation...)
		validation.Conversation[0].FinalResponse.Content = "different output"
		err := validateDatasetIsolation(
			evalSetFile{EvalCases: []caseSpec{shared}},
			evalSetFile{EvalCases: []caseSpec{validation}},
		)
		assert.ErrorContains(t, err, "duplicates train case")
	})
}

func TestSetDefaultsPreservesExplicitZeroRetries(t *testing.T) {
	var explicit pipelineConfig
	require.NoError(t, json.Unmarshal([]byte(`{"live":{"maxRetries":0}}`), &explicit))
	setDefaults(&explicit)
	assert.Zero(t, explicit.Live.MaxRetries)

	var omitted pipelineConfig
	require.NoError(t, json.Unmarshal([]byte(`{"live":{}}`), &omitted))
	setDefaults(&omitted)
	assert.Equal(t, 2, omitted.Live.MaxRetries)
}

func TestSetDefaultsInheritsLiveOptimizerModelSettings(t *testing.T) {
	var cfg pipelineConfig
	require.NoError(t, json.Unmarshal([]byte(`{
		"live":{
			"model":"evaluation-model",
			"baseURL":"https://models.example.test",
			"timeoutSeconds":30,
			"maxRetries":1,
			"optimizer":{"temperature":0.25}
		}
	}`), &cfg))

	setDefaults(&cfg)

	assert.Equal(t, "evaluation-model", cfg.Live.Optimizer.Model)
	assert.Equal(t, "https://models.example.test", cfg.Live.Optimizer.BaseURL)
	assert.Equal(t, 30, cfg.Live.Optimizer.TimeoutSeconds)
	assert.Equal(t, 1, cfg.Live.Optimizer.MaxRetries)
	assert.Equal(t, 0.25, cfg.Live.Optimizer.Temperature)
	assert.Equal(t, 1024, cfg.Live.Optimizer.MaxOutputTokens)
	assert.Equal(t, 2, cfg.Live.Optimizer.Budget.MaxCalls)
	assert.Equal(t, 16384, cfg.Live.Optimizer.Budget.MaxTokens)
	assert.Equal(t, 1.0, cfg.Live.Optimizer.Budget.MaxCostCNY)
}

func TestValidateLiveCallBudgetIncludesSymmetricRetries(t *testing.T) {
	cfg := pipelineConfig{
		Gate: gateFileConfig{PassK: 3, MaxCalls: 161},
		Live: liveConfig{
			MaxRetries: 2,
			Optimizer: liveOptimizerConfig{
				Budget: optimizerBudgetConfig{MaxCalls: 3},
			},
		},
	}
	train := evalSetFile{EvalCases: make([]caseSpec, 6)}
	validation := evalSetFile{EvalCases: make([]caseSpec, 7)}

	err := validateLiveCallBudget(cfg, train, validation)
	assert.ErrorContains(t, err, "cannot cover 165 required live calls")
	cfg.Gate.MaxCalls = 165
	assert.NoError(t, validateLiveCallBudget(cfg, train, validation))
}

func TestDefaultConfigCanReserveCandidateEvaluation(t *testing.T) {
	cfg, err := loadConfig("data/config.json")
	require.NoError(t, err)

	reservation := candidateEvaluationReservation(cfg)

	assert.Equal(t, 81, reservation.Calls)
	assert.LessOrEqual(t, reservation.Tokens, cfg.Gate.MaxTokens)
	assert.LessOrEqual(t, reservation.CostCNY, cfg.Gate.MaxCostCNY)
}

func TestValidateMetricsRejectsUnsupportedPolicyValues(t *testing.T) {
	valid := metricsConfig{Metrics: []metricSpec{
		{Name: "required_keywords", Threshold: 1, Kind: "deterministic"},
		{Name: "hard_failure", Threshold: 1, Kind: "red_line"},
		{Name: "pass_power_k", K: 3, Kind: "stability"},
		{Name: "paired_bootstrap", Confidence: bootstrapConfidence, Kind: "regression"},
	}}
	require.NoError(t, validateMetrics(valid, 3))

	tests := []struct {
		name   string
		metric string
		mutate func(*metricSpec)
	}{
		{name: "threshold", metric: "required_keywords", mutate: func(metric *metricSpec) { metric.Threshold = 0.5 }},
		{name: "confidence", metric: "paired_bootstrap", mutate: func(metric *metricSpec) { metric.Confidence = 0.99 }},
		{name: "kind", metric: "hard_failure", mutate: func(metric *metricSpec) { metric.Kind = "decorative" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			modified := metricsConfig{Metrics: append([]metricSpec(nil), valid.Metrics...)}
			for i := range modified.Metrics {
				if modified.Metrics[i].Name == test.metric {
					test.mutate(&modified.Metrics[i])
				}
			}
			assert.ErrorContains(t, validateMetrics(modified, 3), "policy is unsupported")
		})
	}
}

func TestValidateConfigRejectsUnsafeLiveBudgetValues(t *testing.T) {
	cfg := pipelineConfig{
		PromptFile:        "prompt",
		TrainEvalSet:      "train",
		ValidationEvalSet: "validation",
		MetricsFile:       "metrics",
		PromptIterFile:    "promptiter",
		OutputDir:         "output",
		Gate:              gateFileConfig{PassK: 3, MaxCostCNY: 20},
		Live: liveConfig{
			TimeoutSeconds:      1,
			MaxRetries:          -1,
			InputCNYPerMillion:  1,
			OutputCNYPerMillion: 2,
		},
	}
	err := validateConfig(cfg)
	assert.ErrorContains(t, err, "maxRetries")
}
