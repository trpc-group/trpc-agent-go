//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"strings"
	"testing"
)

func TestValidateConfigRejectsInvalidCostAndFailureTargets(t *testing.T) {
	valid := pipelineConfig{
		AppName:         "app",
		TargetSurfaceID: "candidate#instruction",
		MaxRounds:       1,
		Inputs: inputConfig{
			TrainEvalSet:      "train.json",
			ValidationEvalSet: "validation.json",
			Metrics:           "metrics.json",
			PromptSource:      "prompt.txt",
		},
		FakeModel: fakeModelConfig{Name: "fake"},
		Candidates: []candidateConfig{{
			ID:             "candidate",
			Append:         "instruction",
			Reason:         "reason",
			TargetFailures: []failureCategory{failureFormat},
		}},
	}
	tests := []struct {
		name          string
		mutate        func(*pipelineConfig)
		errorContains string
	}{
		{
			name: "negative prompt cost",
			mutate: func(config *pipelineConfig) {
				config.FakeModel.PromptCostPerMillionTokens = -0.1
			},
			errorContains: "prompt token cost",
		},
		{
			name: "negative output cost",
			mutate: func(config *pipelineConfig) {
				config.FakeModel.OutputCostPerMillionTokens = -0.1
			},
			errorContains: "output token cost",
		},
		{
			name: "empty targets",
			mutate: func(config *pipelineConfig) {
				config.Candidates[0].TargetFailures = nil
			},
			errorContains: "target failures are empty",
		},
		{
			name: "unknown target",
			mutate: func(config *pipelineConfig) {
				config.Candidates[0].TargetFailures = []failureCategory{"typo"}
			},
			errorContains: "unknown target failure",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			config.Candidates = append([]candidateConfig(nil), valid.Candidates...)
			test.mutate(&config)
			err := validateConfig(config)
			if err == nil || !strings.Contains(err.Error(), test.errorContains) {
				t.Fatalf("validateConfig error = %v, want containing %q", err, test.errorContains)
			}
		})
	}
}
