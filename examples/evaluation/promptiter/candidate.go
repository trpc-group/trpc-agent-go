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
	"errors"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func newCandidateRunner(cfg Config) (runner.Runner, error) {
	if strings.TrimSpace(cfg.AppName) == "" {
		return nil, errors.New("app name is empty")
	}
	if strings.TrimSpace(cfg.CandidateModel.ProviderName) == "" {
		return nil, errors.New("candidate provider name is empty")
	}
	if strings.TrimSpace(cfg.CandidateModel.ModelName) == "" {
		return nil, errors.New("candidate model name is empty")
	}
	if strings.TrimSpace(cfg.SchemaPath) == "" {
		return nil, errors.New("schema path is empty")
	}
	schemaBytes, err := os.ReadFile(cfg.SchemaPath)
	if err != nil {
		return nil, fmt.Errorf("read output schema: %w", err)
	}
	var outputSchema map[string]any
	if err := json.Unmarshal(schemaBytes, &outputSchema); err != nil {
		return nil, fmt.Errorf("unmarshal output schema: %w", err)
	}
	opts := make([]provider.Option, 0, 3)
	if strings.TrimSpace(cfg.CandidateModel.APIKey) != "" {
		opts = append(opts, provider.WithAPIKey(cfg.CandidateModel.APIKey))
	}
	if strings.TrimSpace(cfg.CandidateModel.BaseURL) != "" {
		opts = append(opts, provider.WithBaseURL(cfg.CandidateModel.BaseURL))
	}
	m, err := provider.Model(cfg.CandidateModel.ProviderName, cfg.CandidateModel.ModelName, opts...)
	if err != nil {
		return nil, fmt.Errorf("create model: %w", err)
	}
	gen := cfg.CandidateModel.Generation
	gen.Stream = false
	ag := llmagent.New(
		"candidate",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithStructuredOutputJSONSchema("sportscaster_output", outputSchema, true, "Sportscaster output."),
	)
	return runner.NewRunner(cfg.AppName, ag), nil
}
