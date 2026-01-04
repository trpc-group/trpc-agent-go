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
	"encoding/json"
	"reflect"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type sampleOut struct {
	A string `json:"a"`
}

func TestOutputResponseProcessor_StructuredOutputTypedEvent(t *testing.T) {
	ctx := context.Background()
	proc := NewOutputResponseProcessor("", nil)

	inv := &agent.Invocation{InvocationID: "inv", AgentName: "agent"}
	// Set StructuredOutputType to reflect.Type of *sampleOut for typed payloads.
	inv.StructuredOutputType = reflect.TypeOf((*sampleOut)(nil))

	// Response content: valid JSON object for sampleOut.
	payload := sampleOut{A: "ok"}
	b, _ := json.Marshal(payload)
	rsp := &model.Response{
		Done:    true,
		Choices: []model.Choice{{Message: model.Message{Content: string(b)}}},
	}

	ch := make(chan *event.Event, 1)
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)

	select {
	case evt := <-ch:
		if evt.StructuredOutput == nil {
			t.Fatalf("expected typed StructuredOutput event")
		}
	default:
		t.Fatalf("expected an event to be emitted")
	}
}

func TestOutputResponseProcessor_StructuredOutputUntypedEvent(t *testing.T) {
	ctx := context.Background()
	proc := NewOutputResponseProcessor("", nil)

	inv := &agent.Invocation{InvocationID: "inv", AgentName: "agent"}
	inv.StructuredOutput = &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
		JSONSchema: &model.JSONSchemaConfig{
			Name:   "output",
			Schema: map[string]any{"type": "object"},
			Strict: true,
		},
	}

	rsp := &model.Response{
		Done:    true,
		Choices: []model.Choice{{Message: model.Message{Content: `{"a":"ok"}`}}},
	}

	ch := make(chan *event.Event, 1)
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)

	select {
	case evt := <-ch:
		if evt.StructuredOutput == nil {
			t.Fatalf("expected untyped StructuredOutput event")
		}
		m, ok := evt.StructuredOutput.(map[string]any)
		if !ok {
			t.Fatalf("expected StructuredOutput to be map[string]any, got %T", evt.StructuredOutput)
		}
		if m["a"] != "ok" {
			t.Fatalf("expected StructuredOutput[a] to be %q, got %v", "ok", m["a"])
		}
	default:
		t.Fatalf("expected an event to be emitted")
	}
}
