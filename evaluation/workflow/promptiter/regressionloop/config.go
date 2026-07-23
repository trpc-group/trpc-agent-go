//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadConfig reads and validates a loop configuration from path.
func LoadConfig(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("config path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	resolveConfigPaths(&cfg, filepath.Dir(path))
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks that required configuration is present and coherent.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config is nil")
	}
	var missing []string
	if strings.TrimSpace(c.AppName) == "" {
		missing = append(missing, "appName")
	}
	if strings.TrimSpace(c.PromptSource.ID) == "" {
		missing = append(missing, "promptSource.id")
	}
	if strings.TrimSpace(c.PromptSource.Path) == "" {
		missing = append(missing, "promptSource.path")
	}
	if c.PromptSource.TargetType == "" {
		missing = append(missing, "promptSource.targetType")
	}
	if strings.TrimSpace(c.TrainEvalSet.ID) == "" {
		missing = append(missing, "trainEvalSet.id")
	}
	if strings.TrimSpace(c.TrainEvalSet.Path) == "" {
		missing = append(missing, "trainEvalSet.path")
	}
	if strings.TrimSpace(c.ValidationEvalSet.ID) == "" {
		missing = append(missing, "validationEvalSet.id")
	}
	if strings.TrimSpace(c.ValidationEvalSet.Path) == "" {
		missing = append(missing, "validationEvalSet.path")
	}
	if strings.TrimSpace(c.Metrics.Path) == "" {
		missing = append(missing, "metrics.path")
	}
	if c.PromptIter.MaxRounds <= 0 {
		missing = append(missing, "promptiter.maxRounds")
	}
	if len(c.PromptIter.TargetSurfaceIDs) == 0 {
		missing = append(missing, "promptiter.targetSurfaceIds")
	}
	if c.Runner.Mode == "" {
		missing = append(missing, "runner.mode")
	}
	if strings.TrimSpace(c.Output.Dir) == "" {
		missing = append(missing, "output.dir")
	}
	if strings.TrimSpace(c.Output.JSONReport) == "" {
		missing = append(missing, "output.jsonReport")
	}
	if strings.TrimSpace(c.Output.MarkdownReport) == "" {
		missing = append(missing, "output.markdownReport")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config fields: %s", strings.Join(missing, ", "))
	}
	if c.TrainEvalSet.ID == c.ValidationEvalSet.ID {
		return errors.New("train and validation eval sets must be distinct")
	}
	if c.Gate.MinValidationScoreGain < 0 {
		return errors.New("gate.minValidationScoreGain must be non-negative")
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
	switch c.PromptSource.TargetType {
	case PromptTargetSystemPrompt, PromptTargetAgentInstruction, PromptTargetSkillDescription, PromptTargetRouterPrompt:
	default:
		return fmt.Errorf("unsupported prompt target type %q", c.PromptSource.TargetType)
	}
	switch c.Runner.Mode {
	case RunnerModeFake, RunnerModeTrace, RunnerModeModel:
	default:
		return fmt.Errorf("unsupported runner mode %q", c.Runner.Mode)
	}
	if (c.Runner.Mode == RunnerModeFake || c.Runner.Mode == RunnerModeTrace) && c.Seed == 0 {
		return errors.New("deterministic fake/trace mode requires a non-zero seed")
	}
	return nil
}

func resolveConfigPaths(cfg *Config, baseDir string) {
	cfg.PromptSource.Path = resolvePath(baseDir, cfg.PromptSource.Path)
	cfg.TrainEvalSet.Path = resolvePath(baseDir, cfg.TrainEvalSet.Path)
	cfg.ValidationEvalSet.Path = resolvePath(baseDir, cfg.ValidationEvalSet.Path)
	cfg.Metrics.Path = resolvePath(baseDir, cfg.Metrics.Path)
	cfg.Runner.FixturePath = resolvePath(baseDir, cfg.Runner.FixturePath)
	cfg.Output.Dir = resolvePath(baseDir, cfg.Output.Dir)
	cfg.Output.JSONReport = resolveOutputPath(cfg.Output.Dir, cfg.Output.JSONReport)
	cfg.Output.MarkdownReport = resolveOutputPath(cfg.Output.Dir, cfg.Output.MarkdownReport)
}

func resolvePath(baseDir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func resolveOutputPath(outputDir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Clean(filepath.Join(outputDir, path))
}
