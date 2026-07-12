// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package regressionloop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPipelineRun(t *testing.T) {
	tmpDir := t.TempDir()

	config := &Config{
		TrainEvalSetPath:      "train.evalset.json",
		ValidationEvalSetPath: "validation.evalset.json",
		MetricsPath:           "metrics.json",
		BaselinePromptPath:    "baseline_prompt.txt",
		PromptiterConfigPath:  "promptiter.json",
		Seed:                  12345,
		Mode:                  "fake",
		Gate: GateConfig{
			MinValidationGain:   0.05,
			AllowNewHardFail:    false,
			MaxNewHardFailCount: 0,
			MaxRegressedCases:   10,
		},
		Optimization: OptimizationConfig{
			MaxRounds:        5,
			TargetSurfaceIDs: []string{"surface1"},
			MinScoreGain:     0.01,
		},
		Output: OutputConfig{
			OutputDir: tmpDir,
		},
	}

	pipeline := NewPipeline(config)
	report, err := pipeline.Run(context.Background())

	assert.NoError(t, err)
	assert.NotNil(t, report)
	assert.Equal(t, "fake", report.RunMeta.Mode)
	assert.Equal(t, int64(12345), report.RunMeta.Seed)
	assert.Greater(t, report.RunMeta.DurationMS, int64(0))
	assert.Equal(t, GateResultAccept, report.GateDecision.Result)

	jsonPath := filepath.Join(tmpDir, "optimization_report.json")
	mdPath := filepath.Join(tmpDir, "optimization_report.md")
	assert.FileExists(t, jsonPath)
	assert.FileExists(t, mdPath)
}

func TestPipelineRunWithOverfit(t *testing.T) {
	tmpDir := t.TempDir()

	config := &Config{
		TrainEvalSetPath:      "train.evalset.json",
		ValidationEvalSetPath: "validation.evalset.json",
		MetricsPath:           "metrics.json",
		BaselinePromptPath:    "baseline_prompt.txt",
		PromptiterConfigPath:  "promptiter.json",
		Seed:                  12345,
		Mode:                  "fake",
		Gate: GateConfig{
			MinValidationGain:   0.05,
			AllowNewHardFail:    false,
			MaxNewHardFailCount: 0,
			MaxRegressedCases:   0,
			CriticalCaseIDs:     []string{"critical_case"},
		},
		Optimization: OptimizationConfig{
			MaxRounds:        5,
			TargetSurfaceIDs: []string{"surface1"},
			MinScoreGain:     0.01,
		},
		Output: OutputConfig{
			OutputDir: tmpDir,
		},
	}

	pipeline := NewPipeline(config)
	report, err := pipeline.Run(context.Background())

	assert.NoError(t, err)
	assert.NotNil(t, report)
}

func TestPipelineNilConfig(t *testing.T) {
	assert.Panics(t, func() {
		pipeline := NewPipeline(nil)
		_, _ = pipeline.Run(context.Background())
	})
}

func TestPipelineOutputDirectoryCreation(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "test_pipeline_output")
	os.RemoveAll(tmpDir)

	config := &Config{
		TrainEvalSetPath:      "train.evalset.json",
		ValidationEvalSetPath: "validation.evalset.json",
		MetricsPath:           "metrics.json",
		BaselinePromptPath:    "baseline_prompt.txt",
		PromptiterConfigPath:  "promptiter.json",
		Seed:                  12345,
		Mode:                  "fake",
		Gate: GateConfig{
			MinValidationGain: 0.05,
		},
		Optimization: OptimizationConfig{
			MaxRounds:        5,
			TargetSurfaceIDs: []string{"surface1"},
			MinScoreGain:     0.01,
		},
		Output: OutputConfig{
			OutputDir: tmpDir,
		},
	}

	pipeline := NewPipeline(config)
	_, err := pipeline.Run(context.Background())

	assert.NoError(t, err)
	assert.DirExists(t, tmpDir)
}
