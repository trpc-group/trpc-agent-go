//
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
	"testing"
)

// writeConfig writes a minimal valid config with the given gate JSON and returns
// the data dir.
func writeConfig(t *testing.T, gateJSON string) string {
	t.Helper()
	dir := t.TempDir()
	appDir := filepath.Join(dir, appName)
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := `{"maxRounds":2,"minScoreGain":0.5,"targetScore":1.0,"maxRoundsWithoutAcceptance":2,"gate":` + gateJSON + `}`
	if err := os.WriteFile(filepath.Join(appDir, "promptiter.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "baseline.instruction.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write instruction: %v", err)
	}
	return dir
}

func TestLoadConfigRejectsNegativeBudgets(t *testing.T) {
	if _, err := loadLoopConfig(writeConfig(t, `{"minTotalGain":0.5,"maxModelCalls":-1}`)); err == nil {
		t.Fatalf("negative gate.maxModelCalls must error")
	}
	if _, err := loadLoopConfig(writeConfig(t, `{"minTotalGain":0.5,"maxRounds":-1}`)); err == nil {
		t.Fatalf("negative gate.maxRounds must error")
	}
}

func TestLoadConfigAllowsDisabledBudgets(t *testing.T) {
	if _, err := loadLoopConfig(writeConfig(t, `{"minTotalGain":0.5,"maxModelCalls":0,"maxRounds":0}`)); err != nil {
		t.Fatalf("zero (disabled) budgets must be allowed: %v", err)
	}
}
