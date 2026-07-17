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

func TestValidateLiveCallBudgetIncludesSymmetricRetries(t *testing.T) {
	cfg := pipelineConfig{
		Gate: gateFileConfig{PassK: 3, MaxCalls: 161},
		Live: liveConfig{MaxRetries: 2},
	}
	train := evalSetFile{EvalCases: make([]caseSpec, 6)}
	validation := evalSetFile{EvalCases: make([]caseSpec, 7)}

	err := validateLiveCallBudget(cfg, train, validation)
	assert.ErrorContains(t, err, "cannot cover 162 required live calls")
	cfg.Gate.MaxCalls = 162
	assert.NoError(t, validateLiveCallBudget(cfg, train, validation))
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
