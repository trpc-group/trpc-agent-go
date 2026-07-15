//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llm

import (
	"errors"
	"fmt"
	"os"
	"strings"

	agentmodel "trpc.group/trpc-go/trpc-agent-go/model"
	officialopenai "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

const (
	ProviderOpenAI           = "openai"
	ProviderOpenAICompatible = "openai-compatible"
	ProviderDeepSeek         = "deepseek"
	DefaultOpenAIAPIKeyEnv   = "OPENAI_API_KEY"
	DefaultDeepSeekAPIKeyEnv = "DEEPSEEK_API_KEY"
	DefaultDeepSeekModel     = "deepseek-chat"
)

// OpenAIConfig controls the official OpenAI-compatible model provider.
type OpenAIConfig struct {
	Enabled   bool
	Provider  string
	Model     string
	APIKey    string
	APIKeyEnv string
	BaseURL   string
	Variant   string
}

// NewOpenAIReviewProvider creates an official-model-backed review provider.
func NewOpenAIReviewProvider(cfg OpenAIConfig) (Provider, error) {
	model, err := NewOpenAIModel(cfg)
	if err != nil {
		return nil, err
	}
	return OfficialProvider{Model: model}, nil
}

// OpenAIModelAudit returns non-sensitive provider audit fields.
func OpenAIModelAudit(cfg OpenAIConfig) Audit {
	provider := strings.TrimSpace(cfg.Provider)
	if provider == "" {
		provider = ProviderOpenAI
	}
	name := strings.TrimSpace(cfg.Model)
	if name == "" && provider == ProviderDeepSeek {
		name = DefaultDeepSeekModel
	}
	return Audit{
		Provider: provider,
		Name:     name,
		Backend:  BackendOpenAI,
	}
}

// NewOpenAIModel builds the official trpc-agent-go/model/openai model.
func NewOpenAIModel(cfg OpenAIConfig) (agentmodel.Model, error) {
	provider := strings.TrimSpace(cfg.Provider)
	if provider == "" {
		provider = ProviderOpenAI
	}
	modelName := strings.TrimSpace(cfg.Model)
	if modelName == "" && provider == ProviderDeepSeek {
		modelName = DefaultDeepSeekModel
	}
	if modelName == "" {
		return nil, fmt.Errorf("model name is required for %s provider", provider)
	}
	apiKeyEnv := ModelAPIKeyEnv(cfg)
	apiKey := ModelAPIKey(cfg, apiKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("model provider %s requires API key", provider)
	}
	var opts []officialopenai.Option
	opts = append(opts, officialopenai.WithAPIKey(apiKey))
	if baseURL := OpenAIModelBaseURL(cfg); baseURL != "" {
		opts = append(opts, officialopenai.WithBaseURL(baseURL))
	}
	variant, err := OpenAIModelVariant(cfg)
	if err != nil {
		return nil, err
	}
	if variant != "" {
		opts = append(opts, officialopenai.WithVariant(variant))
	}
	return officialopenai.New(modelName, opts...), nil
}

// OpenAIModelBaseURL returns configured base_url or OPENAI_BASE_URL fallback.
func OpenAIModelBaseURL(cfg OpenAIConfig) string {
	if baseURL := strings.TrimSpace(cfg.BaseURL); baseURL != "" {
		return baseURL
	}
	switch strings.TrimSpace(cfg.Provider) {
	case ProviderDeepSeek:
		return ""
	}
	return strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
}

// ModelAPIKeyEnv returns the provider's API key env name.
func ModelAPIKeyEnv(cfg OpenAIConfig) string {
	if envName := strings.TrimSpace(cfg.APIKeyEnv); envName != "" {
		return envName
	}
	switch strings.TrimSpace(cfg.Provider) {
	case ProviderDeepSeek:
		return DefaultDeepSeekAPIKeyEnv
	default:
		return DefaultOpenAIAPIKeyEnv
	}
}

// ModelAPIKey returns explicit local key or the configured env value.
func ModelAPIKey(cfg OpenAIConfig, envName string) string {
	if key := strings.TrimSpace(cfg.APIKey); key != "" {
		return key
	}
	return strings.TrimSpace(os.Getenv(envName))
}

// OpenAIModelVariant maps provider config to official variants.
func OpenAIModelVariant(cfg OpenAIConfig) (officialopenai.Variant, error) {
	variant := strings.TrimSpace(cfg.Variant)
	if variant == "" {
		switch strings.TrimSpace(cfg.Provider) {
		case ProviderDeepSeek:
			variant = string(officialopenai.VariantDeepSeek)
		case ProviderOpenAI, ProviderOpenAICompatible, "":
			variant = string(officialopenai.VariantOpenAI)
		default:
			return "", fmt.Errorf("unsupported OpenAI-compatible provider %q", cfg.Provider)
		}
	}
	switch variant {
	case string(officialopenai.VariantOpenAI):
		return officialopenai.VariantOpenAI, nil
	case string(officialopenai.VariantDeepSeek):
		return officialopenai.VariantDeepSeek, nil
	default:
		return "", errors.New("unsupported model variant")
	}
}
