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
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestBuildRequestAttributes_DropSkipsMarshal(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	var marshalCalls int
	SetSpanAttributePolicy(AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationChat,
		Key:       semconvtrace.KeyGenAIInputMessagesOTel,
		Action:    AttributeDrop,
	}))

	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}
	_ = buildRequestAttributes(req)

	attrs := appendStringAttribute(nil, OperationChat, semconvtrace.KeyGenAIInputMessagesOTel, "", func() ([]byte, error) {
		marshalCalls++
		return []byte(`[]`), nil
	})
	if len(attrs) != 0 {
		t.Fatal("expected drop to skip attribute")
	}
	if marshalCalls != 0 {
		t.Fatal("expected drop to skip marshal")
	}
}

func TestBuildRequestAttributes_DropSkipsAttribute(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	SetSpanAttributePolicy(AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationChat,
		Key:       semconvtrace.KeyGenAIInputMessagesOTel,
		Action:    AttributeDrop,
	}))

	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}
	attrs := buildRequestAttributes(req)
	if _, ok := attrStringValue(attrs, semconvtrace.KeyGenAIInputMessagesOTel); ok {
		t.Fatal("expected otel input messages attribute to be skipped")
	}
	if _, ok := attrStringValue(attrs, semconvtrace.KeyGenAIInputMessages); !ok {
		t.Fatal("expected legacy input messages attribute to remain")
	}
}

func TestAppendStringAttribute_OmitSkipsMarshal(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	var marshalCalls int
	SetSpanAttributePolicy(AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationChat,
		Key:       semconvtrace.KeyGenAIInputMessages,
		Action:    AttributeOmit,
	}))

	attrs := appendStringAttribute(nil, OperationChat, semconvtrace.KeyGenAIInputMessages, "", func() ([]byte, error) {
		marshalCalls++
		return []byte(`{"messages":[]}`), nil
	})
	if marshalCalls != 0 {
		t.Fatal("expected unconditional omit to skip marshal")
	}
	got, ok := attrStringValue(attrs, semconvtrace.KeyGenAIInputMessages)
	if !ok || got != `{"omitted":true}` {
		t.Fatalf("expected omit envelope, got %q ok=%v", got, ok)
	}
}

func TestSetBytesAttribute_MaxBytesOmitOverLimitSkipsMarshal(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	SetSpanAttributePolicy(AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationExecuteTool,
		Key:       semconvtrace.KeyGenAIToolCallArguments,
		Action:    AttributeOmit,
		MaxBytes:  16,
	}))

	span := newRecordingSpan()
	setBytesAttribute(span, OperationExecuteTool, semconvtrace.KeyGenAIToolCallArguments, []byte(strings.Repeat("x", 128)))
	got, ok := attrStringValue(span.attrs, semconvtrace.KeyGenAIToolCallArguments)
	if !ok {
		t.Fatal("expected omitted attribute")
	}
	var envelope AttributeEnvelope
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !envelope.Omitted || envelope.Truncated || envelope.Prefix != "" {
		t.Fatalf("expected omitted envelope, got %+v", envelope)
	}
}

func TestFormatAttributeValue_Truncate(t *testing.T) {
	payload := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("x", 128) + `"}]}`)
	got, ok := formatAttributeValue(payload, AttributeRule{Action: AttributeTruncate, MaxBytes: 32})
	if !ok {
		t.Fatal("expected truncated value")
	}
	var envelope AttributeEnvelope
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !envelope.Truncated || envelope.Omitted {
		t.Fatalf("expected truncated envelope, got %+v", envelope)
	}
	if !utf8.ValidString(envelope.Prefix) {
		t.Fatal("expected utf-8 safe prefix")
	}
	if envelope.OriginalBytes != int64(len(payload)) {
		t.Fatalf("expected original bytes %d, got %d", len(payload), envelope.OriginalBytes)
	}
	if envelope.SHA256 == "" {
		t.Fatal("expected sha256 fingerprint")
	}
}

func TestFormatAttributeValue_OmitOverLimit(t *testing.T) {
	payload := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	got, ok := formatAttributeValue(payload, AttributeRule{Action: AttributeOmit, MaxBytes: 16})
	if !ok {
		t.Fatal("expected omitted value")
	}
	var envelope AttributeEnvelope
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !envelope.Omitted || envelope.Truncated || envelope.Prefix != "" {
		t.Fatalf("expected omitted envelope, got %+v", envelope)
	}
}

func TestAppendStringAttribute_SkipsOnErrorWhenBestEffort(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	attrs := appendStringAttribute(nil, OperationChat, semconvtrace.KeyGenAIOutputMessages, "", func() ([]byte, error) {
		return nil, fmt.Errorf("marshal failed")
	})
	if len(attrs) != 0 {
		t.Fatalf("expected best-effort attribute to be skipped on error, got %d attrs", len(attrs))
	}
}

func TestAppendStringAttribute_PlaceholderOnErrorWhenConfigured(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	attrs := appendStringAttribute(nil, OperationChat, semconvtrace.KeyGenAIInputMessages, "<not json serializable>", func() ([]byte, error) {
		return nil, fmt.Errorf("marshal failed")
	})
	got, ok := attrStringValue(attrs, semconvtrace.KeyGenAIInputMessages)
	if !ok || got != "<not json serializable>" {
		t.Fatalf("expected placeholder on error, got %q ok=%v", got, ok)
	}
}

func TestBuildResponseAttributes_DropSkipsAttribute(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	SetSpanAttributePolicy(AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationChat,
		Key:       semconvtrace.KeyGenAIOutputMessagesOTel,
		Action:    AttributeDrop,
	}))

	rsp := &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hi"}}},
	}
	attrs := buildResponseAttributes(rsp, semconvtrace.ValueDefaultErrorType)
	if _, ok := attrStringValue(attrs, semconvtrace.KeyGenAIOutputMessagesOTel); ok {
		t.Fatal("expected otel output messages attribute to be skipped")
	}
	if _, ok := attrStringValue(attrs, semconvtrace.KeyGenAIOutputMessages); !ok {
		t.Fatal("expected legacy output messages attribute to remain")
	}
}

func TestBuildRequestAttributes_TruncateEnvelope(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	SetSpanAttributePolicy(AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationChat,
		Key:       semconvtrace.KeyGenAIInputMessages,
		Action:    AttributeTruncate,
		MaxBytes:  32,
	}))
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: strings.Repeat("x", 256)}},
	}
	attrs := buildRequestAttributes(req)
	got, ok := attrStringValue(attrs, semconvtrace.KeyGenAIInputMessages)
	if !ok {
		t.Fatal("expected input messages attribute")
	}
	var envelope AttributeEnvelope
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("expected truncated envelope, got %q: %v", got, err)
	}
	if !envelope.Truncated || envelope.Omitted {
		t.Fatalf("expected truncated envelope, got %+v", envelope)
	}
}

func TestFormatAttributeValue_UTF8SafePrefix(t *testing.T) {
	payload := []byte("你好世界")
	got, ok := formatAttributeValue(payload, AttributeRule{Action: AttributeTruncate, MaxBytes: 3})
	if !ok {
		t.Fatal("expected truncated value")
	}
	var envelope AttributeEnvelope
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !utf8.ValidString(envelope.Prefix) {
		t.Fatalf("expected utf-8 safe prefix, got %q", envelope.Prefix)
	}
}

func TestUtf8SafePrefix_EdgeCases(t *testing.T) {
	if got := utf8SafePrefix(nil, 10); got != "" {
		t.Fatalf("expected empty prefix for nil input, got %q", got)
	}
	if got := utf8SafePrefix([]byte("hi"), 0); got != "" {
		t.Fatalf("expected empty prefix for zero limit, got %q", got)
	}
	if got := utf8SafePrefix([]byte("hi"), 10); got != "hi" {
		t.Fatalf("expected full payload within limit, got %q", got)
	}
}

func TestTraceWorkflow_DropRequest(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	SetSpanAttributePolicy(AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationWorkflow,
		Key:       semconvtrace.KeyGenAIWorkflowRequest,
		Action:    AttributeDrop,
	}))

	span := newRecordingSpan()
	TraceWorkflow(span, &Workflow{Name: "wf", Request: map[string]string{"k": "v"}})
	if _, ok := attrStringValue(span.attrs, semconvtrace.KeyGenAIWorkflowRequest); ok {
		t.Fatal("expected workflow request to be dropped")
	}
}

func TestTraceToolCall_DropArguments(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	SetSpanAttributePolicy(AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationExecuteTool,
		Key:       semconvtrace.KeyGenAIToolCallArguments,
		Action:    AttributeDrop,
	}))

	span := newRecordingSpan()
	TraceToolCall(span, nil, &tool.Declaration{Name: "t"}, []byte(`{"a":1}`), nil, nil)
	if _, ok := attrStringValue(span.attrs, semconvtrace.KeyGenAIToolCallArguments); ok {
		t.Fatal("expected tool arguments to be dropped")
	}
}

func TestSetInvokeAgentInputMessageAttributes_DropOTel(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	SetSpanAttributePolicy(AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationInvokeAgent,
		Key:       semconvtrace.KeyGenAIInputMessagesOTel,
		Action:    AttributeDrop,
	}))

	span := newRecordingSpan()
	setInvokeAgentInputMessageAttributes(span, model.Message{Role: model.RoleUser, Content: "hi"})
	if _, ok := attrStringValue(span.attrs, semconvtrace.KeyGenAIInputMessagesOTel); ok {
		t.Fatal("expected invoke otel input to be dropped")
	}
	if _, ok := attrStringValue(span.attrs, semconvtrace.KeyGenAIInputMessages); !ok {
		t.Fatal("expected invoke legacy input to remain")
	}
}
