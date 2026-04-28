//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func newLLMModel(name string, baseURL string, apiKey string) model.Model {
	return openai.New(name, buildOpenAIOptions(baseURL, apiKey)...)
}

func newGenerationConfig(stream bool) model.GenerationConfig {
	return model.GenerationConfig{
		MaxTokens:   intPtr(2048),
		Temperature: floatPtr(0.2),
		Stream:      stream,
	}
}

func buildOpenAIOptions(baseURL string, apiKey string) []openai.Option {
	opts := []openai.Option{openai.WithShowToolCallDelta(true)}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey != "" {
		opts = append(opts, openai.WithAPIKey(apiKey))
	}
	return opts
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
