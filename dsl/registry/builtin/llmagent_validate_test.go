//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package builtin

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
)

func TestLLMAgentValidate_MaxTokens_IntegerLike(t *testing.T) {
	component := &LLMAgentComponent{}

	baseConfig := registry.ComponentConfig{
		"model_spec": map[string]any{
			"provider":   "openai",
			"model_name": "dummy",
			"api_key":    "dummy",
		},
	}

	tests := []struct {
		name      string
		maxTokens any
		wantErr   bool
	}{
		{name: "int32", maxTokens: int32(512)},
		{name: "int64", maxTokens: int64(512)},
		{name: "uint32", maxTokens: uint32(512)},
		{name: "float64_int", maxTokens: float64(512)},
		{name: "float64_fraction", maxTokens: float64(512.5), wantErr: true},
		{name: "string", maxTokens: "512", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := make(registry.ComponentConfig, len(baseConfig)+1)
			for k, v := range baseConfig {
				cfg[k] = v
			}
			cfg["max_tokens"] = tt.maxTokens

			err := component.Validate(cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "max_tokens") {
					t.Fatalf("error should mention max_tokens, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLLMAgentValidate_MaxTokens_TooLarge(t *testing.T) {
	component := &LLMAgentComponent{}

	maxInt := uint64(int64(^uint(0) >> 1))

	cfg := registry.ComponentConfig{
		"model_spec": map[string]any{
			"provider":   "openai",
			"model_name": "dummy",
			"api_key":    "dummy",
		},
		"max_tokens": maxInt + 1,
	}

	err := component.Validate(cfg)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("error should mention max_tokens, got: %v", err)
	}
}
