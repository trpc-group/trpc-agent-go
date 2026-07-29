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

func TestOutputResponseProcessor_StructuredOutputTypedEvent_RawNewlineInString(t *testing.T) {
	ctx := context.Background()
	proc := NewOutputResponseProcessor("", nil)

	inv := &agent.Invocation{InvocationID: "inv", AgentName: "agent"}
	inv.StructuredOutputType = reflect.TypeOf((*sampleOut)(nil))

	// LLM output defect: raw newline and tab inside a JSON string literal.
	content := "{\"a\": \"line1\nline2\tend\"}"
	rsp := &model.Response{
		Done:    true,
		Choices: []model.Choice{{Message: model.Message{Content: content}}},
	}

	ch := make(chan *event.Event, 1)
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)

	select {
	case evt := <-ch:
		out, ok := evt.StructuredOutput.(*sampleOut)
		if !ok {
			t.Fatalf("expected *sampleOut, got %T", evt.StructuredOutput)
		}
		if out.A != "line1\nline2\tend" {
			t.Fatalf("unexpected value: %q", out.A)
		}
	default:
		t.Fatalf("expected an event to be emitted despite raw control chars")
	}
}

func TestUnmarshalLenient(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantA   string
		wantErr bool
	}{
		{name: "strict valid", input: `{"a":"ok"}`, wantA: "ok"},
		{name: "raw newline repaired", input: "{\"a\":\"x\ny\"}", wantA: "x\ny"},
		{name: "raw carriage return repaired", input: "{\"a\":\"x\r\ny\"}", wantA: "x\r\ny"},
		{name: "other control char repaired", input: "{\"a\":\"x\x01y\"}", wantA: "x\x01y"},
		{name: "escaped newline untouched", input: `{"a":"x\ny"}`, wantA: "x\ny"},
		{name: "newline outside string untouched", input: "{\n\"a\":\"ok\"\n}", wantA: "ok"},
		{name: "still invalid after repair", input: "{\"a\":\"x\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out sampleOut
			err := unmarshalLenient(tt.input, &out)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.A != tt.wantA {
				t.Fatalf("got %q, want %q", out.A, tt.wantA)
			}
		})
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
