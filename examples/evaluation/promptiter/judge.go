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

func newJudgeRunner(cfg Config) (runner.Runner, error) {
	if strings.TrimSpace(cfg.JudgeModel.ProviderName) == "" {
		return nil, errors.New("judge provider name is empty")
	}
	if strings.TrimSpace(cfg.JudgeModel.ModelName) == "" {
		return nil, errors.New("judge model name is empty")
	}
	schemaDef, err := loadJSONSchemaMap(cfg.JudgeOutputSchemaPath)
	if err != nil {
		return nil, err
	}
	opts := make([]provider.Option, 0, 2)
	if strings.TrimSpace(cfg.JudgeModel.APIKey) != "" {
		opts = append(opts, provider.WithAPIKey(cfg.JudgeModel.APIKey))
	}
	if strings.TrimSpace(cfg.JudgeModel.BaseURL) != "" {
		opts = append(opts, provider.WithBaseURL(cfg.JudgeModel.BaseURL))
	}
	m, err := provider.Model(cfg.JudgeModel.ProviderName, cfg.JudgeModel.ModelName, opts...)
	if err != nil {
		return nil, fmt.Errorf("create judge model: %w", err)
	}
	gen := cfg.JudgeModel.Generation
	gen.Stream = false
	ag := llmagent.New(
		"llm_judge",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithStructuredOutputJSONSchema("llmcritic_output", schemaDef, true, "Judge output."),
	)
	return runner.NewRunner("promptiter_judge", ag), nil
}

func loadJSONSchemaMap(path string) (map[string]any, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("judge output schema path is empty")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read judge output schema: %w", err)
	}
	var schemaDef map[string]any
	if err := json.Unmarshal(b, &schemaDef); err != nil {
		return nil, fmt.Errorf("unmarshal judge output schema: %w", err)
	}
	if len(schemaDef) == 0 {
		return nil, errors.New("judge output schema is empty")
	}
	return schemaDef, nil
}
