// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigRejectsUnknownField(t *testing.T) {
	path := writeTestConfig(t, strings.TrimSuffix(validTestConfig, "}")+",\"unknown\":true}")
	if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("loadConfig() error = %v, want unknown field", err)
	}
}

func TestLoadConfigRejectsTrailingJSON(t *testing.T) {
	path := writeTestConfig(t, validTestConfig+"{}")
	if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("loadConfig() error = %v, want trailing JSON error", err)
	}
}

func TestLoadConfigRejectsNegativeBudget(t *testing.T) {
	configJSON := strings.Replace(validTestConfig, "\"gate\":{}", "\"gate\":{\"maxValidationTokens\":-1}", 1)
	path := writeTestConfig(t, configJSON)
	if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "is negative") {
		t.Fatalf("loadConfig() error = %v, want negative budget error", err)
	}
}

func TestLoadConfigRejectsEmptyOutputDir(t *testing.T) {
	configJSON := strings.Replace(validTestConfig, `"outputDir":"output"`, `"outputDir":""`, 1)
	path := writeTestConfig(t, configJSON)
	if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "output directory is empty") {
		t.Fatalf("loadConfig() error = %v, want empty output directory error", err)
	}
}

func TestLoadConfigRejectsSharedTrainAndValidationEvalSet(t *testing.T) {
	configJSON := strings.Replace(validTestConfig, `"validationEvalSetID":"validation"`, `"validationEvalSetID":"train"`, 1)
	path := writeTestConfig(t, configJSON)
	if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "must be different") {
		t.Fatalf("loadConfig() error = %v, want distinct eval set error", err)
	}
}

func TestLoadConfigTrimsCandidatePrompts(t *testing.T) {
	configJSON := strings.Replace(validTestConfig, `"candidatePrompts":["candidate"]`, `"candidatePrompts":["  candidate  "]`, 1)
	path := writeTestConfig(t, configJSON)
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if got := cfg.CandidatePrompts[0]; got != "candidate" {
		t.Fatalf("candidate prompt = %q, want trimmed prompt", got)
	}
}

func TestLoadInputSnapshotFingerprintChangesForEveryDecisionInput(t *testing.T) {
	inputs := []string{
		"promptiter.json",
		"baseline.txt",
		"train.evalset.json",
		"validation.evalset.json",
		"metrics.json",
	}
	for _, name := range inputs {
		t.Run(name, func(t *testing.T) {
			cfg, dir := writeInputFixture(t)
			before, err := loadInputSnapshot(cfg)
			if err != nil {
				t.Fatalf("loadInputSnapshot() before mutation error = %v", err)
			}
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read input %q: %v", name, err)
			}
			if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
				t.Fatalf("mutate input %q: %v", name, err)
			}
			reloaded, err := loadConfig(filepath.Join(dir, "promptiter.json"))
			if err != nil {
				t.Fatalf("loadConfig() after mutation error = %v", err)
			}
			after, err := loadInputSnapshot(reloaded)
			if err != nil {
				t.Fatalf("loadInputSnapshot() after mutation error = %v", err)
			}
			if before.sha256 == after.sha256 {
				t.Fatalf("input fingerprint did not change after mutating %q", name)
			}
		})
	}
}

func writeInputFixture(t *testing.T) (*config, string) {
	t.Helper()
	// Keep the directory name different from appName to protect custom config
	// paths from accidentally falling back to the local manager's default layout.
	dir := filepath.Join(t.TempDir(), "custom-config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create input fixture directory: %v", err)
	}
	files := map[string]string{
		"promptiter.json":         validTestConfig,
		"baseline.txt":            "baseline",
		"train.evalset.json":      `{}`,
		"validation.evalset.json": `{}`,
		"metrics.json":            `[]`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write input fixture %q: %v", name, err)
		}
	}
	cfg, err := loadConfig(filepath.Join(dir, "promptiter.json"))
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	return cfg, dir
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "promptiter.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

const validTestConfig = `{
  "appName":"app",
  "trainEvalSetID":"train",
  "validationEvalSetID":"validation",
  "targetSurfaceID":"candidate#instruction",
  "candidatePrompts":["candidate"],
  "gate":{},
  "baselinePromptSource":"baseline.txt",
  "outputDir":"output"
}`
