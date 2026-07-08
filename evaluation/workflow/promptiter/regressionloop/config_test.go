//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	cfg := testConfig(t)
	require.NoError(t, cfg.Validate())

	cfg.TrainEvalSet.ID = "validation"
	require.ErrorContains(t, cfg.Validate(), "must be distinct")
}

func TestLoadConfigResolvesRelativePaths(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(t)
	cfg.PromptSource.Path = "baseline_prompt.txt"
	cfg.TrainEvalSet.Path = "train.evalset.json"
	cfg.ValidationEvalSet.Path = "validation.evalset.json"
	cfg.Metrics.Path = "metrics.json"
	cfg.Output.Dir = "output"
	cfg.Output.JSONReport = "optimization_report.json"
	cfg.Output.MarkdownReport = "optimization_report.md"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "baseline_prompt.txt"), []byte("prompt"), 0o644))
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	configPath := filepath.Join(dir, "promptiter.json")
	require.NoError(t, os.WriteFile(configPath, data, 0o644))

	loaded, err := LoadConfig(configPath)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "baseline_prompt.txt"), loaded.PromptSource.Path)
	assert.Equal(t, filepath.Join(dir, "output", "optimization_report.json"), loaded.Output.JSONReport)
}
