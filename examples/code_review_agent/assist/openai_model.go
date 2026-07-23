//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package assist

import (
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// LLMBackend selects the model used in --mode=llm.
const (
	LLMFake   = "fake"
	LLMOpenAI = "openai"
	LLMAuto   = "auto"
)

// OpenAIModelOptions configures an OpenAI-compatible chat model.
type OpenAIModelOptions struct {
	Model   string // e.g. gpt-4o-mini, deepseek-chat, qwen-flash
	BaseURL string // optional; falls back to OPENAI_BASE_URL / OPENAI_BASE_API
	APIKey  string // optional; falls back to OPENAI_API_KEY / DASHSCOPE_API_KEY
	Variant string // optional openai|deepseek|qwen|...
}

// ResolveModel picks a fake or real model for agent assist.
//
//	fake   — deterministic FakeModel (no network)
//	openai — OpenAI-compatible HTTP API (requires API key)
//	auto   — openai when an API key env is set, otherwise fake
func ResolveModel(backend string, opts OpenAIModelOptions) (model.Model, string, error) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" {
		backend = LLMFake
	}
	switch backend {
	case LLMFake:
		return NewFakeModel(), LLMFake, nil
	case LLMAuto:
		if resolveAPIKey(opts) == "" {
			return NewFakeModel(), LLMFake, nil
		}
		m, err := NewOpenAIModel(opts)
		if err != nil {
			return nil, "", err
		}
		return m, LLMOpenAI, nil
	case LLMOpenAI:
		m, err := NewOpenAIModel(opts)
		if err != nil {
			return nil, "", err
		}
		return m, LLMOpenAI, nil
	default:
		return nil, "", fmt.Errorf("unknown llm backend %q (want fake|openai|auto)", backend)
	}
}

// NewOpenAIModel builds an OpenAI-compatible model.Model.
func NewOpenAIModel(opts OpenAIModelOptions) (model.Model, error) {
	name := strings.TrimSpace(opts.Model)
	if name == "" {
		name = "gpt-4o-mini"
	}
	apiKey := resolveAPIKey(opts)
	if apiKey == "" {
		return nil, fmt.Errorf("API key required for --llm=openai (set OPENAI_API_KEY or DASHSCOPE_API_KEY; use OPENAI_BASE_URL for compatible gateways)")
	}
	baseURL := resolveBaseURL(opts)
	variant := normalizeVariant(opts.Variant, name, baseURL)

	var oopts []openai.Option
	oopts = append(oopts, openai.WithAPIKey(apiKey))
	if baseURL != "" {
		oopts = append(oopts, openai.WithBaseURL(baseURL))
	}
	if variant != "" {
		oopts = append(oopts, openai.WithVariant(openai.Variant(variant)))
	}
	return openai.New(name, oopts...), nil
}

// resolveAPIKey resolves the API key from options or environment variables.
func resolveAPIKey(opts OpenAIModelOptions) string {
	return strings.TrimSpace(firstNonEmpty(
		opts.APIKey,
		os.Getenv("OPENAI_API_KEY"),
		os.Getenv("DASHSCOPE_API_KEY"),
	))
}

// resolveBaseURL resolves the OpenAI-compatible base URL from options or env.
func resolveBaseURL(opts OpenAIModelOptions) string {
	// OPENAI_BASE_API is accepted as a common typo/alias for OPENAI_BASE_URL.
	return strings.TrimSpace(firstNonEmpty(
		opts.BaseURL,
		os.Getenv("OPENAI_BASE_URL"),
		os.Getenv("OPENAI_BASE_API"),
	))
}

// normalizeVariant maps model names / mistaken flags to framework variants.
// Example: --model-variant=qwen-flash → qwen.
func normalizeVariant(variant, modelName, baseURL string) string {
	v := strings.ToLower(strings.TrimSpace(variant))
	switch v {
	case "openai", "deepseek", "qwen", "hunyuan", "glm", "minimax", "kimi":
		return v
	case "qwen-flash", "qwen-plus", "qwen-max", "qwen-turbo":
		return string(openai.VariantQwen)
	case "deepseek-chat", "deepseek-reasoner", "deepseek-v4-flash":
		return string(openai.VariantDeepSeek)
	}
	if v != "" {
		// Unknown explicit variant: ignore and infer below.
		v = ""
	}
	lowerModel := strings.ToLower(modelName)
	lowerURL := strings.ToLower(baseURL)
	switch {
	case strings.HasPrefix(lowerModel, "qwen") || strings.Contains(lowerURL, "dashscope"):
		return string(openai.VariantQwen)
	case strings.HasPrefix(lowerModel, "deepseek") || strings.Contains(lowerURL, "deepseek"):
		return string(openai.VariantDeepSeek)
	default:
		return v
	}
}

// firstNonEmpty returns the first non-empty trimmed string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
