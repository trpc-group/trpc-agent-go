//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package assist_test

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/assist"
)

// TestResolveModel_Fake verifies related behavior.
func TestResolveModel_Fake(t *testing.T) {
	m, backend, err := assist.ResolveModel(assist.LLMFake, assist.OpenAIModelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if backend != assist.LLMFake {
		t.Fatalf("backend=%s", backend)
	}
	if m.Info().Name != "fake-code-review" {
		t.Fatalf("model=%s", m.Info().Name)
	}
}

// TestResolveModel_OpenAIRequiresKey verifies related behavior.
func TestResolveModel_OpenAIRequiresKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, _, err := assist.ResolveModel(assist.LLMOpenAI, assist.OpenAIModelOptions{
		Model:  "gpt-4o-mini",
		APIKey: "",
	})
	if err == nil {
		t.Fatal("expected error without API key")
	}
}

// TestResolveModel_AutoFallsBackToFake verifies related behavior.
func TestResolveModel_AutoFallsBackToFake(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	m, backend, err := assist.ResolveModel(assist.LLMAuto, assist.OpenAIModelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if backend != assist.LLMFake {
		t.Fatalf("backend=%s want fake", backend)
	}
	if m.Info().Name != "fake-code-review" {
		t.Fatalf("model=%s", m.Info().Name)
	}
}

// TestResolveModel_OpenAIWithKey verifies related behavior.
func TestResolveModel_OpenAIWithKey(t *testing.T) {
	m, backend, err := assist.ResolveModel(assist.LLMOpenAI, assist.OpenAIModelOptions{
		Model:   "gpt-4o-mini",
		APIKey:  "sk-test-not-used",
		BaseURL: "https://example.invalid/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if backend != assist.LLMOpenAI {
		t.Fatalf("backend=%s", backend)
	}
	if m.Info().Name != "gpt-4o-mini" {
		t.Fatalf("model=%s", m.Info().Name)
	}
}

// TestResolveModel_QwenInfersVariantAndBaseAPIAlias verifies related behavior.
func TestResolveModel_QwenInfersVariantAndBaseAPIAlias(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_BASE_API", "https://dashscope.aliyuncs.com/compatible-mode/v1")
	m, backend, err := assist.ResolveModel(assist.LLMOpenAI, assist.OpenAIModelOptions{
		Model:   "qwen-flash",
		Variant: "qwen-flash", // common mistake; should normalize to qwen
	})
	if err != nil {
		t.Fatal(err)
	}
	if backend != assist.LLMOpenAI {
		t.Fatalf("backend=%s", backend)
	}
	if m.Info().Name != "qwen-flash" {
		t.Fatalf("model=%s", m.Info().Name)
	}
}
