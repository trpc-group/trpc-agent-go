//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	workflowoptimizer "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func newOptimizer(cfg Config) (runner.Runner, workflowoptimizer.Optimizer, error) {
	instructionBytes, err := os.ReadFile(cfg.PromptOptimizerPromptPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read optimizer instruction: %w", err)
	}
	opts := make([]provider.Option, 0, 3)
	if strings.TrimSpace(cfg.OptimizerModel.APIKey) != "" {
		opts = append(opts, provider.WithAPIKey(cfg.OptimizerModel.APIKey))
	}
	if strings.TrimSpace(cfg.OptimizerModel.BaseURL) != "" {
		opts = append(opts, provider.WithBaseURL(cfg.OptimizerModel.BaseURL))
	}
	m, err := provider.Model(cfg.OptimizerModel.ProviderName, cfg.OptimizerModel.ModelName, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create optimizer model: %w", err)
	}
	gen := cfg.OptimizerModel.Generation
	gen.Stream = false
	ag := llmagent.New(
		"prompt_optimizer",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(gen),
	)
	r := runner.NewRunner("promptiter_optimizer", ag)
	o, err := workflowoptimizer.New(r, workflowoptimizer.WithRunOptions(agent.WithInstruction(string(instructionBytes))))
	if err != nil {
		return nil, nil, fmt.Errorf("create workflow optimizer: %w", err)
	}
	return r, o, nil
}
