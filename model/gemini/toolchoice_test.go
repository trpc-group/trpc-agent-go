//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gemini

import (
	"testing"

	"google.golang.org/genai"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestApplyToolChoice(t *testing.T) {
	cfg := &genai.GenerateContentConfig{}
	applyToolChoice(cfg, nil)
	if cfg.ToolConfig != nil {
		t.Fatalf("nil tool choice must not touch ToolConfig: %+v", cfg.ToolConfig)
	}

	for mode, want := range map[string]genai.FunctionCallingConfigMode{
		"":                       genai.FunctionCallingConfigModeAuto,
		model.ToolChoiceAuto:     genai.FunctionCallingConfigModeAuto,
		model.ToolChoiceNone:     genai.FunctionCallingConfigModeNone,
		model.ToolChoiceRequired: genai.FunctionCallingConfigModeAny,
	} {
		cfg := &genai.GenerateContentConfig{}
		applyToolChoice(cfg, &model.ToolChoice{Mode: mode})
		if cfg.ToolConfig == nil || cfg.ToolConfig.FunctionCallingConfig.Mode != want {
			t.Fatalf("mode %q: got %+v want %v", mode, cfg.ToolConfig, want)
		}
	}

	// function mode: ANY + allowed names; existing ToolConfig preserved.
	cfg = &genai.GenerateContentConfig{ToolConfig: &genai.ToolConfig{
		FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAuto},
	}}
	applyToolChoice(cfg, &model.ToolChoice{Mode: model.ToolChoiceFunction, FunctionName: "get_weather"})
	fc := cfg.ToolConfig.FunctionCallingConfig
	if fc.Mode != genai.FunctionCallingConfigModeAny || len(fc.AllowedFunctionNames) != 1 ||
		fc.AllowedFunctionNames[0] != "get_weather" {
		t.Fatalf("function mode mapping wrong: %+v", fc)
	}

	// Unknown mode: ignored, existing config untouched.
	cfg = &genai.GenerateContentConfig{}
	applyToolChoice(cfg, &model.ToolChoice{Mode: "bogus"})
	if cfg.ToolConfig != nil {
		t.Fatalf("unknown mode must be ignored: %+v", cfg.ToolConfig)
	}
}
