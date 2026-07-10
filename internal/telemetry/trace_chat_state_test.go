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

func TestChatTraceState_RequestAttributesCommittedOnce(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })
	installChatStreamingPolicyForTest()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("aaaa")},
	}
	span := newRecordingSpan()
	state := &ChatTraceState{}

	state.TraceChat(span, &TraceChatAttributes{Request: req})
	req.Messages[0].Content = "bbbb" // Request mutations after commit are not reflected.
	state.TraceChat(span, &TraceChatAttributes{Request: req})

	got := lastAttrStringValue(t, span.attrs, semconvtrace.KeyGenAIInputMessages)
	var messages []telemetryMessage
	if err := json.Unmarshal([]byte(got), &messages); err != nil {
		t.Fatalf("unmarshal input messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Content != "aaaa" {
		t.Fatalf("expected initially committed message in trace, got %+v", messages)
	}
	if countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages) != 1 {
		t.Fatalf("expected input messages to be committed once, got %d", countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages))
	}
}

func TestChatTraceState_PolicyDropSkipsDroppedRequestAttributesOnRefresh(t *testing.T) {
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

func TestChatTraceState_NilRequestThenRequestCommitsPayload(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })
	installChatStreamingPolicyForTest()

	span := newRecordingSpan()
	state := &ChatTraceState{}
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	}

	state.TraceChat(span, &TraceChatAttributes{
		Response: chatResponseForChatStateTest("first"),
	})
	if countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages) != 0 {
		t.Fatalf("expected no input messages before request is committed, got %d", countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages))
	}

	state.TraceChat(span, &TraceChatAttributes{
		Request:  req,
		Response: chatResponseForChatStateTest("second"),
	})
	if countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages) != 1 {
		t.Fatalf("expected input messages after delayed request commit, got %d", countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages))
	}

	state.TraceChat(span, &TraceChatAttributes{
		Request:  req,
		Response: chatResponseForChatStateTest("third"),
	})
	if countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages) != 1 {
		t.Fatalf("expected input messages committed once, got %d", countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages))
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
		Response: chatResponseForChatStateTest("first"),
	})
	state.TraceChat(span, &TraceChatAttributes{
		Request:  req,
		Response: chatResponseForChatStateTest("second"),
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

func TestChatTraceState_NilReceiverUsesStatelessPath(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })
	installChatStreamingPolicyForTest()

	var state *ChatTraceState
	span := newRecordingSpan()
	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hello")}}

	state.TraceChat(span, &TraceChatAttributes{Request: req})
	if countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages) != 1 {
		t.Fatalf("expected input messages attribute from nil ChatTraceState path")
	}
}

func TestTraceChat_NilAttributesSetsBaseSpanAttributes(t *testing.T) {
	span := newRecordingSpan()

	TraceChat(span, nil)

	if countAttr(span.attrs, semconvtrace.KeyGenAISystem) != 1 {
		t.Fatalf("expected gen_ai.system attribute, got %d", countAttr(span.attrs, semconvtrace.KeyGenAISystem))
	}
	if countAttr(span.attrs, semconvtrace.KeyGenAIOperationName) != 1 {
		t.Fatalf("expected gen_ai.operation.name attribute, got %d", countAttr(span.attrs, semconvtrace.KeyGenAIOperationName))
	}
	if countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages) != 0 {
		t.Fatalf("expected no input messages when attributes are nil")
	}
}

func TestChatTraceState_RequestWithOptionalGenerationConfig(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })
	installChatStreamingPolicyForTest()

	fp := 0.1
	mt := 128
	pp := 0.2
	tp := 0.7
	topP := 0.9
	thinkingEnabled := true
	req := &model.Request{
		GenerationConfig: model.GenerationConfig{
			Stop:             []string{"END"},
			Stream:           true,
			FrequencyPenalty: &fp,
			MaxTokens:        &mt,
			PresencePenalty:  &pp,
			Temperature:      &tp,
			TopP:             &topP,
			ThinkingEnabled:  &thinkingEnabled,
		},
		Messages: []model.Message{model.NewUserMessage("hello")},
	}
	span := newRecordingSpan()
	state := &ChatTraceState{}

	state.TraceChat(span, &TraceChatAttributes{Request: req})
	state.TraceChat(span, &TraceChatAttributes{Request: req})

	if countAttr(span.attrs, semconvtrace.KeyGenAIRequestThinkingEnabled) == 0 {
		t.Fatal("expected thinking enabled attribute")
	}
	if countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages) != 1 {
		t.Fatalf("expected input messages committed once, got %d", countAttr(span.attrs, semconvtrace.KeyGenAIInputMessages))
	}
}

func BenchmarkTraceChatStreamingRequestAttributes_Stateless(b *testing.B) {
	benchmarkTraceChatStreamingRequestAttributes(b, false)
}

func BenchmarkTraceChatStreamingRequestAttributes_WithState(b *testing.B) {
	benchmarkTraceChatStreamingRequestAttributes(b, true)
}

func benchmarkTraceChatStreamingRequestAttributes(b *testing.B, useState bool) {
	installChatStreamingPolicyForTest()
	defer SetSpanAttributePolicy(SpanAttributePolicy{})

	req := &model.Request{Messages: multiTurnMessagesForChatStateTest(4)}
	responses := make([]*model.Response, 40)
	for i := range responses {
		responses[i] = chatResponseForChatStateTest(fmt.Sprintf("chunk-%d", i))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		span := newRecordingSpan()
		if useState {
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

func chatResponseForChatStateTest(content string) *model.Response {
	return &model.Response{
		ID:    "response-id",
		Model: "test-model",
		Choices: []model.Choice{{
			Index: 0,
			Delta: model.Message{Role: model.RoleAssistant, Content: content},
		}},
	}
}

func multiTurnMessagesForChatStateTest(turns int) []model.Message {
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
