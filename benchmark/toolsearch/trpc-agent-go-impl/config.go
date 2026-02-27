//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Mode string

const (
	ModeNone            Mode = "none"
	ModeLLMSearch       Mode = "llm"
	ModeKnowledgeSearch Mode = "knowledge"
)

func ParseMode(s string) (Mode, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", string(ModeLLMSearch):
		return ModeLLMSearch, nil
	case string(ModeNone):
		return ModeNone, nil
	case string(ModeKnowledgeSearch):
		return ModeKnowledgeSearch, nil
	default:
		return "", fmt.Errorf("invalid mode: %q (valid: none|llm|knowledge)", s)
	}
}

type BenchmarkConfig struct {
	AppName   string
	EvalSetID string
	DataDir   string
	OutputDir string

	NumRuns    int
	ModelName  string
	Mode       Mode
	MaxTools   int
	EmbedModel string
}

func (c BenchmarkConfig) Validate() error {
	if strings.TrimSpace(c.AppName) == "" {
		return fmt.Errorf("app name is empty")
	}
	if strings.TrimSpace(c.EvalSetID) == "" {
		return fmt.Errorf("evalset id is empty")
	}
	if c.NumRuns < 1 {
		return fmt.Errorf("num runs must be >= 1")
	}
	if strings.TrimSpace(c.ModelName) == "" {
		return fmt.Errorf("model name is empty")
	}
	if c.MaxTools < 1 {
		return fmt.Errorf("max tools must be >= 1")
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return fmt.Errorf("data dir is empty")
	}
	if strings.TrimSpace(c.OutputDir) == "" {
		return fmt.Errorf("output dir is empty")
	}
	if _, err := os.Stat(c.DataDir); err != nil {
		return fmt.Errorf("data dir not found: %w", err)
	}

	// Evaluation reads user inputs from <dataDir>/<app>/<evalset>.evalset.json.
	evalsetPath := filepath.Join(c.DataDir, c.AppName, c.EvalSetID+".evalset.json")
	if _, err := os.Stat(evalsetPath); err != nil {
		return fmt.Errorf("evalset file not found: %s: %w", evalsetPath, err)
	}
	metricsPath := filepath.Join(c.DataDir, c.AppName, c.EvalSetID+".metrics.json")
	if _, err := os.Stat(metricsPath); err != nil {
		return fmt.Errorf("metrics file not found: %s: %w", metricsPath, err)
	}

	_ = filepath.Clean(c.DataDir)
	_ = filepath.Clean(c.OutputDir)
	return nil
}
