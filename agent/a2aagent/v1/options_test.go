//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2aagent

import (
	"encoding/json"
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/v2/client"
	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	protocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type optionEventConverter struct{}

func (*optionEventConverter) ConvertToEvents(
	protocol.SendMessageResponse,
	string,
	*agent.Invocation,
) ([]*event.Event, error) {
	return nil, nil
}

func (*optionEventConverter) ConvertStreamingToEvents(
	protocol.StreamResponse,
	string,
	*agent.Invocation,
) ([]*event.Event, error) {
	return nil, nil
}

type optionInvocationConverter struct{}

func (*optionInvocationConverter) ConvertToA2AMessage(
	string,
	*agent.Invocation,
) (*protocol.Message, error) {
	message := protocol.NewMessage(protocol.MessageRoleUser, nil)
	return &message, nil
}

func TestA2ADataPartMappingResultAccessorsAndCloning(t *testing.T) {
	var nilResult *A2ADataPartMappingResult
	if nilResult.GetTextContent() != "" ||
		nilResult.GetReasoningContent() != "" ||
		nilResult.GetCodeExecution() != "" ||
		nilResult.GetCodeExecutionResult() != "" {
		t.Fatal("nil mapping result getters returned content")
	}
	nilResult.SetTextContent("ignored")
	nilResult.SetReasoningContent("ignored")
	nilResult.AppendToolCall(model.ToolCall{})
	nilResult.AppendToolResponse(A2ADataPartToolResponse{})
	nilResult.SetCodeExecution("ignored")
	nilResult.SetCodeExecutionResult("ignored")
	if err := nilResult.SetEventExtension("ignored", true); err != nil {
		t.Fatalf("nil SetEventExtension failed: %v", err)
	}

	result := &A2ADataPartMappingResult{}
	result.SetTextContent("text")
	result.SetReasoningContent("reasoning")
	result.AppendToolCall(model.ToolCall{ID: "call"})
	result.AppendToolResponse(A2ADataPartToolResponse{
		ID: "call", Name: "lookup", Content: "result",
	})
	result.SetCodeExecution("print(1)")
	result.SetCodeExecutionResult("1")
	if err := result.SetEventExtension("custom", map[string]any{"value": true}); err != nil {
		t.Fatalf("SetEventExtension failed: %v", err)
	}
	if result.GetTextContent() != "text" ||
		result.GetReasoningContent() != "reasoning" ||
		result.GetCodeExecution() != "print(1)" ||
		result.GetCodeExecutionResult() != "1" ||
		len(result.toolCalls) != 1 || len(result.toolResponses) != 1 {
		t.Fatalf("mapping result = %#v", result)
	}
	if err := result.SetEventExtension("", true); err != nil {
		t.Fatalf("empty extension key failed: %v", err)
	}
	if err := result.SetEventExtension("invalid", func() {}); err == nil {
		t.Fatal("SetEventExtension accepted an unsupported value")
	}

	raw := json.RawMessage(`{"value":true}`)
	cloned := cloneA2AExtensionRawMessage(raw)
	raw[0] = '['
	if string(cloned) != `{"value":true}` {
		t.Fatalf("cloned raw message changed to %q", cloned)
	}
	if cloneA2AExtensionRawMessage(nil) != nil {
		t.Fatal("nil raw message clone was non-nil")
	}
	if cloneA2AExtensions(nil) != nil {
		t.Fatal("nil extension map clone was non-nil")
	}
	extensions := cloneA2AExtensions(map[string]json.RawMessage{"custom": cloned})
	cloned[0] = '['
	if string(extensions["custom"]) != `{"value":true}` {
		t.Fatalf("cloned extension map changed to %q", extensions["custom"])
	}
}

func TestA2AAgentOptions(t *testing.T) {
	eventConverter := &optionEventConverter{}
	messageConverter := &optionInvocationConverter{}
	mapper := func(*protocol.Part, *A2ADataPartMappingResult) (bool, error) {
		return true, nil
	}
	hook := func(next ConvertToA2AMessageFunc) ConvertToA2AMessageFunc { return next }
	card := &protocolserver.AgentCard{Name: "card", URL: "http://localhost:8080"}
	a := &A2AAgent{}
	for _, option := range []Option{
		WithName("agent"),
		WithDescription("description"),
		WithAgentCardURL(" http://localhost:8080 "),
		WithAgentCard(card),
		WithCustomEventConverter(eventConverter),
		WithA2ADataPartMapper(nil),
		WithA2ADataPartMapper(mapper),
		WithCustomA2AConverter(messageConverter),
		WithA2AClientExtraOptions([]client.Option{}...),
		WithStreamingChannelBufSize(-1),
		WithTransferStateKey("state.*"),
		WithBuildMessageHook(hook),
		WithUserIDHeader("X-Custom-User"),
		WithEnableStreaming(true),
	} {
		option(a)
	}
	if a.name != "agent" || a.description != "description" ||
		a.agentURL != "http://localhost:8080" || a.agentCard != card ||
		a.eventConverter != eventConverter || len(a.dataPartMappers) != 1 ||
		a.a2aMessageConverter != messageConverter ||
		a.streamingBufSize != defaultStreamingChannelSize ||
		len(a.transferStateKey) != 1 || a.buildMessageHook == nil ||
		a.userIDHeader != "X-Custom-User" ||
		a.enableStreaming == nil || !*a.enableStreaming {
		t.Fatalf("options not applied: %#v", a)
	}
	WithStreamingChannelBufSize(0)(a)
	if a.streamingBufSize != 0 {
		t.Fatalf("streaming buffer size = %d, want 0", a.streamingBufSize)
	}
	WithUserIDHeader("")(a)
	if a.userIDHeader != "X-Custom-User" {
		t.Fatal("empty user ID header overwrote configured value")
	}
}

func TestDataPartMappingResultApplication(t *testing.T) {
	original := &parseResult{
		textContent:         "original",
		reasoningContent:    "old reasoning",
		codeExecution:       "old code",
		codeExecutionResult: "old result",
		extensions: map[string]json.RawMessage{
			"existing": json.RawMessage(`true`),
		},
	}
	mapped := newDataPartMappingResult(original)
	mapped.SetTextContent("mapped")
	mapped.SetReasoningContent("mapped reasoning")
	mapped.SetCodeExecution("mapped code")
	mapped.SetCodeExecutionResult("mapped result")
	mapped.AppendToolCall(model.ToolCall{ID: "call"})
	mapped.AppendToolResponse(A2ADataPartToolResponse{
		ID: "call", Name: "lookup", Content: "response",
	})
	if err := mapped.SetEventExtension("custom", 1); err != nil {
		t.Fatalf("SetEventExtension failed: %v", err)
	}
	applyDataPartMappingResult(original, mapped)
	if original.textContent != "mapped" ||
		original.reasoningContent != "mapped reasoning" ||
		original.codeExecution != "mapped code" ||
		original.codeExecutionResult != "mapped result" ||
		len(original.toolCalls) != 1 || len(original.toolResponses) != 1 ||
		len(original.extensions) != 2 {
		t.Fatalf("applied result = %#v", original)
	}
	applyDataPartMappingResult(nil, mapped)
	applyDataPartMappingResult(original, nil)
	if empty := newDataPartMappingResult(nil); empty == nil {
		t.Fatal("newDataPartMappingResult(nil) returned nil")
	}
}

func TestBuildA2AMessageHooksAndTransferState(t *testing.T) {
	wantErr := errors.New("conversion failed")
	a := &A2AAgent{name: "remote"}
	if _, err := a.buildA2AMessage(&agent.Invocation{}); err == nil {
		t.Fatal("buildA2AMessage accepted a nil converter")
	}
	a.a2aMessageConverter = invocationConverterFunc(func(
		string,
		*agent.Invocation,
	) (*protocol.Message, error) {
		return nil, wantErr
	})
	if _, err := a.buildA2AMessage(&agent.Invocation{}); !errors.Is(err, wantErr) {
		t.Fatalf("conversion error = %v, want %v", err, wantErr)
	}
	a.a2aMessageConverter = invocationConverterFunc(func(
		string,
		*agent.Invocation,
	) (*protocol.Message, error) {
		return nil, nil
	})
	if _, err := a.buildA2AMessage(&agent.Invocation{}); err == nil {
		t.Fatal("buildA2AMessage accepted a nil message")
	}

	a.transferStateKey = []string{"*", "user.*", "*.id", "exact"}
	a.buildMessageHook = func(ConvertToA2AMessageFunc) ConvertToA2AMessageFunc {
		return func(string, *agent.Invocation) (*protocol.Message, error) {
			message := protocol.NewMessage(protocol.MessageRoleUser, nil)
			return &message, nil
		}
	}
	invocation := &agent.Invocation{}
	invocation.RunOptions.RuntimeState = map[string]any{
		"user.name":  "alice",
		"account.id": "account",
		"exact":      true,
		"other":      1,
	}
	message, err := a.buildA2AMessage(invocation)
	if err != nil {
		t.Fatalf("buildA2AMessage failed: %v", err)
	}
	if len(message.Metadata) != 4 {
		t.Fatalf("transferred metadata = %#v", message.Metadata)
	}

	next := a.wrapWithTransferState(func(string, *agent.Invocation) (*protocol.Message, error) {
		return nil, nil
	})
	if message, err := next("remote", invocation); err != nil || message != nil {
		t.Fatalf("nil wrapped message = %#v, err = %v", message, err)
	}
	next = a.wrapWithTransferState(func(string, *agent.Invocation) (*protocol.Message, error) {
		return nil, wantErr
	})
	if _, err := next("remote", invocation); !errors.Is(err, wantErr) {
		t.Fatalf("wrapped error = %v, want %v", err, wantErr)
	}
	noState := &agent.Invocation{}
	next = a.wrapWithTransferState(func(string, *agent.Invocation) (*protocol.Message, error) {
		message := protocol.NewMessage(protocol.MessageRoleUser, nil)
		return &message, nil
	})
	if _, err := next("remote", noState); err != nil {
		t.Fatalf("wrapped no-state conversion failed: %v", err)
	}
}

type invocationConverterFunc func(string, *agent.Invocation) (*protocol.Message, error)

func (f invocationConverterFunc) ConvertToA2AMessage(
	name string,
	invocation *agent.Invocation,
) (*protocol.Message, error) {
	return f(name, invocation)
}
