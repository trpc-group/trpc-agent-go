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
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	agentmodel "trpc.group/trpc-go/trpc-agent-go/model"
	officialopenai "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

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

func TestOpenAIModelTimeoutDefaultsToHTTPProviderTimeout(t *testing.T) {
	if got := OpenAIModelTimeout(OpenAIConfig{}); got != defaultHTTPTimeout {
		t.Fatalf("expected default timeout %s, got %s", defaultHTTPTimeout, got)
	}
}

func TestOpenAIModelProviderTimeoutBoundsStalledRequests(t *testing.T) {
	const timeout = 50 * time.Millisecond

	originalNewHTTPClient := agentmodel.DefaultNewHTTPClient
	t.Cleanup(func() {
		agentmodel.DefaultNewHTTPClient = originalNewHTTPClient
	})

	var capturedTimeout time.Duration
	attempts := 0
	agentmodel.DefaultNewHTTPClient = func(opts ...agentmodel.HTTPClientOption) agentmodel.HTTPClient {
		httpOpts := &agentmodel.HTTPClientOptions{}
		for _, opt := range opts {
			opt(httpOpts)
		}
		capturedTimeout = httpOpts.Timeout
		return &http.Client{
			Timeout: httpOpts.Timeout,
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				<-req.Context().Done()
				return nil, req.Context().Err()
			}),
		}
	}

	model, err := NewOpenAIModel(OpenAIConfig{
		Provider: ProviderOpenAICompatible,
		Model:    "gpt-4o-mini",
		APIKey:   "sk-localyaml-1234567890abcdef",
		BaseURL:  "https://gateway.example.com/v1",
		Timeout:  timeout,
	})
	if err != nil {
		t.Fatalf("NewOpenAIModel returned error: %v", err)
	}
	if capturedTimeout != timeout {
		t.Fatalf("expected official client timeout %s, got %s", timeout, capturedTimeout)
	}

	start := time.Now()
	responseChan, err := model.GenerateContent(context.Background(), &agentmodel.Request{
		Messages: []agentmodel.Message{
			agentmodel.NewUserMessage("hello"),
		},
	})
	if err != nil {
		t.Fatalf("GenerateContent returned error: %v", err)
	}

	var responses []*agentmodel.Response
	for response := range responseChan {
		responses = append(responses, response)
	}
	elapsed := time.Since(start)

	if len(responses) != 1 {
		t.Fatalf("expected a single terminal response, got %d", len(responses))
	}
	if responses[0].Error == nil {
		t.Fatalf("expected timeout error response, got %+v", responses[0])
	}
	if !strings.Contains(strings.ToLower(responses[0].Error.Message), "timeout") &&
		!strings.Contains(strings.ToLower(responses[0].Error.Message), "deadline exceeded") {
		t.Fatalf("expected timeout-related error, got %q", responses[0].Error.Message)
	}
	if attempts != 1 {
		t.Fatalf("expected a single timed request attempt, got %d", attempts)
	}
	if elapsed >= 4*timeout {
		t.Fatalf("expected request to be bounded near %s, took %s", timeout, elapsed)
	}
}
