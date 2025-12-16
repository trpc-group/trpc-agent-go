package compiler

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/dsl"
)

func TestNewLLMAgentNodeFuncFromConfig_CoercesNumbers(t *testing.T) {
	cfg := map[string]any{
		"model_spec": map[string]any{
			"provider":   "openai",
			"model_name": "dummy",
			"api_key":    "dummy",
		},
		"temperature":       float32(0.7),
		"max_tokens":        int32(512),
		"top_p":             int32(1),
		"presence_penalty":  int32(0),
		"frequency_penalty": int32(0),
	}

	fn, err := newLLMAgentNodeFuncFromConfig("return_agent", cfg, dsl.ToolProvider(nil), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fn == nil {
		t.Fatalf("expected node func, got nil")
	}
}

func TestNewLLMAgentNodeFuncFromConfig_RejectsInvalidMaxTokens(t *testing.T) {
	cfg := map[string]any{
		"model_spec": map[string]any{
			"provider":   "openai",
			"model_name": "dummy",
			"api_key":    "dummy",
		},
		"max_tokens": float64(1.5),
	}

	_, err := newLLMAgentNodeFuncFromConfig("return_agent", cfg, dsl.ToolProvider(nil), false)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}
