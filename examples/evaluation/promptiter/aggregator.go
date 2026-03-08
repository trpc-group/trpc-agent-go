//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	workflowaggregator "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func newAggregator(cfg Config) (runner.Runner, workflowaggregator.Aggregator, error) {
	instructionBytes, err := os.ReadFile(cfg.GradientAggregatorPromptPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read aggregator instruction: %w", err)
	}
	schemaBytes, err := os.ReadFile(cfg.AggregatedGradientSchemaPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read aggregator output schema: %w", err)
	}
	var outputSchema map[string]any
	if err := json.Unmarshal(schemaBytes, &outputSchema); err != nil {
		return nil, nil, fmt.Errorf("unmarshal aggregator output schema: %w", err)
	}
	opts := make([]provider.Option, 0, 3)
	if strings.TrimSpace(cfg.AggregatorModel.APIKey) != "" {
		opts = append(opts, provider.WithAPIKey(cfg.AggregatorModel.APIKey))
	}
	if strings.TrimSpace(cfg.AggregatorModel.BaseURL) != "" {
		opts = append(opts, provider.WithBaseURL(cfg.AggregatorModel.BaseURL))
	}
	m, err := provider.Model(cfg.AggregatorModel.ProviderName, cfg.AggregatorModel.ModelName, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create aggregator model: %w", err)
	}
	gen := cfg.AggregatorModel.Generation
	gen.Stream = false
	ag := llmagent.New(
		"gradient_aggregator",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithStructuredOutputJSONSchema("aggregated_gradient", outputSchema, true, "Aggregated prompt gradient."),
	)
	r := runner.NewRunner("promptiter_aggregator", ag)
	a, err := workflowaggregator.New(r, workflowaggregator.WithRunOptions(agent.WithInstruction(string(instructionBytes))))
	if err != nil {
		return nil, nil, fmt.Errorf("create workflow aggregator: %w", err)
	}
	return r, a, nil
}
