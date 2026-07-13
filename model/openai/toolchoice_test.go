//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"testing"

	openai "github.com/openai/openai-go"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestApplyToolChoice(t *testing.T) {
	// nil leaves the field untouched (provider default).
	req := &openai.ChatCompletionNewParams{}
	applyToolChoice(req, nil)
	if req.ToolChoice.OfAuto.Valid() || req.ToolChoice.OfChatCompletionNamedToolChoice != nil {
		t.Fatalf("nil tool choice must not set the field: %+v", req.ToolChoice)
	}

	for mode, want := range map[string]string{
		"": "auto", model.ToolChoiceAuto: "auto",
		model.ToolChoiceNone: "none", model.ToolChoiceRequired: "required",
	} {
		req := &openai.ChatCompletionNewParams{}
		applyToolChoice(req, &model.ToolChoice{Mode: mode})
		if got := req.ToolChoice.OfAuto.Value; got != want {
			t.Fatalf("mode %q: got %q want %q", mode, got, want)
		}
	}

	req = &openai.ChatCompletionNewParams{}
	applyToolChoice(req, &model.ToolChoice{Mode: model.ToolChoiceFunction, FunctionName: "get_weather"})
	named := req.ToolChoice.OfChatCompletionNamedToolChoice
	if named == nil || named.Function.Name != "get_weather" {
		t.Fatalf("function mode must set the named tool: %+v", req.ToolChoice)
	}

	// Unknown mode: ignored, no panic, field untouched.
	req = &openai.ChatCompletionNewParams{}
	applyToolChoice(req, &model.ToolChoice{Mode: "bogus"})
	if req.ToolChoice.OfAuto.Valid() || req.ToolChoice.OfChatCompletionNamedToolChoice != nil {
		t.Fatalf("unknown mode must be ignored: %+v", req.ToolChoice)
	}
}
