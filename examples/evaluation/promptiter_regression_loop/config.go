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
	"fmt"
	"os"
)

// Config is the pipeline configuration loaded from promptiter.json.
type Config struct {
	AppName             string           `json:"appName"`
	CandidateName       string           `json:"candidateName"`
	DataDir             string           `json:"dataDir"`
	OutputDir           string           `json:"outputDir"`
	TrainEvalSetID      string           `json:"trainEvalSetID"`
	ValidationEvalSetID string           `json:"validationEvalSetID"`
	MetricFileID        string           `json:"metricFileID"`
	BaselinePromptFile  string           `json:"baselinePromptFile"`
	TargetSurfaceID     string           `json:"targetSurfaceID"`
	MaxRounds           int              `json:"maxRounds"`
	EvalCaseParallelism int              `json:"evalCaseParallelism"`
	ParallelInference   bool             `json:"parallelInference"`
	ParallelEvaluation  bool             `json:"parallelEvaluation"`
	Model               ModelConfig      `json:"model"`
	AcceptancePolicy    AcceptanceConfig `json:"acceptancePolicy"`
	StopPolicy          StopConfig       `json:"stopPolicy"`
	Gate                GateConfig       `json:"gate"`
}

// ModelConfig records which model the pipeline uses and how it was configured.
// The example ships with a deterministic scripted fake model so the full loop
// runs without any real API key; recording provider/name/seed makes every run
// reproducible and auditable.
type ModelConfig struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Seed     int64  `json:"seed"`
}

// AcceptanceConfig mirrors promptiterengine.AcceptancePolicy.
type AcceptanceConfig struct {
	MinScoreGain float64 `json:"minScoreGain"`
}

// StopConfig mirrors promptiterengine.StopPolicy.
type StopConfig struct {
	MaxRoundsWithoutAcceptance int `json:"maxRoundsWithoutAcceptance"`
}

// GateConfig defines the configurable acceptance gate applied on top of the
// PromptIter engine. Every check must pass for the candidate to be accepted.
type GateConfig struct {
	// MinScoreGain is the minimum validation score gain required against the
	// accepted baseline.
	MinScoreGain float64 `json:"minScoreGain"`
	// MaxNewHardFails is the maximum number of validation cases allowed to turn
	// from passing to failing. 0 means no newly broken case is tolerated.
	MaxNewHardFails int `json:"maxNewHardFails"`
	// KeyCaseIDs lists validation cases that must never regress. A regression on
	// any key case rejects the candidate regardless of the total score gain.
	KeyCaseIDs []string `json:"keyCaseIDs"`
	// MaxModelCalls caps the total number of fake/real model calls in the loop.
	MaxModelCalls int `json:"maxModelCalls"`
	// MaxLatencyMs caps the end-to-end pipeline latency in milliseconds.
	MaxLatencyMs int64 `json:"maxLatencyMs"`
}

// LoadConfig reads and validates the pipeline configuration file.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

// Validate checks required configuration fields.
func (c *Config) Validate() error {
	switch {
	case c == nil:
		return fmt.Errorf("config is nil")
	case c.AppName == "":
		return fmt.Errorf("appName is empty")
	case c.TrainEvalSetID == "":
		return fmt.Errorf("trainEvalSetID is empty")
	case c.ValidationEvalSetID == "":
		return fmt.Errorf("validationEvalSetID is empty")
	case c.MetricFileID == "":
		return fmt.Errorf("metricFileID is empty")
	case c.BaselinePromptFile == "":
		return fmt.Errorf("baselinePromptFile is empty")
	case c.TargetSurfaceID == "":
		return fmt.Errorf("targetSurfaceID is empty")
	case c.MaxRounds <= 0:
		return fmt.Errorf("maxRounds must be greater than 0")
	case c.EvalCaseParallelism <= 0:
		return fmt.Errorf("evalCaseParallelism must be greater than 0")
	default:
		return nil
	}
}
