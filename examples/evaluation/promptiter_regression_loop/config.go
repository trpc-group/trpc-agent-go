//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

const (
	appName          = "promptiter-regression-loop"
	agentName        = "candidate"
	toolName         = "echo_ping"
	targetSurfaceID  = "answer#instruction"
	defaultDataDir   = "./promptiter_regression_loop/data"
	defaultOutputDir = "./promptiter_regression_loop/output"
)

type fixturePaths struct {
	baseline   string
	train      string
	validation string
	metrics    string
	promptiter string
	fakeEngine string
}

type loopConfig struct {
	baseline   string
	train      *evalset.EvalSet
	validation *evalset.EvalSet
	metrics    []*metric.EvalMetric
	candidates []string
	maxRounds  int
	gate       regression.GatePolicy
	engine     fakeEngineConfig
	seed       int64
	inputs     []regression.AuditInput
	configHash string
}

type promptIterConfig struct {
	Candidates []string   `json:"candidates"`
	MaxRounds  int        `json:"maxRounds"`
	Gate       gateConfig `json:"gate"`
}

type gateConfig struct {
	MinValidationGain float64  `json:"minValidationGain"`
	RejectNewHardFail bool     `json:"rejectNewHardFail"`
	HardMetrics       []string `json:"hardMetrics"`
	CriticalCases     []string `json:"criticalCases"`
	MaxMetricDrop     float64  `json:"maxMetricDrop"`
	MaxModelCalls     int      `json:"maxModelCalls"`
	MaxTokens         int64    `json:"maxTokens"`
}

type fakeEngineConfig struct {
	EngineID           string `json:"engineId"`
	PromptTokens       int    `json:"promptTokens"`
	CompletionTokens   int    `json:"completionTokens"`
	LatencyMS          int64  `json:"latencyMillis"`
	OptimizerTokens    int64  `json:"optimizerTokens"`
	OptimizerLatencyMS int64  `json:"optimizerLatencyMillis"`
}

func defaultPaths() fixturePaths {
	baseDir := defaultDataDir
	if _, err := os.Stat(baseDir); err != nil {
		if _, localErr := os.Stat("./data"); localErr == nil {
			baseDir = "./data"
		}
	}
	return fixturePaths{
		baseline:   filepath.Join(baseDir, "baseline_prompt.txt"),
		train:      filepath.Join(baseDir, "train.evalset.json"),
		validation: filepath.Join(baseDir, "validation.evalset.json"),
		metrics:    filepath.Join(baseDir, "metrics.json"),
		promptiter: filepath.Join(baseDir, "promptiter.json"),
		fakeEngine: filepath.Join(baseDir, "fake_engine.json"),
	}
}

func loadConfig(paths fixturePaths, seed int64) (loopConfig, error) {
	var config loopConfig
	config.seed = seed
	baseline, audit, err := readInput("baseline_prompt", paths.baseline)
	if err != nil {
		return config, err
	}
	config.baseline = strings.TrimSpace(string(baseline))
	config.inputs = append(config.inputs, audit)
	var promptConfig promptIterConfig
	files := []struct {
		name string
		path string
		out  any
	}{
		{name: "train_evalset", path: paths.train, out: &config.train},
		{name: "validation_evalset", path: paths.validation, out: &config.validation},
		{name: "metrics", path: paths.metrics, out: &config.metrics},
		{name: "promptiter", path: paths.promptiter, out: &promptConfig},
		{name: "fake_engine", path: paths.fakeEngine, out: &config.engine},
	}
	hasher := sha256.New()
	hasher.Write(baseline)
	for _, file := range files {
		data, input, err := readInput(file.name, file.path)
		if err != nil {
			return loopConfig{}, err
		}
		if err := json.Unmarshal(data, file.out); err != nil {
			return loopConfig{}, fmt.Errorf("decode %s: %w", file.name, err)
		}
		config.inputs = append(config.inputs, input)
		hasher.Write(data)
	}
	config.candidates = promptConfig.Candidates
	config.maxRounds = promptConfig.MaxRounds
	config.gate = regression.GatePolicy{
		MinValidationGain: promptConfig.Gate.MinValidationGain,
		RejectNewHardFail: promptConfig.Gate.RejectNewHardFail,
		HardMetrics:       promptConfig.Gate.HardMetrics,
		CriticalCases:     promptConfig.Gate.CriticalCases,
		MaxMetricDrop:     promptConfig.Gate.MaxMetricDrop,
		MaxModelCalls:     promptConfig.Gate.MaxModelCalls,
		MaxTokens:         promptConfig.Gate.MaxTokens,
	}
	hasher.Write([]byte(fmt.Sprint(seed)))
	config.configHash = hex.EncodeToString(hasher.Sum(nil))
	if err := validateConfig(config); err != nil {
		return loopConfig{}, err
	}
	return config, nil
}

func readInput(name, path string) ([]byte, regression.AuditInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, regression.AuditInput{}, fmt.Errorf("read %s %q: %w", name, path, err)
	}
	hash := sha256.Sum256(data)
	return data, regression.AuditInput{Name: name, Path: path, SHA256: hex.EncodeToString(hash[:])}, nil
}

func validateConfig(config loopConfig) error {
	switch {
	case config.baseline == "":
		return errors.New("baseline prompt is empty")
	case config.train == nil || config.validation == nil:
		return errors.New("train and validation evalsets are required")
	case config.train.EvalSetID == "" || config.validation.EvalSetID == "":
		return errors.New("evalset IDs are required")
	case config.train.EvalSetID == config.validation.EvalSetID:
		return errors.New("train and validation evalset IDs must differ")
	case len(config.train.EvalCases) != 3 || len(config.validation.EvalCases) != 3:
		return errors.New("fixtures require three train and three validation cases")
	case len(config.metrics) == 0:
		return errors.New("metrics are empty")
	case config.maxRounds <= 0 || len(config.candidates) < config.maxRounds:
		return errors.New("candidate count must cover maxRounds")
	case config.engine.EngineID == "":
		return errors.New("fake engine ID is empty")
	case config.engine.PromptTokens < 0 || config.engine.CompletionTokens < 0 ||
		config.engine.LatencyMS < 0 || config.engine.OptimizerTokens < 0 || config.engine.OptimizerLatencyMS < 0:
		return errors.New("fake engine cost must not be negative")
	}
	for index, candidate := range config.candidates {
		if strings.TrimSpace(candidate) == "" {
			return fmt.Errorf("candidate %d is empty", index+1)
		}
	}
	return nil
}
