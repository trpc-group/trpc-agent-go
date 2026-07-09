//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Command promptiter_regression_loop runs the evaluation + optimization
// regression pipeline: baseline evaluation, failure attribution, PromptIter
// optimization, per-case regression, acceptance gating, and audit reporting.
//
// A gate rejection is a normal business outcome and exits with code 0; only
// pipeline execution errors exit non-zero.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	dataDir    = flag.String("data-dir", "./data", "Directory containing evalset, metric, and prompt source files")
	outputDir  = flag.String("output-dir", "./output", "Directory receiving reports, audit files, and the candidate prompt")
	configPath = flag.String("config", "", "Pipeline configuration file (default <data-dir>/promptiter-regression-app/promptiter.json)")
	mode       = flag.String("mode", "fake", "Model sourcing mode: fake (deterministic, no API key) or real (OPENAI_API_KEY)")
	writeBack  = flag.Bool("write-back", false, "Overwrite the baseline prompt source on acceptance instead of only emitting output/candidate_prompt.txt")
)

// Default real-mode model names.
const (
	defaultRealCandidateModel = "deepseek-v3.2"
	defaultRealWorkerModel    = "gpt-5.2"
)

func main() {
	flag.Parse()
	logger := log.New(os.Stderr, "", log.LstdFlags)
	if err := run(context.Background(), logger); err != nil {
		logger.Fatalf("pipeline error: %v", err)
	}
}

func run(ctx context.Context, logger *log.Logger) error {
	pipelineMode, err := parseMode(*mode)
	if err != nil {
		return err
	}
	// The config file lives inside the data dir by default so that -data-dir
	// alone relocates every input, including the prompt source and write-back
	// target resolved relative to the config file.
	path := *configPath
	if path == "" {
		path = filepath.Join(*dataDir, "promptiter-regression-app", "promptiter.json")
	}
	config, err := LoadConfig(path)
	if err != nil {
		return err
	}
	inputs, err := resolveInputs(*dataDir, config)
	if err != nil {
		return err
	}
	components, closeComponents, err := buildComponents(ctx, pipelineMode, inputs.baselinePrompt)
	if err != nil {
		return err
	}
	defer closeComponents()
	result, err := runPipeline(ctx, Options{
		Config:     config,
		Inputs:     inputs,
		OutputDir:  *outputDir,
		DataDir:    *dataDir,
		Mode:       pipelineMode,
		WriteBack:  *writeBack,
		Components: components,
		Logger:     logger,
	})
	if err != nil {
		return err
	}
	logger.Printf("pipeline finished: status=%s %s", result.Status, result.Message)
	return nil
}

func parseMode(value string) (Mode, error) {
	switch Mode(strings.TrimSpace(value)) {
	case ModeFake:
		return ModeFake, nil
	case ModeReal:
		if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
			return "", fmt.Errorf("mode real requires OPENAI_API_KEY; use -mode fake to run without credentials")
		}
		return ModeReal, nil
	default:
		return "", fmt.Errorf("mode %q is not supported, expected fake or real", value)
	}
}

// buildComponents assembles mode-specific pipeline collaborators. The
// returned closer releases worker runners; it is safe to call once.
func buildComponents(ctx context.Context, pipelineMode Mode, baselinePrompt string) (Components, func(), error) {
	if pipelineMode == ModeFake {
		return buildFakeComponents(baselinePrompt)
	}
	return buildRealComponents(ctx, baselinePrompt)
}

// buildFakeComponents wires the deterministic scripted runtime: scripted
// candidate model plus workers implementing the PromptIter interfaces
// directly, so no API key or network access is required.
func buildFakeComponents(baselinePrompt string) (Components, func(), error) {
	components := Components{
		CandidateAgent: NewAgent(NewModel(""), baselinePrompt),
		Backwarder:     NewBackwarder(),
		Aggregator:     NewAggregator(),
		Optimizer:      NewOptimizer(),
		ModelInfo: map[string]string{
			"candidate":  "fake scripted order assistant",
			"backwarder": "fake deterministic backwarder",
			"aggregator": "fake pass-through aggregator",
			"optimizer":  "fake marker-append optimizer",
		},
	}
	return components, func() {}, nil
}

// buildRealComponents wires OpenAI-compatible models for the candidate and
// the PromptIter worker stages.
func buildRealComponents(ctx context.Context, baselinePrompt string) (Components, func(), error) {
	candidateModel, err := loadOpenAIModel(defaultRealCandidateModel)
	if err != nil {
		return Components{}, nil, fmt.Errorf("load candidate model: %w", err)
	}
	workerModel, err := loadOpenAIModel(defaultRealWorkerModel)
	if err != nil {
		return Components{}, nil, fmt.Errorf("load worker model: %w", err)
	}
	backwarderRunner := runner.NewRunner("promptiter-backwarder", newWorkerAgent("promptiter-backwarder", workerModel))
	aggregatorRunner := runner.NewRunner("promptiter-aggregator", newWorkerAgent("promptiter-aggregator", workerModel))
	optimizerRunner := runner.NewRunner("promptiter-optimizer", newWorkerAgent("promptiter-optimizer", workerModel))
	judgeRunner := runner.NewRunner("promptiter-judge", newWorkerAgent("promptiter-judge", workerModel))
	closer := func() {
		backwarderRunner.Close()
		aggregatorRunner.Close()
		optimizerRunner.Close()
		judgeRunner.Close()
	}
	backwarderInstance, err := backwarder.New(ctx, backwarderRunner)
	if err != nil {
		closer()
		return Components{}, nil, fmt.Errorf("create backwarder: %w", err)
	}
	aggregatorInstance, err := aggregator.New(ctx, aggregatorRunner)
	if err != nil {
		closer()
		return Components{}, nil, fmt.Errorf("create aggregator: %w", err)
	}
	optimizerInstance, err := optimizer.New(ctx, optimizerRunner)
	if err != nil {
		closer()
		return Components{}, nil, fmt.Errorf("create optimizer: %w", err)
	}
	components := Components{
		CandidateAgent: NewAgent(candidateModel, baselinePrompt),
		Backwarder:     backwarderInstance,
		Aggregator:     aggregatorInstance,
		Optimizer:      optimizerInstance,
		Judge:          judgeRunner,
		ModelInfo: map[string]string{
			"candidate": defaultRealCandidateModel,
			"worker":    defaultRealWorkerModel,
		},
	}
	return components, closer, nil
}

func newWorkerAgent(name string, workerModel model.Model) agent.Agent {
	maxTokens := 32768
	temperature := 0.0
	return llmagent.New(
		name,
		llmagent.WithModel(workerModel),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Temperature: &temperature,
			Stream:      false,
		}),
	)
}

func loadOpenAIModel(modelName string) (model.Model, error) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	switch {
	case modelName == "":
		return nil, errors.New("model name is empty")
	case apiKey == "":
		return nil, errors.New("OPENAI_API_KEY is empty")
	}
	options := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL != "" {
		options = append(options, openai.WithBaseURL(baseURL))
	}
	return openai.New(modelName, options...), nil
}
