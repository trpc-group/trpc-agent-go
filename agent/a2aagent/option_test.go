//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2aagent

import (
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestWithStreamingChannelBufSize(t *testing.T) {
	tests := []struct {
		name        string
		inputSize   int
		wantBufSize int
	}{
		{
			name:        "positive buffer size",
			inputSize:   1024,
			wantBufSize: 1024,
		},
		{
			name:        "zero buffer size",
			inputSize:   0,
			wantBufSize: 0,
		},
		{
			name:        "negative size uses default",
			inputSize:   -1,
			wantBufSize: defaultStreamingChannelSize,
		},
		{
			name:        "large buffer size",
			inputSize:   65536,
			wantBufSize: 65536,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &A2AAgent{}

			option := WithStreamingChannelBufSize(tt.inputSize)
			option(agent)

			if agent.streamingBufSize != tt.wantBufSize {
				t.Errorf("got buf size %d, want %d", agent.streamingBufSize, tt.wantBufSize)
			}
		})
	}
}

func TestWithA2ADataPartMapper(t *testing.T) {
	agent := &A2AAgent{}
	WithA2ADataPartMapper(nil)(agent)
	WithA2ADataPartMapper(func(part *protocol.DataPart, result *A2ADataPartMappingResult) (bool, error) {
		return false, nil
	})(agent)

	if len(agent.dataPartMappers) != 1 {
		t.Fatalf("expected one data part mapper, got %d", len(agent.dataPartMappers))
	}
}

func TestA2ADataPartMappingResult_AccessorsAndMutators(t *testing.T) {
	var nilResult *A2ADataPartMappingResult
	if nilResult.GetTextContent() != "" {
		t.Fatal("nil result should return empty text content")
	}
	if nilResult.GetReasoningContent() != "" {
		t.Fatal("nil result should return empty reasoning content")
	}
	if nilResult.GetCodeExecution() != "" {
		t.Fatal("nil result should return empty code execution")
	}
	if nilResult.GetCodeExecutionResult() != "" {
		t.Fatal("nil result should return empty code execution result")
	}
	nilResult.SetTextContent("ignored")
	nilResult.SetReasoningContent("ignored")
	nilResult.AppendToolCall(model.ToolCall{ID: "ignored"})
	nilResult.AppendToolResponse(A2ADataPartToolResponse{ID: "ignored"})
	nilResult.SetCodeExecution("ignored")
	nilResult.SetCodeExecutionResult("ignored")
	if err := nilResult.SetEventExtension("ignored", map[string]any{"ok": true}); err != nil {
		t.Fatalf("nil result should ignore event extension writes: %v", err)
	}

	result := &A2ADataPartMappingResult{}
	result.SetTextContent("mapped text")
	result.SetReasoningContent("mapped reasoning")
	result.AppendToolCall(model.ToolCall{
		ID:   "call-1",
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name: "lookup",
		},
	})
	result.AppendToolResponse(A2ADataPartToolResponse{
		ID:      "resp-1",
		Name:    "lookup",
		Content: "ok",
	})
	result.SetCodeExecution("print('ok')")
	result.SetCodeExecutionResult("ok")
	if err := result.SetEventExtension("payload", map[string]any{"ok": true}); err != nil {
		t.Fatalf("SetEventExtension() returned error: %v", err)
	}

	if got := result.GetTextContent(); got != "mapped text" {
		t.Fatalf("GetTextContent() = %q, want %q", got, "mapped text")
	}
	if got := result.GetReasoningContent(); got != "mapped reasoning" {
		t.Fatalf("GetReasoningContent() = %q, want %q", got, "mapped reasoning")
	}
	if got := result.GetCodeExecution(); got != "print('ok')" {
		t.Fatalf("GetCodeExecution() = %q, want %q", got, "print('ok')")
	}
	if got := result.GetCodeExecutionResult(); got != "ok" {
		t.Fatalf("GetCodeExecutionResult() = %q, want %q", got, "ok")
	}
	if len(result.toolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(result.toolCalls))
	}
	if len(result.toolResponses) != 1 {
		t.Fatalf("expected one tool response, got %d", len(result.toolResponses))
	}
	if string(result.eventExtensions["payload"]) != `{"ok":true}` {
		t.Fatalf("unexpected event extension payload: %s", result.eventExtensions["payload"])
	}
}

func TestA2ADataPartMappingResult_SetEventExtensionError(t *testing.T) {
	result := &A2ADataPartMappingResult{}
	if err := result.SetEventExtension("", map[string]any{"ignored": true}); err != nil {
		t.Fatalf("empty key should be ignored without error: %v", err)
	}
	if len(result.eventExtensions) != 0 {
		t.Fatalf("empty key should not write extensions, got %d", len(result.eventExtensions))
	}

	err := result.SetEventExtension("bad", map[string]any{"fn": func() {}})
	if err == nil {
		t.Fatal("expected marshal error for unsupported extension payload")
	}
}

func TestCloneA2AExtensionRawMessage(t *testing.T) {
	if cloneA2AExtensionRawMessage(nil) != nil {
		t.Fatal("nil raw message should clone to nil")
	}

	raw := json.RawMessage(`{"value":"x"}`)
	cloned := cloneA2AExtensionRawMessage(raw)
	raw[0] = '['

	if string(cloned) != `{"value":"x"}` {
		t.Fatalf("clone should not change after source mutation, got %s", cloned)
	}
}
