//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func loadInput(configPath string) (*LoadedInput, error) {
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	var cfg Config
	if err := readJSON(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	configDir := filepath.Dir(configPath)
	promptPath := resolvePath(configDir, cfg.PromptSource)
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		return nil, fmt.Errorf("read prompt source: %w", err)
	}
	var train EvalSetInput
	if err := readJSON(resolvePath(configDir, cfg.TrainEvalSet), &train); err != nil {
		return nil, fmt.Errorf("read train evalset: %w", err)
	}
	var validation EvalSetInput
	if err := readJSON(resolvePath(configDir, cfg.ValidationEvalSet), &validation); err != nil {
		return nil, fmt.Errorf("read validation evalset: %w", err)
	}
	var metrics []MetricInput
	if err := readJSON(resolvePath(configDir, cfg.Metrics), &metrics); err != nil {
		return nil, fmt.Errorf("read metrics: %w", err)
	}
	if cfg.AppName == "" {
		cfg.AppName = "promptiter-regression-app"
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "output"
	}
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = 1
	}
	if cfg.TargetSurfaceID == "" {
		cfg.TargetSurfaceID = "agent#instruction"
	}
	applyCriticalCaseConfig(&validation, cfg.Gate.CriticalCaseIDs)
	return &LoadedInput{
		Config:            cfg,
		ConfigDir:         configDir,
		BaselinePrompt:    string(prompt),
		TrainEvalSet:      train,
		ValidationEvalSet: validation,
		Metrics:           metrics,
	}, nil
}

func applyCriticalCaseConfig(set *EvalSetInput, caseIDs []string) {
	if set == nil || len(caseIDs) == 0 {
		return
	}
	critical := make(map[string]struct{}, len(caseIDs))
	for _, caseID := range caseIDs {
		critical[caseID] = struct{}{}
	}
	for i := range set.Cases {
		if _, ok := critical[set.Cases[i].EvalID]; ok {
			set.Cases[i].Critical = true
		}
	}
}

func readJSON(path string, target any) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return err
	}
	return nil
}

func resolvePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}
