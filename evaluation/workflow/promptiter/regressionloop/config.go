//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	configDir := filepath.Dir(path)
	if err := config.ResolvePaths(configDir); err != nil {
		return nil, fmt.Errorf("resolve paths: %w", err)
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &config, nil
}

func (c *Config) ResolvePaths(baseDir string) error {
	if baseDir == "" {
		return nil
	}

	if c.TrainEvalSetPath != "" {
		c.TrainEvalSetPath = resolvePath(baseDir, c.TrainEvalSetPath)
	}
	if c.ValidationEvalSetPath != "" {
		c.ValidationEvalSetPath = resolvePath(baseDir, c.ValidationEvalSetPath)
	}
	if c.MetricsPath != "" {
		c.MetricsPath = resolvePath(baseDir, c.MetricsPath)
	}
	if c.BaselinePromptPath != "" {
		c.BaselinePromptPath = resolvePath(baseDir, c.BaselinePromptPath)
	}
	if c.PromptiterConfigPath != "" {
		c.PromptiterConfigPath = resolvePath(baseDir, c.PromptiterConfigPath)
	}
	if c.Output.OutputDir != "" {
		c.Output.OutputDir = resolvePath(baseDir, c.Output.OutputDir)
	}

	return nil
}

func resolvePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if len(path) > 0 && path[0] == '/' {
		return path
	}
	return filepath.Join(baseDir, path)
}

func (c *Config) Validate() error {
	if c.TrainEvalSetPath == "" {
		return errors.New("trainEvalSetPath is required")
	}
	if c.ValidationEvalSetPath == "" {
		return errors.New("validationEvalSetPath is required")
	}
	if c.MetricsPath == "" {
		return errors.New("metricsPath is required")
	}
	if c.BaselinePromptPath == "" {
		return errors.New("baselinePromptPath is required")
	}
	if c.PromptiterConfigPath == "" {
		return errors.New("promptiterConfigPath is required")
	}
	if c.Mode != "fake" && c.Mode != "trace-smoke" && c.Mode != "real" {
		return errors.New("mode must be one of: fake, trace-smoke, real")
	}
	if c.Seed <= 0 && (c.Mode == "fake" || c.Mode == "trace-smoke") {
		return errors.New("seed must be positive for fake/trace-smoke mode")
	}
	if c.Gate.MinValidationGain < 0 {
		return errors.New("gate.minValidationGain must be non-negative")
	}
	if c.Gate.MaxNewHardFailCount < 0 {
		return errors.New("gate.maxNewHardFailCount must be non-negative")
	}
	if c.Gate.MaxRegressedCases < 0 {
		return errors.New("gate.maxRegressedCases must be non-negative")
	}
	if c.Gate.MaxCost < 0 {
		return errors.New("gate.maxCost must be non-negative")
	}
	if c.Gate.MaxCalls < 0 {
		return errors.New("gate.maxCalls must be non-negative")
	}
	if c.Gate.MaxLatencyMS < 0 {
		return errors.New("gate.maxLatencyMs must be non-negative")
	}
	if c.Optimization.MaxRounds <= 0 {
		return errors.New("optimization.maxRounds must be positive")
	}
	if len(c.Optimization.TargetSurfaceIDs) == 0 {
		return errors.New("optimization.targetSurfaceIds must not be empty")
	}
	if c.Optimization.MinScoreGain < 0 {
		return errors.New("optimization.minScoreGain must be non-negative")
	}
	if c.Optimization.CaseParallelism < 0 {
		return errors.New("optimization.caseParallelism must be non-negative")
	}

	return nil
}

func (c *Config) Hash() (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
