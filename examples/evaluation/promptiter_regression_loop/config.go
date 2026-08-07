// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/internal/regression"
)

const defaultTimeout = 2 * time.Minute

const evalSetFileSuffix = ".evalset.json"

var defaultConfigCandidates = []string{
	"data/promptiter-regression-app/promptiter.json",
	"promptiter_regression_loop/data/promptiter-regression-app/promptiter.json",
	"examples/evaluation/promptiter_regression_loop/data/promptiter-regression-app/promptiter.json",
}

type durationValue time.Duration

func (d *durationValue) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode duration: %w", err)
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
	TargetSurfaceID      string                `json:"targetSurfaceID"`
	CandidatePrompts     []string              `json:"candidatePrompts"`
	Gate                 regression.GatePolicy `json:"gate"`
	BaselinePromptSource string                `json:"baselinePromptSource"`
	OutputDir            string                `json:"outputDir"`
	DataDir              string                `json:"-"`
	ConfigPath           string                `json:"-"`
	ConfigSHA256         string                `json:"-"`
	configData           []byte
}

type inputSnapshot struct {
	sha256         string
	baselinePrompt string
	evalSets       map[string]*evalset.EvalSet
	metrics        []*metric.EvalMetric
	catalog        regression.AttributionCatalog
}

func resolveConfigPath(path string) (string, error) {
	if strings.TrimSpace(path) != "" {
		return filepath.Clean(path), nil
	}
	for _, candidate := range defaultConfigCandidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return filepath.Clean(candidate), nil
		}
	}
	return "", errors.New("default promptiter configuration was not found")
}

func loadConfig(path string) (*config, error) {
	resolved, err := resolveConfigPath(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(resolved)
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
	cfg.ConfigPath = filepath.ToSlash(resolved)
	cfg.ConfigSHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
	cfg.configData = append([]byte(nil), data...)
	cfg.DataDir = filepath.Dir(resolved)
	if cfg.Timeout == 0 {
		cfg.Timeout = durationValue(defaultTimeout)
	}
	for index, prompt := range cfg.CandidatePrompts {
		cfg.CandidatePrompts[index] = strings.TrimSpace(prompt)
	}
	if cfg.BaselinePromptSource == "" {
		cfg.BaselinePromptSource = "baseline_prompt.txt"
	}
	cfg.BaselinePromptSource = resolveRelative(cfg.DataDir, cfg.BaselinePromptSource)
	cfg.OutputDir = resolveRelative(cfg.DataDir, cfg.OutputDir)
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

func resolveRelative(baseDir, path string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func validateConfig(cfg *config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	for name, value := range map[string]string{
		"app name":               cfg.AppName,
		"train eval set id":      cfg.TrainEvalSetID,
		"validation eval set id": cfg.ValidationEvalSetID,
		"target surface id":      cfg.TargetSurfaceID,
		"baseline prompt source": cfg.BaselinePromptSource,
		"output directory":       cfg.OutputDir,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is empty", name)
		}
	}
	if cfg.TrainEvalSetID == cfg.ValidationEvalSetID {
		return errors.New("train and validation eval set ids must be different")
	}
	if time.Duration(cfg.Timeout) <= 0 {
		return errors.New("timeout must be greater than zero")
	}
	if len(cfg.CandidatePrompts) == 0 {
		return errors.New("candidate prompts are empty")
	}
	for index, prompt := range cfg.CandidatePrompts {
		if strings.TrimSpace(prompt) == "" {
			return fmt.Errorf("candidate prompt %d is empty", index+1)
		}
	}
	if err := regression.ValidateGatePolicy(cfg.Gate); err != nil {
		return fmt.Errorf("validate gate: %w", err)
	}
	return nil
}

func loadInputSnapshot(cfg *config) (*inputSnapshot, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if len(cfg.configData) == 0 {
		return nil, errors.New("config source bytes are unavailable")
	}
	inputs := []struct {
		name string
		path string
		data []byte
	}{
		{name: "config", path: cfg.ConfigPath, data: cfg.configData},
		{name: "baselinePrompt", path: cfg.BaselinePromptSource},
		{name: "trainEvalSet", path: filepath.Join(cfg.DataDir, cfg.TrainEvalSetID+evalSetFileSuffix)},
		{name: "validationEvalSet", path: filepath.Join(cfg.DataDir, cfg.ValidationEvalSetID+evalSetFileSuffix)},
		{name: "metrics", path: filepath.Join(cfg.DataDir, "metrics.json")},
	}
	var combined []byte
	for index := range inputs {
		if inputs[index].data == nil {
			data, err := os.ReadFile(inputs[index].path)
			if err != nil {
				return nil, fmt.Errorf("read %s input: %w", inputs[index].name, err)
			}
			inputs[index].data = data
		}
		digest := sha256.Sum256(inputs[index].data)
		combined = append(combined, inputs[index].name...)
		combined = append(combined, 0)
		combined = append(combined, digest[:]...)
	}
	baselinePrompt := strings.TrimSpace(string(inputs[1].data))
	if baselinePrompt == "" {
		return nil, errors.New("baseline prompt is empty")
	}
	evalSets := make(map[string]*evalset.EvalSet, 2)
	for _, item := range []struct {
		id   string
		name string
		data []byte
	}{
		{id: cfg.TrainEvalSetID, name: "train eval set", data: inputs[2].data},
		{id: cfg.ValidationEvalSetID, name: "validation eval set", data: inputs[3].data},
	} {
		var value evalset.EvalSet
		if err := json.Unmarshal(item.data, &value); err != nil {
			return nil, fmt.Errorf("decode %s: %w", item.name, err)
		}
		evalSets[item.id] = &value
	}
	var metrics []*metric.EvalMetric
	if err := json.Unmarshal(inputs[4].data, &metrics); err != nil {
		return nil, fmt.Errorf("decode metrics: %w", err)
	}
	catalog, err := regression.CatalogFromMetrics(metrics)
	if err != nil {
		return nil, fmt.Errorf("build attribution catalog: %w", err)
	}
	return &inputSnapshot{
		sha256:         fmt.Sprintf("%x", sha256.Sum256(combined)),
		baselinePrompt: baselinePrompt,
		evalSets:       evalSets,
		metrics:        metrics,
		catalog:        catalog,
	}, nil
}
