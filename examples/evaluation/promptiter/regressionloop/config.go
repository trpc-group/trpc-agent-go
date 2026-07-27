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
	"net/url"
	"os"
	"strings"
)

type runMode string

const (
	modeDeterministic runMode = "deterministic"
	modeLive          runMode = "live"

	defaultAPIKeyEnv = "OPENAI_API_KEY"
)

type roleConfig struct {
	Model      string   `json:"model"`
	BaseURL    string   `json:"baseURL"`
	APIKeyEnv  string   `json:"apiKeyEnv"`
	InputPerM  *float64 `json:"inputPerMillion,omitempty"`
	OutputPerM *float64 `json:"outputPerMillion,omitempty"`
}

type criticalRule struct {
	EvalCaseID   string   `json:"evalCaseId"`
	MetricName   string   `json:"metricName,omitempty"`
	MustPass     bool     `json:"mustPass"`
	MinScore     *float64 `json:"minScore,omitempty"`
	MaxScoreDrop *float64 `json:"maxScoreDrop,omitempty"`
}

type config struct {
	Mode                runMode        `json:"mode"`
	Seed                int64          `json:"seed"`
	MaxRounds           int            `json:"maxRounds"`
	TrainEvalSetID      string         `json:"trainEvalSetId"`
	SearchEvalSetID     string         `json:"searchEvalSetId"`
	ValidationEvalSetID string         `json:"validationEvalSetId"`
	MetricFileID        string         `json:"metricFileId"`
	MinValidationGain   float64        `json:"minValidationGain"`
	MaxHardFailures     int            `json:"maxHardFailures"`
	MaxCaseScoreDrop    float64        `json:"maxCaseScoreDrop"`
	MaxModelCalls       *int           `json:"maxModelCalls,omitempty"`
	MaxToolCalls        *int           `json:"maxToolCalls,omitempty"`
	MaxTokens           *int           `json:"maxTokens,omitempty"`
	MaxEstimatedCost    *float64       `json:"maxEstimatedCost,omitempty"`
	MaxLatencyMillis    *int64         `json:"maxLatencyMillis,omitempty"`
	Critical            []criticalRule `json:"critical,omitempty"`
	EvalCaseParallelism int            `json:"evalCaseParallelism"`
	ParallelInference   bool           `json:"parallelInference"`
	ParallelEvaluation  bool           `json:"parallelEvaluation"`
	Candidate           roleConfig     `json:"candidate"`
	Judge               roleConfig     `json:"judge"`
	Worker              roleConfig     `json:"worker"`
}

func loadConfig(path string) (*config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	var cfg config
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	switch err := decoder.Decode(&trailing); {
	case err == nil:
		return nil, errors.New("decode config: trailing JSON value")
	case !errors.Is(err, io.EOF):
		return nil, fmt.Errorf("decode config trailing data: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return &cfg, nil
}

func (cfg config) validate() error {
	if cfg.Mode != modeDeterministic && cfg.Mode != modeLive {
		return fmt.Errorf("mode %q is unsupported", cfg.Mode)
	}
	if cfg.MaxRounds <= 0 {
		return errors.New("max rounds must be positive")
	}
	if cfg.EvalCaseParallelism <= 0 {
		return errors.New("evaluation case parallelism must be positive")
	}
	if strings.TrimSpace(cfg.TrainEvalSetID) == "" {
		return errors.New("training evaluation set ID is empty")
	}
	if strings.TrimSpace(cfg.SearchEvalSetID) == "" {
		return errors.New("search evaluation set ID is empty")
	}
	if strings.TrimSpace(cfg.ValidationEvalSetID) == "" {
		return errors.New("validation evaluation set ID is empty")
	}
	if strings.TrimSpace(cfg.MetricFileID) == "" {
		return errors.New("metric file ID is empty")
	}
	if cfg.SearchEvalSetID == cfg.ValidationEvalSetID {
		return errors.New("search evaluation set must not use held-out validation")
	}
	if cfg.SearchEvalSetID != cfg.TrainEvalSetID {
		return errors.New("search evaluation set must equal the training evaluation set")
	}
	if cfg.MinValidationGain < 0 {
		return errors.New("minimum validation gain must be nonnegative")
	}
	if cfg.MaxHardFailures < 0 {
		return errors.New("maximum hard failures must be nonnegative")
	}
	if cfg.MaxCaseScoreDrop < 0 {
		return errors.New("maximum case score drop must be nonnegative")
	}
	if err := validateOptionalInt("max model calls", cfg.MaxModelCalls); err != nil {
		return err
	}
	if err := validateOptionalInt("max tool calls", cfg.MaxToolCalls); err != nil {
		return err
	}
	if err := validateOptionalInt("max tokens", cfg.MaxTokens); err != nil {
		return err
	}
	if cfg.MaxEstimatedCost != nil && *cfg.MaxEstimatedCost < 0 {
		return errors.New("max estimated cost must be nonnegative")
	}
	if cfg.MaxLatencyMillis != nil && *cfg.MaxLatencyMillis < 0 {
		return errors.New("max latency milliseconds must be nonnegative")
	}
	for i, rule := range cfg.Critical {
		if strings.TrimSpace(rule.EvalCaseID) == "" {
			return fmt.Errorf("critical rule %d evaluation case ID is empty", i)
		}
		if !rule.MustPass && rule.MinScore == nil && rule.MaxScoreDrop == nil {
			return fmt.Errorf("critical rule %d must define a condition", i)
		}
		if rule.MaxScoreDrop != nil && *rule.MaxScoreDrop < 0 {
			return fmt.Errorf("critical rule %d maximum score drop must be nonnegative", i)
		}
	}
	roles := []struct {
		name string
		cfg  roleConfig
	}{
		{name: "candidate", cfg: cfg.Candidate},
		{name: "judge", cfg: cfg.Judge},
		{name: "worker", cfg: cfg.Worker},
	}
	for _, role := range roles {
		if err := validateRolePricing(role.name, role.cfg); err != nil {
			return err
		}
	}
	if cfg.Mode == modeLive {
		for _, role := range roles {
			if err := validateLiveRole(role.name, role.cfg); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOptionalInt(name string, value *int) error {
	if value != nil && *value < 0 {
		return fmt.Errorf("%s must be nonnegative", name)
	}
	return nil
}

func validateLiveRole(name string, role roleConfig) error {
	if strings.TrimSpace(role.Model) == "" {
		return fmt.Errorf("%s model is empty", name)
	}
	if strings.TrimSpace(role.APIKeyEnv) == "" {
		return fmt.Errorf("%s API key environment name is empty", name)
	}
	if role.APIKeyEnv != defaultAPIKeyEnv && strings.TrimSpace(role.BaseURL) == "" {
		return fmt.Errorf("%s base URL is required with non-default credentials", name)
	}
	if strings.TrimSpace(role.BaseURL) != "" {
		parsed, err := url.Parse(strings.TrimSpace(role.BaseURL))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("%s base URL is invalid", name)
		}
		if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("%s base URL must not contain credentials, query, or fragment", name)
		}
	}
	return nil
}

func validateRolePricing(name string, role roleConfig) error {
	if role.InputPerM != nil && *role.InputPerM < 0 {
		return fmt.Errorf("%s input price must be nonnegative", name)
	}
	if role.OutputPerM != nil && *role.OutputPerM < 0 {
		return fmt.Errorf("%s output price must be nonnegative", name)
	}
	return nil
}
