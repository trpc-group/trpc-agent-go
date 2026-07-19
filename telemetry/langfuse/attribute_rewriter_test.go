//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package langfuse

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

// brandingRename mimics Genie's scrubTrpcPublicIdentity rename of framework keys.
func brandingRename(attrs []attribute.KeyValue) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		key := string(a.Key)
		switch {
		case key == semconvtrace.KeyGenAISystem:
			out = append(out, attribute.String(semconvtrace.KeyGenAISystem, "aiden"))
		case strings.HasPrefix(key, "trpc.go.agent."):
			out = append(out, attribute.KeyValue{
				Key:   attribute.Key("aiden.agent." + strings.TrimPrefix(key, "trpc.go.agent.")),
				Value: a.Value,
			})
		default:
			out = append(out, a)
		}
	}
	return out
}

func TestTransformThenRewrite_DoesNotLeakLLMRequest(t *testing.T) {
	llmBlob := `{"messages":[{"role":"user","content":"hello"}],"generation_config":{"temperature":0.7}}`
	otelMsgs := `[{"role":"user","parts":[{"type":"text","content":"hello"}]}]`

	span := &tracepb.Span{
		Name: "chat gpt",
		Attributes: []*commonpb.KeyValue{
			{
				Key:   semconvtrace.KeyGenAIOperationName,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: itelemetry.OperationChat}},
			},
			{
				Key:   semconvtrace.KeyGenAISystem,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: semconvtrace.SystemTRPCGoAgent}},
			},
			{
				Key:   semconvtrace.KeyLLMRequest,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: llmBlob}},
			},
			{
				Key:   semconvtrace.KeyLLMResponse,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: `{"ok":true}`}},
			},
			{
				Key:   semconvtrace.KeyGenAIInputMessagesOTel,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: otelMsgs}},
			},
			{
				Key:   semconvtrace.KeyEventID,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "evt-1"}},
			},
		},
	}

	transformCallLLM(span)
	span.Attributes = rewriteProtoAttributes(span.Attributes, brandingRename)

	attrMap := map[string]string{}
	for _, attr := range span.Attributes {
		require.NotNil(t, attr.Value)
		attrMap[attr.Key] = attr.Value.GetStringValue()
	}

	require.Contains(t, attrMap, observationInput)
	assert.Contains(t, attrMap[observationInput], "hello")
	assert.Equal(t, "aiden", attrMap[semconvtrace.KeyGenAISystem])
	assert.Equal(t, "evt-1", attrMap["aiden.agent.event_id"])
	assert.NotContains(t, attrMap, semconvtrace.KeyLLMRequest)
	assert.NotContains(t, attrMap, semconvtrace.KeyLLMResponse)
	assert.NotContains(t, attrMap, "aiden.agent.llm_request")
	assert.NotContains(t, attrMap, "aiden.agent.llm_response")
	assert.Equal(t, `{"temperature":0.7}`, attrMap[observationModelParameters])
}

func TestRewriteBeforeTransform_WouldLeakLLMRequest(t *testing.T) {
	// Documents the failure mode fixed by running rewrite after transform.
	llmBlob := `{"messages":[{"role":"user","content":"hello"}]}`
	span := &tracepb.Span{
		Name: "chat gpt",
		Attributes: []*commonpb.KeyValue{
			{
				Key:   semconvtrace.KeyGenAIOperationName,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: itelemetry.OperationChat}},
			},
			{
				Key:   semconvtrace.KeyLLMRequest,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: llmBlob}},
			},
		},
	}

	span.Attributes = rewriteProtoAttributes(span.Attributes, brandingRename)
	transformCallLLM(span)

	attrMap := map[string]string{}
	for _, attr := range span.Attributes {
		attrMap[attr.Key] = attr.Value.GetStringValue()
	}
	assert.Contains(t, attrMap, "aiden.agent.llm_request", "rewrite-before-transform leaks renamed llm_request")
	assert.Equal(t, llmBlob, attrMap["aiden.agent.llm_request"])
}
