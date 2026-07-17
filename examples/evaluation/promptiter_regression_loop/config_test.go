//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
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
