//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ModelConfig defines a provider-backed model configuration.
type ModelConfig struct {
	// ProviderName is the provider registry name used by provider.Model.
	ProviderName string
	// ModelName is the model identifier passed to the provider.
	ModelName string
	// BaseURL is the optional OpenAI-compatible endpoint base URL.
	BaseURL string
	// APIKey is the API key used by the provider.
	APIKey string
	// Generation controls sampling and token limits for the model.
	Generation model.GenerationConfig
}

// Config defines the prompt iteration loop configuration.
type Config struct {
	// AppName is the evaluation app name used to locate evalsets and metrics.
	AppName string
	// EvalSetIDs specifies the eval sets to run; when empty, all eval sets under the app are used.
	EvalSetIDs []string
	// DataDir is the directory containing evalset and metrics files.
	DataDir string
	// OutputDir is the base directory used for round artifacts.
	OutputDir string
	// SchemaPath is the output JSON schema file path.
	SchemaPath string
	// JudgeOutputSchemaPath is the JSON schema used for the judge structured output.
	JudgeOutputSchemaPath string
	// AggregatedGradientSchemaPath is the JSON schema used for the aggregated gradient output.
	AggregatedGradientSchemaPath string
	// TargetPromptPath is the initial prompt to optimize (v1_0).
	TargetPromptPath string
	// TeacherPromptPath is fixed and does not change across iterations.
	TeacherPromptPath string
	// JudgePromptPath is a Go template used by llm_critic.
	JudgePromptPath string
	// GradientAggregatorPromptPath is an instruction used by the gradient aggregator agent.
	GradientAggregatorPromptPath string
	// PromptOptimizerPromptPath is the instruction used by the optimizer agent.
	PromptOptimizerPromptPath string
	// CandidateModel is the model configuration used for candidate inference.
	CandidateModel ModelConfig
	// TeacherModel is the model configuration used for teacher reference outputs.
	TeacherModel ModelConfig
	// JudgeModel is the model configuration used for LLM judge scoring.
	JudgeModel ModelConfig
	// OptimizerModel is the model configuration used for prompt optimization.
	OptimizerModel ModelConfig
	// AggregatorModel is the model configuration used for gradient aggregation.
	AggregatorModel ModelConfig
	// MaxIters is the maximum number of iteration rounds.
	MaxIters int
}

// DefaultConfig returns a ready-to-run default configuration.
func DefaultConfig() Config {
	basePrompts := filepath.Join(".", "prompt")
	return Config{
		AppName:                      "sportscaster_eval_app",
		DataDir:                      filepath.Join(".", "data"),
		OutputDir:                    filepath.Join(".", "output"),
		SchemaPath:                   filepath.Join(".", "schema", "candidate.json"),
		JudgeOutputSchemaPath:        filepath.Join(".", "schema", "llmcritic.json"),
		AggregatedGradientSchemaPath: filepath.Join(".", "schema", "aggregator.json"),
		TargetPromptPath:             filepath.Join(basePrompts, "candidate.md"),
		TeacherPromptPath:            filepath.Join(basePrompts, "teacher.md"),
		JudgePromptPath:              filepath.Join(basePrompts, "llmcritic.md"),
		GradientAggregatorPromptPath: filepath.Join(basePrompts, "aggregator.md"),
		PromptOptimizerPromptPath:    filepath.Join(basePrompts, "optimizer.md"),
		CandidateModel: ModelConfig{
			ProviderName: "openai",
			ModelName:    "deepseek-v3.2",
			Generation: model.GenerationConfig{
				MaxTokens:   intPtr(32768),
				Temperature: floatPtr(1.0),
				Stream:      false,
			},
		},
		TeacherModel: ModelConfig{
			ProviderName: "openai",
			ModelName:    "gpt-5.2",
			Generation: model.GenerationConfig{
				MaxTokens:       intPtr(32768),
				Temperature:     floatPtr(1.0),
				ReasoningEffort: stringPtr("high"),
				Stream:          false,
			},
		},
		JudgeModel: ModelConfig{
			ProviderName: "openai",
			ModelName:    "gpt-5.2",
			Generation: model.GenerationConfig{
				MaxTokens:       intPtr(32768),
				Temperature:     floatPtr(1.0),
				ReasoningEffort: stringPtr("high"),
				Stream:          false,
			},
		},
		OptimizerModel: ModelConfig{
			ProviderName: "openai",
			ModelName:    "gpt-5.2",
			Generation: model.GenerationConfig{
				MaxTokens:       intPtr(32768),
				Temperature:     floatPtr(1.0),
				ReasoningEffort: stringPtr("high"),
				Stream:          false,
			},
		},
		AggregatorModel: ModelConfig{
			ProviderName: "openai",
			ModelName:    "gpt-5.2",
			Generation: model.GenerationConfig{
				MaxTokens:       intPtr(32768),
				Temperature:     floatPtr(1.0),
				ReasoningEffort: stringPtr("high"),
				Stream:          false,
			},
		},
		MaxIters: 3,
	}
}

// Validate returns an error if the config is incomplete.
func (c Config) Validate() error {
	if c.AppName == "" {
		return errors.New("app name is empty")
	}
	if c.DataDir == "" {
		return errors.New("data dir is empty")
	}
	if c.OutputDir == "" {
		return errors.New("output dir is empty")
	}
	if c.SchemaPath == "" {
		return errors.New("schema path is empty")
	}
	if c.JudgeOutputSchemaPath == "" {
		return errors.New("judge output schema path is empty")
	}
	if c.AggregatedGradientSchemaPath == "" {
		return errors.New("aggregated gradient schema path is empty")
	}
	if c.TargetPromptPath == "" {
		return errors.New("target prompt path is empty")
	}
	if c.TeacherPromptPath == "" {
		return errors.New("teacher prompt path is empty")
	}
	if c.JudgePromptPath == "" {
		return errors.New("judge prompt path is empty")
	}
	if c.GradientAggregatorPromptPath == "" {
		return errors.New("gradient aggregator prompt path is empty")
	}
	if c.PromptOptimizerPromptPath == "" {
		return errors.New("prompt optimizer prompt path is empty")
	}
	if c.MaxIters <= 0 {
		return fmt.Errorf("max iters must be greater than 0: %d", c.MaxIters)
	}
	return nil
}

func intPtr(v int) *int { return &v }

func floatPtr(v float64) *float64 { return &v }

func stringPtr(v string) *string { return &v }
