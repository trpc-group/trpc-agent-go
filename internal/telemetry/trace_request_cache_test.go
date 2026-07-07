//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package telemetry

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

func TestChatTraceState_UpdatesWhenMessagesMutate(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })
	installChatStreamingPolicyForTest()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("aaaa")},
	}
	span := newRecordingSpan()
	state := &ChatTraceState{}

	state.TraceChat(span, &TraceChatAttributes{Request: req})
	req.Messages[0].Content = "bbbb" // Same length, different bytes.
	state.TraceChat(span, &TraceChatAttributes{Request: req})

	got := lastAttrStringValue(t, span.attrs, semconvtrace.KeyGenAIInputMessages)
	var messages []telemetryMessage
	if err := json.Unmarshal([]byte(got), &messages); err != nil {
		t.Fatalf("unmarshal input messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Content != "bbbb" {
		t.Fatalf("expected mutated message in cached trace, got %+v", messages)
	}
}

func TestChatTraceState_PolicyDropInvalidatesCachedAttribute(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	}
	span := newRecordingSpan()
	state := &ChatTraceState{}

	state.TraceChat(span, &TraceChatAttributes{Request: req})
	before := countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages)
	if before != 1 {
		t.Fatalf("expected one input message attribute before drop, got %d", before)
	}

	SetSpanAttributePolicy(AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationChat,
		Key:       semconvtrace.KeyGenAIInputMessages,
		Action:    AttributeDrop,
	}))
	state.TraceChat(span, &TraceChatAttributes{Request: req})
	after := countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages)
	if after != before {
		t.Fatalf("drop policy should not append cached input messages: before=%d after=%d", before, after)
	}
}

func TestChatTraceState_ResponseAttributesStillUpdate(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })
	installChatStreamingPolicyForTest()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	}
	span := newRecordingSpan()
	state := &ChatTraceState{}

	state.TraceChat(span, &TraceChatAttributes{
		Request:  req,
		Response: chatResponseForCacheTest("first"),
	})
	state.TraceChat(span, &TraceChatAttributes{
		Request:  req,
		Response: chatResponseForCacheTest("second"),
	})

	got := lastAttrStringValue(t, span.attrs, semconvtrace.KeyGenAIOutputMessages)
	var choices []telemetryChoice
	if err := json.Unmarshal([]byte(got), &choices); err != nil {
		t.Fatalf("unmarshal output messages: %v", err)
	}
	if len(choices) != 1 || choices[0].Delta.Content != "second" {
		t.Fatalf("expected latest response output, got %+v", choices)
	}
}

func TestRequestMessagesFingerprint_DistinguishesNilAndEmptyBytes(t *testing.T) {
	nilArgs := []model.Message{{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			ID: "call",
			Function: model.FunctionDefinitionParam{
				Name: "tool",
			},
		}},
	}}
	emptyArgs := []model.Message{{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			ID: "call",
			Function: model.FunctionDefinitionParam{
				Name:      "tool",
				Arguments: []byte{},
			},
		}},
	}}

	if requestMessagesFingerprint(nilArgs) == requestMessagesFingerprint(emptyArgs) {
		t.Fatal("expected nil and empty byte slices to produce different fingerprints")
	}
}

func TestRequestMessagesFingerprint_DistinguishesExtraFieldsMarshalError(t *testing.T) {
	emptyExtraFields := []model.Message{{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			ID:          "call",
			ExtraFields: map[string]any{},
		}},
	}}
	invalidExtraFields := []model.Message{{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			ID: "call",
			ExtraFields: map[string]any{
				"invalid": func() {},
			},
		}},
	}}

	if requestMessagesFingerprint(emptyExtraFields) == requestMessagesFingerprint(invalidExtraFields) {
		t.Fatal("expected extra field marshal errors to affect the fingerprint")
	}
}

func BenchmarkTraceChatStreamingRequestAttributes_NoCache(b *testing.B) {
	benchmarkTraceChatStreamingRequestAttributes(b, false)
}

func BenchmarkTraceChatStreamingRequestAttributes_WithCache(b *testing.B) {
	benchmarkTraceChatStreamingRequestAttributes(b, true)
}

func benchmarkTraceChatStreamingRequestAttributes(b *testing.B, useCache bool) {
	installChatStreamingPolicyForTest()
	defer SetSpanAttributePolicy(SpanAttributePolicy{})

	req := &model.Request{Messages: multiTurnMessagesForCacheTest(4)}
	responses := make([]*model.Response, 40)
	for i := range responses {
		responses[i] = chatResponseForCacheTest(fmt.Sprintf("chunk-%d", i))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		span := newRecordingSpan()
		if useCache {
			state := &ChatTraceState{}
			for _, rsp := range responses {
				state.TraceChat(span, &TraceChatAttributes{Request: req, Response: rsp})
			}
			continue
		}
		for _, rsp := range responses {
			TraceChat(span, &TraceChatAttributes{Request: req, Response: rsp})
		}
	}
}

func installChatStreamingPolicyForTest() {
	policy := SpanAttributePolicy{}
	for _, key := range []string{
		semconvtrace.KeyLLMRequest,
		semconvtrace.KeyLLMResponse,
		semconvtrace.KeyGenAIInputMessagesOTel,
		semconvtrace.KeyGenAIOutputMessagesOTel,
	} {
		policy = AppendAttributeRule(policy, AttributeRule{
			Operation: OperationChat,
			Key:       key,
			Action:    AttributeDrop,
		})
	}
	SetSpanAttributePolicy(policy)
}

func chatResponseForCacheTest(content string) *model.Response {
	return &model.Response{
		ID:    "response-id",
		Model: "test-model",
		Choices: []model.Choice{{
			Index: 0,
			Delta: model.Message{Role: model.RoleAssistant, Content: content},
		}},
	}
}

func multiTurnMessagesForCacheTest(turns int) []model.Message {
	messages := make([]model.Message, 0, turns*2)
	for i := 0; i < turns; i++ {
		messages = append(messages,
			model.NewUserMessage(strings.Repeat(fmt.Sprintf("user-turn-%d ", i), 200)),
			model.NewAssistantMessage(strings.Repeat(fmt.Sprintf("assistant-turn-%d ", i), 200)),
		)
	}
	return messages
}

func lastAttrStringValue(t *testing.T, attrs []attribute.KeyValue, key string) string {
	t.Helper()
	for i := len(attrs) - 1; i >= 0; i-- {
		if string(attrs[i].Key) == key {
			return attrs[i].Value.AsString()
		}
	}
	t.Fatalf("missing attribute %s", key)
	return ""
}

func countAttr(attrs []attribute.KeyValue, key string) int {
	count := 0
	for _, attr := range attrs {
		if string(attr.Key) == key {
			count++
		}
	}
	return count
}
