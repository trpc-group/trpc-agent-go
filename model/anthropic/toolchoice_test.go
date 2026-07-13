//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package anthropic

import (
	"context"
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestApplyToolChoice(t *testing.T) {
	req := &anthropic.MessageNewParams{}
	applyToolChoice(req, nil)
	if req.ToolChoice.OfAuto != nil || req.ToolChoice.OfAny != nil ||
		req.ToolChoice.OfTool != nil || req.ToolChoice.OfNone != nil {
		t.Fatalf("nil tool choice must not set the field: %+v", req.ToolChoice)
	}

	req = &anthropic.MessageNewParams{}
	applyToolChoice(req, &model.ToolChoice{Mode: model.ToolChoiceAuto})
	if req.ToolChoice.OfAuto == nil {
		t.Fatalf("auto must map to the auto variant: %+v", req.ToolChoice)
	}
	req = &anthropic.MessageNewParams{}
	applyToolChoice(req, &model.ToolChoice{Mode: model.ToolChoiceNone})
	if req.ToolChoice.OfNone == nil {
		t.Fatalf("none must map to the none variant: %+v", req.ToolChoice)
	}
	req = &anthropic.MessageNewParams{}
	applyToolChoice(req, &model.ToolChoice{Mode: model.ToolChoiceRequired})
	if req.ToolChoice.OfAny == nil {
		t.Fatalf("required must map to the any variant: %+v", req.ToolChoice)
	}
	req = &anthropic.MessageNewParams{}
	applyToolChoice(req, &model.ToolChoice{Mode: model.ToolChoiceFunction, FunctionName: "get_weather"})
	if req.ToolChoice.OfTool == nil || req.ToolChoice.OfTool.Name != "get_weather" {
		t.Fatalf("function must map to the named tool variant: %+v", req.ToolChoice)
	}

	req = &anthropic.MessageNewParams{}
	applyToolChoice(req, &model.ToolChoice{Mode: "bogus"})
	if req.ToolChoice.OfAuto != nil || req.ToolChoice.OfAny != nil ||
		req.ToolChoice.OfTool != nil || req.ToolChoice.OfNone != nil {
		t.Fatalf("unknown mode must be ignored: %+v", req.ToolChoice)
	}
}

func TestGenerateContentRejectsEmptyFunctionName(t *testing.T) {
	m := New("claude-test-model")
	_, err := m.GenerateContent(context.Background(), &model.Request{
		GenerationConfig: model.GenerationConfig{
			ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceFunction},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "function name") {
		t.Fatalf("empty function name must fail fast with a framework error, got %v", err)
	}
}
