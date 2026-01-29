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
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type instructionTestAgent struct {
	tools []tool.Tool
}

func (a *instructionTestAgent) Run(
	context.Context, *agent.Invocation,
) (<-chan *event.Event, error) {
	return nil, nil
}

func (a *instructionTestAgent) Tools() []tool.Tool { return a.tools }

func (a *instructionTestAgent) Info() agent.Info { return agent.Info{} }

func (a *instructionTestAgent) SubAgents() []agent.Agent { return nil }

func (a *instructionTestAgent) FindSubAgent(string) agent.Agent { return nil }

type instructionTestTool struct{ decl tool.Declaration }

func (t instructionTestTool) Declaration() *tool.Declaration { return &t.decl }

func (instructionTestTool) Call(context.Context, []byte) (any, error) {
	return nil, nil
}

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

func TestInstructionProc_JSONInjection_StructuredOutput_AllowsTools(
	t *testing.T,
) {
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

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
	}
	inv := &agent.Invocation{
		AgentName:    "a",
		InvocationID: "id-1",
		Message:      model.NewUserMessage("hi"),
		Agent: &instructionTestAgent{
			tools: []tool.Tool{
				instructionTestTool{
					decl: tool.Declaration{Name: "test"},
				},
			},
		},
	}
	ch := make(chan *event.Event, 1)

	p.ProcessRequest(context.Background(), inv, req, ch)

	if len(req.Messages) == 0 || req.Messages[0].Role != model.RoleSystem {
		t.Fatalf("expected a system message to be created")
	}
	content := req.Messages[0].Content
	if !strings.Contains(content, "You MAY call tools") {
		t.Errorf("expected tools to be permitted in JSON instructions")
	}
	if strings.Contains(content, "IMPORTANT: Return ONLY a JSON object") {
		t.Errorf("expected tools-aware JSON instructions")
	}
	if !strings.Contains(content, "return ONLY a JSON object") {
		t.Errorf("expected final JSON-only rule to be present")
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
