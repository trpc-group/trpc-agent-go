//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package config

import (
	"math"
	"path/filepath"
	"testing"
)

func TestLoadSampleConfig(t *testing.T) {
	path := filepath.Join("..", "..", "data", "promptiter-recap-app", "promptiter.json")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Seed != 42 || cfg.Optimization.MaxRounds != 3 || cfg.Prompt.WriteBack {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestValidateRejectsInvalidConfigurations(t *testing.T) {
	path := filepath.Join("..", "..", "data", "promptiter-recap-app", "promptiter.json")
	baseDir := filepath.Dir(path)
	load := func(t *testing.T) *Config {
		t.Helper()
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"same sets", func(c *Config) { c.Evaluation.ValidationEvalSetID = c.Evaluation.TrainEvalSetID }},
		{"rounds", func(c *Config) { c.Optimization.MaxRounds = 0 }},
		{"nan", func(c *Config) { c.Optimization.MinScoreGain = math.NaN() }},
		{"negative counts", func(c *Config) { c.Gate.MaxNewHardFailures = -1 }},
		{"nan p0 drop", func(c *Config) { c.Gate.CriticalCases[0].MaxScoreDrop = math.NaN() }},
		{"empty target", func(c *Config) { c.Prompt.TargetSurfaceIDs = nil }},
		{"missing p0", func(c *Config) { c.Gate.CriticalCases[0].CaseID = "missing" }},
		{"output covers input", func(c *Config) { c.Audit.OutputDir = "." }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := load(t)
			test.mutate(cfg)
			if err := cfg.Validate(baseDir); err == nil {
				t.Fatal("Validate() returned nil error")
			}
		})
	}
}
