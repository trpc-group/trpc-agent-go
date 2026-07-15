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
	"testing"

	agentmodel "trpc.group/trpc-go/trpc-agent-go/model"
	officialopenai "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func TestOpenAIModelProviderBuildsOfficialDeepSeekModel(t *testing.T) {
	t.Setenv("CR_AGENT_TEST_DEEPSEEK_KEY", "test-deepseek-key")

	model, err := NewOpenAIModel(OpenAIConfig{
		Provider:  "deepseek",
		Model:     "deepseek-chat",
		APIKeyEnv: "CR_AGENT_TEST_DEEPSEEK_KEY",
	})
	if err != nil {
		t.Fatalf("newOpenAIModel returned error: %v", err)
	}
	var _ agentmodel.Model = model
	if _, ok := model.(*officialopenai.Model); !ok {
		t.Fatalf("expected official trpc-agent-go/model/openai model, got %T", model)
	}
	if model.Info().Name != "deepseek-chat" {
		t.Fatalf("expected model name deepseek-chat, got %q", model.Info().Name)
	}
}

func TestOpenAIModelProviderRequiresAPIKeyBeforeNetworkCall(t *testing.T) {
	_, err := NewOpenAIReviewProvider(OpenAIConfig{
		Provider:  "deepseek",
		Model:     "deepseek-chat",
		APIKeyEnv: "CR_AGENT_TEST_MISSING_DEEPSEEK_KEY",
	})
	if err == nil {
		t.Fatal("expected missing API key error")
	}
}

func TestOpenAIModelProviderAcceptsLocalAPIKey(t *testing.T) {
	model, err := NewOpenAIModel(OpenAIConfig{
		Provider: ProviderDeepSeek,
		Model:    "deepseek-chat",
		APIKey:   "sk-localyaml-1234567890abcdef",
	})
	if err != nil {
		t.Fatalf("newOpenAIModel returned error: %v", err)
	}
	if model.Info().Name != "deepseek-chat" {
		t.Fatalf("unexpected model info: %+v", model.Info())
	}
}

func TestOpenAIModelProviderDefaultsToOfficialEnv(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://openai-gateway.example.com/v1")

	cfg := OpenAIConfig{
		Provider: ProviderOpenAICompatible,
		Model:    "gpt-4o-mini",
	}
	if got := ModelAPIKeyEnv(cfg); got != DefaultOpenAIAPIKeyEnv {
		t.Fatalf("expected default OpenAI key env, got %q", got)
	}
	if got := OpenAIModelBaseURL(cfg); got != "https://openai-gateway.example.com/v1" {
		t.Fatalf("expected OPENAI_BASE_URL fallback, got %q", got)
	}
}

func TestOpenAIConfigBaseURLOverridesEnv(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://openai-gateway.example.com/v1")

	got := OpenAIModelBaseURL(OpenAIConfig{
		Provider: ProviderOpenAICompatible,
		Model:    "gpt-4o-mini",
		BaseURL:  "https://yaml-gateway.example.com/v1",
	})
	if got != "https://yaml-gateway.example.com/v1" {
		t.Fatalf("expected config base_url to override env, got %q", got)
	}
}

func TestDeepSeekModelProviderDoesNotInheritOpenAIBaseURL(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://openai-gateway.example.com/v1")

	got := OpenAIModelBaseURL(OpenAIConfig{
		Provider: ProviderDeepSeek,
		Model:    "deepseek-chat",
	})
	if got != "" {
		t.Fatalf("expected DeepSeek default base URL to come from official variant, got %q", got)
	}
}
