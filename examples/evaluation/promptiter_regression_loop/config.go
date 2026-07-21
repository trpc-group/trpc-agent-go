//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main runs the deterministic PromptIter regression-loop example.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/internal/regression"
)

const (
	defaultConfigPath = "data/promptiter-regression-app/promptiter.json"
	defaultTimeout    = 2 * time.Minute
)

type durationValue time.Duration

func (d *durationValue) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode duration string: %w", err)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", value, err)
	}
	*d = durationValue(parsed)
	return nil
}

type config struct {
	AppName              string                `json:"appName"`
	TrainEvalSetID       string                `json:"trainEvalSetID"`
	ValidationEvalSetID  string                `json:"validationEvalSetID"`
	Seed                 int64                 `json:"seed"`
	Timeout              durationValue         `json:"timeout"`
	MaxAttempts          int                   `json:"maxAttempts"`
	TargetSurfaceID      string                `json:"targetSurfaceID"`
	CandidatePrompts     []string              `json:"candidatePrompts"`
	Gate                 regression.GateConfig `json:"gate"`
	OutputDir            string                `json:"outputDir"`
	BaselinePromptSource string                `json:"baselinePromptSource"`
	DataDir              string                `json:"-"`
	ConfigPath           string                `json:"-"`
	ConfigSHA256         string                `json:"-"`
}

func loadConfig(path string) (*config, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("config path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	cleanPath := filepath.Clean(path)
	cfg.ConfigPath = filepath.ToSlash(cleanPath)
	cfg.ConfigSHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
	cfg.DataDir = filepath.Dir(cleanPath)
	if cfg.Timeout == 0 {
		cfg.Timeout = durationValue(defaultTimeout)
	}
	if cfg.BaselinePromptSource == "" {
		cfg.BaselinePromptSource = filepath.Join(cfg.DataDir, "baseline_prompt.txt")
	} else if !filepath.IsAbs(cfg.BaselinePromptSource) {
		cfg.BaselinePromptSource = filepath.Join(cfg.DataDir, cfg.BaselinePromptSource)
	}
	if cfg.OutputDir != "" {
		cfg.OutputDir = filepath.Clean(cfg.OutputDir)
	}
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("multiple JSON values are not allowed")
}

func validateConfig(cfg *config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if strings.TrimSpace(cfg.AppName) == "" || strings.TrimSpace(cfg.TrainEvalSetID) == "" ||
		strings.TrimSpace(cfg.ValidationEvalSetID) == "" {
		return errors.New("app name and eval set ids must be non-empty")
	}
	if strings.TrimSpace(cfg.TargetSurfaceID) == "" || strings.TrimSpace(cfg.BaselinePromptSource) == "" ||
		strings.TrimSpace(cfg.OutputDir) == "" {
		return errors.New("target surface, prompt source, and output directory must be non-empty")
	}
	if cfg.MaxAttempts <= 0 {
		return errors.New("max attempts must be greater than zero")
	}
	if cfg.MaxAttempts != len(cfg.CandidatePrompts) {
		return errors.New("max attempts must equal candidate prompt count")
	}
	if time.Duration(cfg.Timeout) <= 0 {
		return errors.New("timeout must be greater than zero")
	}
	if err := regression.ValidateGateConfig(cfg.Gate); err != nil {
		return fmt.Errorf("validate gate: %w", err)
	}
	for index, prompt := range cfg.CandidatePrompts {
		if strings.TrimSpace(prompt) == "" {
			return fmt.Errorf("candidate prompt %d is empty", index+1)
		}
	}
	return nil
}

func loadBaselinePrompt(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read baseline prompt: %w", err)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", errors.New("baseline prompt is empty")
	}
	return prompt, nil
}
