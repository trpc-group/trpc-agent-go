//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestInstructionProc_JSONInjection_StructuredOutput(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"x": map[string]any{"type": "string"},
		},
	}

	p := NewInstructionRequestProcessor(
		"base instruction",
		"base system",
		WithStructuredOutputSchema(schema),
	)

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	inv := &agent.Invocation{AgentName: "a", InvocationID: "id-1"}
	ch := make(chan *event.Event, 1)

	p.ProcessRequest(context.Background(), inv, req, ch)

	if len(req.Messages) == 0 || req.Messages[0].Role != model.RoleSystem {
		t.Fatalf("expected a system message to be created")
	}
	content := req.Messages[0].Content
	if !strings.Contains(content, "IMPORTANT: Return ONLY a JSON object") {
		t.Errorf("expected JSON instructions to be injected")
	}
	if !strings.Contains(content, `"type": "object"`) {
		t.Errorf("expected schema content to be present in instructions")
	}
}

func TestInstructionProc_JSONInjection_OutputSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"y": map[string]any{"type": "number"},
		},
	}

	p := NewInstructionRequestProcessor(
		"",
		"sys",
		WithOutputSchema(schema),
	)

	req := &model.Request{Messages: []model.Message{}}
	inv := &agent.Invocation{AgentName: "a", InvocationID: "id-2"}
	ch := make(chan *event.Event, 1)

	p.ProcessRequest(context.Background(), inv, req, ch)

	if len(req.Messages) == 0 || req.Messages[0].Role != model.RoleSystem {
		t.Fatalf("expected a system message to be created")
	}
	content := req.Messages[0].Content
	if !strings.Contains(content, "IMPORTANT: Return ONLY a JSON object") {
		t.Errorf("expected JSON instructions to be injected for output_schema")
	}
	if !strings.Contains(content, `"y"`) {
		t.Errorf("expected schema properties to be present in instructions")
	}
}
