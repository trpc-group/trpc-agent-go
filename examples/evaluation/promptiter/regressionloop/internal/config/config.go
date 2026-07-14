//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package config loads and validates the regression-loop configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

// Config is the versioned promptiter.json document.
type Config struct {
	Version int    `json:"version"`
	Seed    int64  `json:"seed"`
	Mode    string `json:"mode"`
	Prompt  struct {
		SourceFile       string   `json:"sourceFile"`
		TargetSurfaceIDs []string `json:"targetSurfaceIds"`
	} `json:"prompt"`
	Evaluation struct {
		TrainEvalSetID      string `json:"trainEvalSetId"`
		ValidationEvalSetID string `json:"validationEvalSetId"`
		TrainFile           string `json:"trainFile"`
		ValidationFile      string `json:"validationFile"`
		MetricsFile         string `json:"metricsFile"`
		TraceMode           bool   `json:"traceMode"`
		ExpectedAgentName   string `json:"expectedAgentName"`
	} `json:"evaluation"`
	Optimization struct {
		MaxRounds                  int     `json:"maxRounds"`
		MaxRoundsWithoutAcceptance int     `json:"maxRoundsWithoutAcceptance"`
		MinScoreGain               float64 `json:"minScoreGain"`
	} `json:"optimization"`
	Gate  regression.GatePolicy `json:"gate"`
	Audit struct {
		OutputDir          string `json:"outputDir"`
		SaveRoundArtifacts bool   `json:"saveRoundArtifacts"`
	} `json:"audit"`
}

// Load decodes and validates a config relative to its containing directory.
func Load(path string) (*Config, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate rejects unsafe or internally inconsistent configurations.
func (c *Config) Validate(baseDir string) error {
	if c == nil {
		return errors.New("config is nil")
	}
	if err := c.validateBasic(); err != nil {
		return err
	}
	if err := c.validateCriticalCases(baseDir); err != nil {
		return err
	}
	return c.validateOutputPaths(baseDir)
}

func (c *Config) validateBasic() error {
	switch {
	case c.Version != 1:
		return fmt.Errorf("unsupported config version %d", c.Version)
	case c.Mode != "fake":
		return fmt.Errorf("unsupported mode %q: this reproducible example requires fake", c.Mode)
	case c.Evaluation.TrainEvalSetID == "" || c.Evaluation.ValidationEvalSetID == "":
		return errors.New("train and validation eval set ids must not be empty")
	case c.Evaluation.TrainEvalSetID == c.Evaluation.ValidationEvalSetID:
		return errors.New("train and validation eval sets must differ")
	case c.Optimization.MaxRounds <= 0:
		return errors.New("max rounds must be greater than zero")
	case c.Optimization.MaxRoundsWithoutAcceptance <= 0:
		return errors.New("max rounds without acceptance must be greater than zero")
	case !finite(c.Optimization.MinScoreGain), !finite(c.Gate.MinValidationScoreGain):
		return errors.New("score thresholds must be finite")
	case c.Gate.MaxNewHardFailures < 0, c.Gate.MaxToolCallIncrease < 0, c.Gate.MaxModelCallIncrease < 0:
		return errors.New("gate count thresholds must be non-negative")
	case !finite(c.Gate.MaxLatencyIncrease), !finite(c.Gate.MaxCostIncrease), c.Gate.MaxLatencyIncrease < 0, c.Gate.MaxCostIncrease < 0:
		return errors.New("gate budgets must be finite and non-negative")
	case strings.TrimSpace(c.Evaluation.ExpectedAgentName) == "":
		return errors.New("expected agent name must not be empty")
	case len(c.Prompt.TargetSurfaceIDs) == 0:
		return errors.New("target surface ids must not be empty")
	case strings.TrimSpace(c.Prompt.SourceFile) == "":
		return errors.New("prompt source file must not be empty")
	case strings.TrimSpace(c.Audit.OutputDir) == "":
		return errors.New("audit output directory must not be empty")
	}
	for _, id := range c.Prompt.TargetSurfaceIDs {
		if strings.TrimSpace(id) == "" {
			return errors.New("target surface id must not be empty")
		}
	}
	for _, policy := range c.Gate.CriticalCases {
		if !finite(policy.MaxScoreDrop) || policy.MaxScoreDrop < 0 {
			return fmt.Errorf("critical case %q max score drop must be finite and non-negative", policy.CaseID)
		}
	}
	return nil
}

func (c *Config) validateCriticalCases(baseDir string) error {
	validationIDs, err := evalCaseIDs(filepath.Join(baseDir, c.Evaluation.ValidationFile))
	if err != nil {
		return fmt.Errorf("load validation cases: %w", err)
	}
	for _, policy := range c.Gate.CriticalCases {
		if _, ok := validationIDs[policy.CaseID]; !ok {
			return fmt.Errorf("critical case %q is not in validation eval set", policy.CaseID)
		}
	}
	return nil
}

func (c *Config) validateOutputPaths(baseDir string) error {
	inputs := []string{c.Prompt.SourceFile, c.Evaluation.TrainFile, c.Evaluation.ValidationFile, c.Evaluation.MetricsFile}
	output := cleanAbs(baseDir, c.Audit.OutputDir)
	for _, input := range inputs {
		if input == "" {
			continue
		}
		inputPath := cleanAbs(baseDir, input)
		if output == inputPath || strings.HasPrefix(inputPath, output+string(filepath.Separator)) {
			return fmt.Errorf("output directory %q contains input %q", c.Audit.OutputDir, input)
		}
	}
	return nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func cleanAbs(baseDir, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func evalCaseIDs(path string) (map[string]struct{}, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var document struct {
		EvalCases []struct {
			EvalID string `json:"evalId"`
		} `json:"evalCases"`
	}
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(document.EvalCases))
	for _, evalCase := range document.EvalCases {
		ids[evalCase.EvalID] = struct{}{}
	}
	return ids, nil
}
