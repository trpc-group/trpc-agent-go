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
