//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: Config{
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
					MaxRegressedCases:   1,
					ProtectedCaseIDs:    []string{"case1"},
					CriticalCaseIDs:     []string{"case2"},
					MaxCost:             100.0,
					MaxCalls:            1000,
					MaxLatencyMS:        300000,
				},
				Optimization: OptimizationConfig{
					MaxRounds:        5,
					TargetSurfaceIDs: []string{"surface1"},
					MinScoreGain:     0.01,
					CaseParallelism:  4,
				},
				Output: OutputConfig{
					OutputDir:           "output",
					SaveAuditTrail:      true,
					SaveCandidatePrompt: true,
				},
			},
			wantErr: false,
		},
		{
			name: "missing trainEvalSetPath",
			config: Config{
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
			},
			wantErr: true,
		},
		{
			name: "missing validationEvalSetPath",
			config: Config{
				TrainEvalSetPath:     "train.evalset.json",
				MetricsPath:          "metrics.json",
				BaselinePromptPath:   "baseline_prompt.txt",
				PromptiterConfigPath: "promptiter.json",
				Seed:                 12345,
				Mode:                 "fake",
				Gate: GateConfig{
					MinValidationGain: 0.05,
				},
				Optimization: OptimizationConfig{
					MaxRounds:        5,
					TargetSurfaceIDs: []string{"surface1"},
					MinScoreGain:     0.01,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid mode",
			config: Config{
				TrainEvalSetPath:      "train.evalset.json",
				ValidationEvalSetPath: "validation.evalset.json",
				MetricsPath:           "metrics.json",
				BaselinePromptPath:    "baseline_prompt.txt",
				PromptiterConfigPath:  "promptiter.json",
				Seed:                  12345,
				Mode:                  "invalid",
				Gate: GateConfig{
					MinValidationGain: 0.05,
				},
				Optimization: OptimizationConfig{
					MaxRounds:        5,
					TargetSurfaceIDs: []string{"surface1"},
					MinScoreGain:     0.01,
				},
			},
			wantErr: true,
		},
		{
			name: "zero seed in fake mode",
			config: Config{
				TrainEvalSetPath:      "train.evalset.json",
				ValidationEvalSetPath: "validation.evalset.json",
				MetricsPath:           "metrics.json",
				BaselinePromptPath:    "baseline_prompt.txt",
				PromptiterConfigPath:  "promptiter.json",
				Seed:                  0,
				Mode:                  "fake",
				Gate: GateConfig{
					MinValidationGain: 0.05,
				},
				Optimization: OptimizationConfig{
					MaxRounds:        5,
					TargetSurfaceIDs: []string{"surface1"},
					MinScoreGain:     0.01,
				},
			},
			wantErr: true,
		},
		{
			name: "negative minValidationGain",
			config: Config{
				TrainEvalSetPath:      "train.evalset.json",
				ValidationEvalSetPath: "validation.evalset.json",
				MetricsPath:           "metrics.json",
				BaselinePromptPath:    "baseline_prompt.txt",
				PromptiterConfigPath:  "promptiter.json",
				Seed:                  12345,
				Mode:                  "fake",
				Gate: GateConfig{
					MinValidationGain: -0.05,
				},
				Optimization: OptimizationConfig{
					MaxRounds:        5,
					TargetSurfaceIDs: []string{"surface1"},
					MinScoreGain:     0.01,
				},
			},
			wantErr: true,
		},
		{
			name: "zero maxRounds",
			config: Config{
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
					MaxRounds:        0,
					TargetSurfaceIDs: []string{"surface1"},
					MinScoreGain:     0.01,
				},
			},
			wantErr: true,
		},
		{
			name: "empty targetSurfaceIDs",
			config: Config{
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
					TargetSurfaceIDs: []string{},
					MinScoreGain:     0.01,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestConfigResolvePaths(t *testing.T) {
	baseDir := "/test/base"
	config := &Config{
		TrainEvalSetPath:      "train.evalset.json",
		ValidationEvalSetPath: "../validation.evalset.json",
		MetricsPath:           "/absolute/metrics.json",
		BaselinePromptPath:    "baseline_prompt.txt",
		PromptiterConfigPath:  "config/promptiter.json",
		Output: OutputConfig{
			OutputDir: "output",
		},
	}

	err := config.ResolvePaths(baseDir)
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(baseDir, "train.evalset.json"), config.TrainEvalSetPath)
	assert.Equal(t, filepath.Join(baseDir, "../validation.evalset.json"), config.ValidationEvalSetPath)
	assert.Equal(t, "/absolute/metrics.json", config.MetricsPath)
	assert.Equal(t, filepath.Join(baseDir, "baseline_prompt.txt"), config.BaselinePromptPath)
	assert.Equal(t, filepath.Join(baseDir, "config/promptiter.json"), config.PromptiterConfigPath)
	assert.Equal(t, filepath.Join(baseDir, "output"), config.Output.OutputDir)
}

func TestConfigHash(t *testing.T) {
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
	}

	hash1, err := config.Hash()
	assert.NoError(t, err)
	assert.NotEmpty(t, hash1)

	hash2, err := config.Hash()
	assert.NoError(t, err)
	assert.Equal(t, hash1, hash2)

	config.Seed = 54321
	hash3, err := config.Hash()
	assert.NoError(t, err)
	assert.NotEqual(t, hash1, hash3)
}

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	trainPath := filepath.Join(tmpDir, "train.evalset.json")
	validationPath := filepath.Join(tmpDir, "validation.evalset.json")
	metricsPath := filepath.Join(tmpDir, "metrics.json")
	promptPath := filepath.Join(tmpDir, "baseline_prompt.txt")
	promptiterPath := filepath.Join(tmpDir, "promptiter.json")

	os.WriteFile(trainPath, []byte("{}"), 0644)
	os.WriteFile(validationPath, []byte("{}"), 0644)
	os.WriteFile(metricsPath, []byte("{}"), 0644)
	os.WriteFile(promptPath, []byte("baseline"), 0644)
	os.WriteFile(promptiterPath, []byte("{}"), 0644)

	configContent := `{
		"trainEvalSetPath": "train.evalset.json",
		"validationEvalSetPath": "validation.evalset.json",
		"metricsPath": "metrics.json",
		"baselinePromptPath": "baseline_prompt.txt",
		"promptiterConfigPath": "promptiter.json",
		"seed": 12345,
		"mode": "fake",
		"gate": {
			"minValidationGain": 0.05
		},
		"optimization": {
			"maxRounds": 5,
			"targetSurfaceIds": ["surface1"],
			"minScoreGain": 0.01
		},
		"output": {
			"outputDir": "output"
		}
	}`
	os.WriteFile(configPath, []byte(configContent), 0644)

	config, err := LoadConfig(configPath)
	assert.NoError(t, err)
	assert.Equal(t, trainPath, config.TrainEvalSetPath)
	assert.Equal(t, validationPath, config.ValidationEvalSetPath)
	assert.Equal(t, metricsPath, config.MetricsPath)
	assert.Equal(t, promptPath, config.BaselinePromptPath)
	assert.Equal(t, promptiterPath, config.PromptiterConfigPath)
	assert.Equal(t, filepath.Join(tmpDir, "output"), config.Output.OutputDir)
}
