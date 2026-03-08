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

func newTeacherRunner(cfg Config) (runner.Runner, error) {
	if strings.TrimSpace(cfg.TeacherModel.ProviderName) == "" {
		return nil, errors.New("teacher provider name is empty")
	}
	if strings.TrimSpace(cfg.TeacherModel.ModelName) == "" {
		return nil, errors.New("teacher model name is empty")
	}
	if strings.TrimSpace(cfg.TeacherPromptPath) == "" {
		return nil, errors.New("teacher prompt path is empty")
	}
	if strings.TrimSpace(cfg.SchemaPath) == "" {
		return nil, errors.New("schema path is empty")
	}
	instructionBytes, err := os.ReadFile(cfg.TeacherPromptPath)
	if err != nil {
		return nil, fmt.Errorf("read instruction: %w", err)
	}
	instruction := string(instructionBytes)
	if strings.TrimSpace(instruction) == "" {
		return nil, errors.New("teacher instruction is empty")
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
	if strings.TrimSpace(cfg.TeacherModel.APIKey) != "" {
		opts = append(opts, provider.WithAPIKey(cfg.TeacherModel.APIKey))
	}
	if strings.TrimSpace(cfg.TeacherModel.BaseURL) != "" {
		opts = append(opts, provider.WithBaseURL(cfg.TeacherModel.BaseURL))
	}
	m, err := provider.Model(cfg.TeacherModel.ProviderName, cfg.TeacherModel.ModelName, opts...)
	if err != nil {
		return nil, fmt.Errorf("create model: %w", err)
	}
	gen := cfg.TeacherModel.Generation
	gen.Stream = false
	ag := llmagent.New(
		"teacher",
		llmagent.WithModel(m),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithStructuredOutputJSONSchema("sportscaster_output", outputSchema, true, "Sportscaster output."),
	)
	return runner.NewRunner("promptiter_teacher", ag), nil
}
