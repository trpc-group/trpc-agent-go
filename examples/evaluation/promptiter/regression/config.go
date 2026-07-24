//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type pipelineConfig struct {
	AppName         string            `json:"appName"`
	TargetSurfaceID string            `json:"targetSurfaceId"`
	MaxRounds       int               `json:"maxRounds"`
	Seed            int64             `json:"seed"`
	Inputs          inputConfig       `json:"inputs"`
	FakeModel       fakeModelConfig   `json:"fakeModel"`
	Gate            gateConfig        `json:"gate"`
	Candidates      []candidateConfig `json:"candidates"`
}

type inputConfig struct {
	TrainEvalSet      string `json:"trainEvalSet"`
	ValidationEvalSet string `json:"validationEvalSet"`
	Metrics           string `json:"metrics"`
	PromptSource      string `json:"promptSource"`
}

type fakeModelConfig struct {
	Name                       string  `json:"name"`
	PromptCostPerMillionTokens float64 `json:"promptCostPerMillionTokens"`
	OutputCostPerMillionTokens float64 `json:"outputCostPerMillionTokens"`
	LatencyMillis              int64   `json:"latencyMillis"`
}

type candidateConfig struct {
	ID             string            `json:"id"`
	Append         string            `json:"append"`
	Reason         string            `json:"reason"`
	TargetFailures []failureCategory `json:"targetFailures"`
}

func loadConfig(path string) (pipelineConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pipelineConfig{}, fmt.Errorf("read config: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config pipelineConfig
	if err := decoder.Decode(&config); err != nil {
		return pipelineConfig{}, fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return pipelineConfig{}, errors.New("decode config: multiple JSON values")
		}
		return pipelineConfig{}, fmt.Errorf("decode config trailing value: %w", err)
	}
	if err := validateConfig(config); err != nil {
		return pipelineConfig{}, err
	}
	baseDir := filepath.Dir(path)
	config.Inputs.TrainEvalSet = resolvePath(baseDir, config.Inputs.TrainEvalSet)
	config.Inputs.ValidationEvalSet = resolvePath(baseDir, config.Inputs.ValidationEvalSet)
	config.Inputs.Metrics = resolvePath(baseDir, config.Inputs.Metrics)
	config.Inputs.PromptSource = resolvePath(baseDir, config.Inputs.PromptSource)
	return config, nil
}

func validateConfig(config pipelineConfig) error {
	switch {
	case strings.TrimSpace(config.AppName) == "":
		return errors.New("app name is empty")
	case strings.TrimSpace(config.TargetSurfaceID) == "":
		return errors.New("target surface id is empty")
	case config.MaxRounds <= 0:
		return errors.New("max rounds must be greater than zero")
	case strings.TrimSpace(config.Inputs.TrainEvalSet) == "":
		return errors.New("train eval set path is empty")
	case strings.TrimSpace(config.Inputs.ValidationEvalSet) == "":
		return errors.New("validation eval set path is empty")
	case strings.TrimSpace(config.Inputs.Metrics) == "":
		return errors.New("metrics path is empty")
	case strings.TrimSpace(config.Inputs.PromptSource) == "":
		return errors.New("prompt source path is empty")
	case strings.TrimSpace(config.FakeModel.Name) == "":
		return errors.New("fake model name is empty")
	case config.FakeModel.LatencyMillis < 0:
		return errors.New("fake model latency must not be negative")
	case config.FakeModel.PromptCostPerMillionTokens < 0:
		return errors.New("fake model prompt token cost must not be negative")
	case config.FakeModel.OutputCostPerMillionTokens < 0:
		return errors.New("fake model output token cost must not be negative")
	case config.Gate.MaxCriticalScoreDrop < 0:
		return errors.New("maximum critical score drop must not be negative")
	case config.Gate.MaxEstimatedCostUSD < 0:
		return errors.New("maximum estimated cost must not be negative")
	case config.Gate.MaxToolCalls < 0:
		return errors.New("maximum tool calls must not be negative")
	case len(config.Candidates) == 0:
		return errors.New("candidate list is empty")
	}
	seen := make(map[string]struct{}, len(config.Candidates))
	for index, candidate := range config.Candidates {
		if strings.TrimSpace(candidate.ID) == "" {
			return fmt.Errorf("candidate %d id is empty", index)
		}
		if _, ok := seen[candidate.ID]; ok {
			return fmt.Errorf("candidate id %q is duplicated", candidate.ID)
		}
		seen[candidate.ID] = struct{}{}
		if strings.TrimSpace(candidate.Append) == "" {
			return fmt.Errorf("candidate %q append text is empty", candidate.ID)
		}
		if strings.TrimSpace(candidate.Reason) == "" {
			return fmt.Errorf("candidate %q reason is empty", candidate.ID)
		}
		if len(candidate.TargetFailures) == 0 {
			return fmt.Errorf("candidate %q target failures are empty", candidate.ID)
		}
		for _, category := range candidate.TargetFailures {
			if !isKnownFailureCategory(category) {
				return fmt.Errorf(
					"candidate %q has unknown target failure %q",
					candidate.ID,
					category,
				)
			}
		}
	}
	return nil
}

func isKnownFailureCategory(category failureCategory) bool {
	switch category {
	case failureFinalResponse,
		failureToolCall,
		failureToolArgument,
		failureRoute,
		failureFormat,
		failureKnowledgeRecall,
		failureUnknown:
		return true
	default:
		return false
	}
}

func resolvePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}
