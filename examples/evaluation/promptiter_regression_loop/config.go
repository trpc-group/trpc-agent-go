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
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const configSchemaVersion = "trpc-agent-go.promptiter-regression.config/v1alpha1"

type config struct {
	SchemaVersion string             `json:"schemaVersion"`
	Scenario      string             `json:"scenario"`
	Seed          int64              `json:"seed"`
	MaxRounds     int                `json:"maxRounds"`
	Inputs        inputConfig        `json:"inputs"`
	OutputDir     string             `json:"outputDir"`
	DatasetGuard  datasetGuardConfig `json:"datasetGuard"`
	Gate          gateConfig         `json:"gate"`
	Budget        budgetConfig       `json:"budget"`
	configPath    string
}

type inputConfig struct {
	TrainEvalset      string `json:"trainEvalset"`
	ValidationEvalset string `json:"validationEvalset"`
	Metrics           string `json:"metrics"`
	BaselinePrompt    string `json:"baselinePrompt"`
}

type datasetGuardConfig struct {
	FailOnExactOverlap bool    `json:"failOnExactOverlap"`
	FailOnNearOverlap  bool    `json:"failOnNearOverlap"`
	NearThreshold      float64 `json:"nearThreshold"`
}

type gateConfig struct {
	MinValidationGain       float64  `json:"minValidationGain"`
	HardMetrics             []string `json:"hardMetrics"`
	CriticalCases           []string `json:"criticalCases"`
	MaxMetricRegression     float64  `json:"maxMetricRegression"`
	MaxGeneralizationGap    float64  `json:"maxGeneralizationGap"`
	RequireCompleteMatrix   bool     `json:"requireCompleteMatrix"`
	RejectUnexpectedMetrics bool     `json:"rejectUnexpectedMetrics"`
}

type budgetConfig struct {
	MaxModelCalls  int      `json:"maxModelCalls"`
	MaxTotalTokens int      `json:"maxTotalTokens"`
	MaxLatencyMS   int64    `json:"maxLatencyMs"`
	MaxCost        *float64 `json:"maxCost"`
}

func loadConfig(path string) (*config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	file, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var cfg config
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, errors.New("config contains trailing JSON values")
		}
		return nil, fmt.Errorf("decode trailing config content: %w", err)
	}
	cfg.configPath = abs
	baseDir := filepath.Dir(abs)
	if strings.TrimSpace(cfg.OutputDir) == "" {
		return nil, errors.New("outputDir is empty")
	}
	cfg.Inputs.TrainEvalset = resolveConfigPath(baseDir, cfg.Inputs.TrainEvalset)
	cfg.Inputs.ValidationEvalset = resolveConfigPath(baseDir, cfg.Inputs.ValidationEvalset)
	cfg.Inputs.Metrics = resolveConfigPath(baseDir, cfg.Inputs.Metrics)
	cfg.Inputs.BaselinePrompt = resolveConfigPath(baseDir, cfg.Inputs.BaselinePrompt)
	cfg.OutputDir = resolveConfigPath(baseDir, cfg.OutputDir)
	if cfg.DatasetGuard.NearThreshold == 0 {
		cfg.DatasetGuard.NearThreshold = 0.9
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func resolveConfigPath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func (c *config) validate() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.SchemaVersion != configSchemaVersion {
		return fmt.Errorf("unsupported schema version %q", c.SchemaVersion)
	}
	switch c.Scenario {
	case scenarioImprovement, scenarioNoop, scenarioOverfit, scenarioMultiRound:
	default:
		return fmt.Errorf("unsupported scenario %q", c.Scenario)
	}
	if c.MaxRounds < 1 {
		return errors.New("maxRounds must be at least 1")
	}
	for name, path := range map[string]string{
		"trainEvalset":      c.Inputs.TrainEvalset,
		"validationEvalset": c.Inputs.ValidationEvalset,
		"metrics":           c.Inputs.Metrics,
		"baselinePrompt":    c.Inputs.BaselinePrompt,
	} {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s must be a regular file", name)
		}
	}
	if strings.TrimSpace(c.OutputDir) == "" {
		return errors.New("outputDir is empty")
	}
	if math.IsNaN(c.DatasetGuard.NearThreshold) || math.IsInf(c.DatasetGuard.NearThreshold, 0) ||
		c.DatasetGuard.NearThreshold <= 0 || c.DatasetGuard.NearThreshold > 1 {
		return errors.New("datasetGuard.nearThreshold must be in (0, 1]")
	}
	for name, value := range map[string]float64{
		"minValidationGain":    c.Gate.MinValidationGain,
		"maxMetricRegression":  c.Gate.MaxMetricRegression,
		"maxGeneralizationGap": c.Gate.MaxGeneralizationGap,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("%s must be a finite non-negative number", name)
		}
	}
	if c.Budget.MaxModelCalls < 1 || c.Budget.MaxTotalTokens < 1 || c.Budget.MaxLatencyMS < 1 {
		return errors.New("model call, token, and latency budgets must be positive")
	}
	if c.Budget.MaxCost != nil && (math.IsNaN(*c.Budget.MaxCost) || math.IsInf(*c.Budget.MaxCost, 0) || *c.Budget.MaxCost < 0) {
		return errors.New("maxCost must be a finite non-negative number")
	}
	return nil
}

func (c *config) timeout() time.Duration {
	return time.Duration(c.Budget.MaxLatencyMS) * time.Millisecond
}

func (c *config) reportJSONPath() string {
	return filepath.Join(c.OutputDir, "optimization_report.json")
}

func (c *config) reportMarkdownPath() string {
	return filepath.Join(c.OutputDir, "optimization_report.md")
}
