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
	"strings"
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

func TestOutputResponseProcessor_StructuredOutputParseErrorTruncatesSnippet(t *testing.T) {
	ctx := context.Background()
	proc := NewOutputResponseProcessor("", nil)

	inv := &agent.Invocation{InvocationID: "inv", AgentName: "agent"}
	inv.StructuredOutputType = reflect.TypeOf(typedStruct{})

	content := "{" + strings.Repeat("x", structuredOutputParseErrorSnippetBytes+1) + "}"
	rsp := &model.Response{
		Done:    true,
		Choices: []model.Choice{{Message: model.Message{Content: content}}},
	}

	ch := make(chan *event.Event, 1)
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)
	close(ch)
	if len(ch) != 1 {
		t.Fatalf("expected one parse error event, got %d", len(ch))
	}
	evt := <-ch
	assertStructuredOutputParseError(t, evt)
	raw := evt.Extensions[structuredOutputParseErrorExtensionKey]
	var ext structuredOutputParseErrorExtension
	if err := json.Unmarshal(raw, &ext); err != nil {
		t.Fatalf("unmarshal parse error extension: %v", err)
	}
	if len(ext.Snippet) != structuredOutputParseErrorSnippetBytes {
		t.Fatalf("expected snippet length %d, got %d", structuredOutputParseErrorSnippetBytes, len(ext.Snippet))
	}
	if !ext.Truncated {
		t.Fatalf("expected truncated=true")
	}
}

func assertStructuredOutputParseError(
	t *testing.T,
	evt *event.Event,
) {
	t.Helper()
	if evt == nil {
		t.Fatalf("expected parse error event")
	}
	if evt.Object != model.ObjectTypeStateUpdate {
		t.Fatalf("expected state update parse error event, got %q", evt.Object)
	}
	if evt.Done {
		t.Fatalf("parse error event should be non-terminal")
	}
	if evt.Error != nil {
		t.Fatalf("parse error observability event should not set Response.Error: %v", evt.Error)
	}
	if !evt.ContainsTag(structuredOutputParseErrorCode) {
		t.Fatalf("expected parse error tag %q", structuredOutputParseErrorCode)
	}
	raw, ok := evt.Extensions[structuredOutputParseErrorExtensionKey]
	if !ok {
		t.Fatalf("expected parse error extension %q", structuredOutputParseErrorExtensionKey)
	}
	var ext structuredOutputParseErrorExtension
	if err := json.Unmarshal(raw, &ext); err != nil {
		t.Fatalf("unmarshal parse error extension: %v", err)
	}
	if ext.Error == "" {
		t.Fatalf("expected extension error")
	}
	if ext.Snippet == "" {
		t.Fatalf("expected extension snippet")
	}
}
