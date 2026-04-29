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
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestTransform(t *testing.T) {
	tests := []struct {
		name     string
		input    []*tracepb.ResourceSpans
		expected []*tracepb.ResourceSpans
	}{
		{
			name:     "empty input",
			input:    nil,
			expected: nil,
		},
		{
			name: "nil scope spans and nil spans",
			input: []*tracepb.ResourceSpans{
				{ScopeSpans: []*tracepb.ScopeSpans{nil, {}}},
			},
			expected: []*tracepb.ResourceSpans{
				{ScopeSpans: []*tracepb.ScopeSpans{nil, {}}},
			},
		},
		{
			name:     "empty slice",
			input:    []*tracepb.ResourceSpans{},
			expected: []*tracepb.ResourceSpans{},
		},
		{
			name: "nil resource spans",
			input: []*tracepb.ResourceSpans{
				nil,
			},
			expected: []*tracepb.ResourceSpans{
				nil,
			},
		},
		{
			name: "normal spans without operation name",
			input: []*tracepb.ResourceSpans{
				{
					ScopeSpans: []*tracepb.ScopeSpans{
						{
							Spans: []*tracepb.Span{
								{
									TraceId: []byte("test-trace-id"),
									SpanId:  []byte("test-span-id"),
									Name:    "test-span",
									Attributes: []*commonpb.KeyValue{
										{
											Key: "test.key",
											Value: &commonpb.AnyValue{
												Value: &commonpb.AnyValue_StringValue{StringValue: "test-value"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: []*tracepb.ResourceSpans{
				{
					ScopeSpans: []*tracepb.ScopeSpans{
						{
							Spans: []*tracepb.Span{
								{
									TraceId: []byte("test-trace-id"),
									SpanId:  []byte("test-span-id"),
									Name:    "test-span",
									Attributes: []*commonpb.KeyValue{
										{
											Key: "test.key",
											Value: &commonpb.AnyValue{
												Value: &commonpb.AnyValue_StringValue{StringValue: "test-value"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := transform(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTransformSpan(t *testing.T) {
	tests := []struct {
		name           string
		operationName  string
		inputSpan      *tracepb.Span
		expectedAction string // what transformation should occur
	}{
		{
			name: "span without attributes",
			inputSpan: &tracepb.Span{
				Name: "test-span",
			},
			expectedAction: "no change",
		},
		{
			name: "span without operation name",
			inputSpan: &tracepb.Span{
				Name: "test-span",
				Attributes: []*commonpb.KeyValue{
					{
						Key: "test.key",
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: "test-value"},
						},
					},
				},
			},
			expectedAction: "no change",
		},
		{
			name:          "call_llm operation",
			operationName: itelemetry.OperationChat,
			inputSpan: &tracepb.Span{
				Name: "test-span",
				Attributes: []*commonpb.KeyValue{
					{
						Key: semconvtrace.KeyGenAIOperationName,
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: itelemetry.OperationChat},
						},
					},
				},
			},
			expectedAction: "transform_call_llm",
		},
		{
			name:          "execute_tool operation",
			operationName: itelemetry.OperationExecuteTool,
			inputSpan: &tracepb.Span{
				Name: "test-span",
				Attributes: []*commonpb.KeyValue{
					{
						Key: semconvtrace.KeyGenAIOperationName,
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: itelemetry.OperationExecuteTool},
						},
					},
				},
			},
			expectedAction: "transform_execute_tool",
		},
		{
			name:          "workflow operation",
			operationName: itelemetry.OperationWorkflow,
			inputSpan: &tracepb.Span{
				Name: "test-span",
				Attributes: []*commonpb.KeyValue{
					{
						Key: semconvtrace.KeyGenAIOperationName,
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: itelemetry.OperationWorkflow},
						},
					},
				},
			},
			expectedAction: "transform_workflow",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalLen := len(tt.inputSpan.Attributes)
			transformSpan(tt.inputSpan)

			switch tt.expectedAction {
			case "no change":
				assert.Len(t, tt.inputSpan.Attributes, originalLen)
			case "transform_call_llm":
				// Should have observation type added
				found := false
				for _, attr := range tt.inputSpan.Attributes {
					if attr.Key == observationType && attr.Value.GetStringValue() == "generation" {
						found = true
						break
					}
				}
				assert.True(t, found, "should have observation type 'generation'")
			case "transform_execute_tool":
				// Should have observation type added
				found := false
				for _, attr := range tt.inputSpan.Attributes {
					if attr.Key == observationType && attr.Value.GetStringValue() == "tool" {
						found = true
						break
					}
				}
				assert.True(t, found, "should have observation type 'tool'")
			case "transform_run_runner":
				// Runner transformation has different behavior, check attributes are processed
				assert.NotNil(t, tt.inputSpan.Attributes)
			case "transform_workflow":
				// Should have observation type added
				found := false
				for _, attr := range tt.inputSpan.Attributes {
					if attr.Key == observationType && attr.Value.GetStringValue() == observationTypeChain {
						found = true
						break
					}
				}
				assert.True(t, found, "should have observation type 'chain'")
			}
		})
	}
}

func TestTransformInvokeAgent(t *testing.T) {
	inputMessages := `[{"role":"tool","name":"search","parts":[{"type":"tool_call_response","id":"call-123","response":"ok"}]}]`
	outputChoices := `[{"role":"assistant","parts":[{"type":"text","content":"hello"}],"finish_reason":"stop"}]`
	span := &tracepb.Span{
		Name: "agent-span",
		Attributes: []*commonpb.KeyValue{
			{
				Key: semconvtrace.KeyGenAIInputMessages,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: inputMessages},
				},
			},
			{
				Key: semconvtrace.KeyGenAIOutputMessages,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: outputChoices},
				},
			},
			{
				Key: semconvtrace.KeyGenAIUsageInputTokens,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_IntValue{IntValue: 123},
				},
			},
			{
				Key: semconvtrace.KeyGenAIUsageOutputTokens,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_IntValue{IntValue: 456},
				},
			},
			{
				Key: "other.attribute",
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: "keep-this"},
				},
			},
		},
	}

	transformInvokeAgent(span)

	attrMap := make(map[string]string)
	for _, attr := range span.Attributes {
		if attr.Value != nil {
			attrMap[attr.Key] = attr.Value.GetStringValue()
		}
	}

	assert.Equal(t, observationTypeAgent, attrMap[observationType])
	assert.Equal(t, inputMessages, attrMap[observationInput])
	assert.Equal(t, outputChoices, attrMap[observationOutput])
	assert.Equal(t, "keep-this", attrMap["other.attribute"])

	// Token usage attributes should be filtered out for InvokeAgent observations.
	for _, attr := range span.Attributes {
		assert.NotEqual(t, semconvtrace.KeyGenAIUsageInputTokens, attr.Key)
		assert.NotEqual(t, semconvtrace.KeyGenAIUsageOutputTokens, attr.Key)
	}
}

func TestTransformInvokeAgent_PreservesToolCallToolResponseAndReasoning(t *testing.T) {
	inputMessages := `[{"role":"assistant","parts":[{"type":"text","content":"need tool"},{"type":"reasoning","content":"thinking before call"},{"type":"tool_call","id":"call-123","name":"harness_lookup","arguments":{"key":"otel"}}]},{"role":"tool","name":"harness_lookup","parts":[{"type":"tool_call_response","id":"call-123","response":{"token":"otel-tool-token-7f3b9c1d"}}]}]`
	outputChoices := `[{"role":"assistant","parts":[{"type":"text","content":"otel-tool-token-7f3b9c1d"},{"type":"reasoning","content":"used tool output"}],"finish_reason":"stop"}]`
	span := &tracepb.Span{
		Name: "agent-span",
		Attributes: []*commonpb.KeyValue{
			{
				Key: semconvtrace.KeyGenAIInputMessages,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: inputMessages},
				},
			},
			{
				Key: semconvtrace.KeyGenAIOutputMessages,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: outputChoices},
				},
			},
		},
	}

	transformInvokeAgent(span)

	attrMap := make(map[string]string)
	for _, attr := range span.Attributes {
		if attr.Value != nil {
			attrMap[attr.Key] = attr.Value.GetStringValue()
		}
	}

	assert.Equal(t, observationTypeAgent, attrMap[observationType])
	assert.JSONEq(t, inputMessages, attrMap[observationInput])
	assert.JSONEq(t, outputChoices, attrMap[observationOutput])
}

func TestTransformCallLLM(t *testing.T) {
	tests := []struct {
		name     string
		input    *tracepb.Span
		expected map[string]string // key -> expected value
	}{
		{
			name: "basic LLM call transformation",
			input: &tracepb.Span{
				Name: "llm-call",
				Attributes: []*commonpb.KeyValue{
					{
						Key: semconvtrace.KeyLLMRequest,
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: `{"prompt": "Hello", "generation_config": {"temperature": 0.7}}`},
						},
					},
					{
						Key: semconvtrace.KeyLLMResponse,
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: `{"text": "Hello! How can I help you?"}`},
						},
					},
					{
						Key: "other.attribute",
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: "keep-this"},
						},
					},
				},
			},
			expected: map[string]string{
				observationType:            "generation",
				observationInput:           `{"prompt": "Hello", "generation_config": {"temperature": 0.7}}`,
				observationOutput:          `{"text": "Hello! How can I help you?"}`,
				observationModelParameters: `{"temperature": 0.7}`,
				"other.attribute":          "keep-this",
			},
		},
		{
			name: "LLM call with nil request",
			input: &tracepb.Span{
				Name: "llm-call",
				Attributes: []*commonpb.KeyValue{
					{
						Key:   semconvtrace.KeyLLMRequest,
						Value: nil,
					},
					{
						Key: semconvtrace.KeyLLMResponse,
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: "response"},
						},
					},
				},
			},
			expected: map[string]string{
				observationType:   "generation",
				observationInput:  "N/A",
				observationOutput: "response",
			},
		},
		{
			name: "LLM call with nil response",
			input: &tracepb.Span{
				Name: "llm-call",
				Attributes: []*commonpb.KeyValue{
					{
						Key: semconvtrace.KeyLLMRequest,
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: "request"},
						},
					},
					{
						Key:   semconvtrace.KeyLLMResponse,
						Value: nil,
					},
				},
			},
			expected: map[string]string{
				observationType:   "generation",
				observationInput:  "request",
				observationOutput: "N/A",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transformCallLLM(tt.input)

			// Check that expected attributes are present
			attrMap := make(map[string]string)
			for _, attr := range tt.input.Attributes {
				attrMap[attr.Key] = attr.Value.GetStringValue()
			}

			for key, expectedValue := range tt.expected {
				actualValue, exists := attrMap[key]
				assert.True(t, exists, "attribute %s should exist", key)
				assert.Equal(t, expectedValue, actualValue, "attribute %s value mismatch", key)
			}

			// Check that LLM-specific attributes are removed
			for _, attr := range tt.input.Attributes {
				assert.NotEqual(t, semconvtrace.KeyLLMRequest, attr.Key, "LLM request attribute should be removed")
				assert.NotEqual(t, semconvtrace.KeyLLMResponse, attr.Key, "LLM response attribute should be removed")
			}
		})
	}
}

func TestTransformCallLLM_PromptWithTools(t *testing.T) {
	toolDefs := []*tool.Declaration{
		{
			Name:        "alpha",
			Description: "first",
			InputSchema: &tool.Schema{Type: "object"},
		},
		{
			Name:        "beta",
			Description: "second",
			InputSchema: &tool.Schema{Type: "object"},
		},
	}
	defsJSON, err := json.Marshal(toolDefs)
	require.NoError(t, err)

	span := &tracepb.Span{
		Name: "llm-call",
		Attributes: []*commonpb.KeyValue{
			{
				Key: semconvtrace.KeyLLMRequest,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: `{"generation_config": {"temperature": 0.7}}`},
				},
			},
			{
				Key: semconvtrace.KeyGenAIInputMessages,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: `[{"role":"user","parts":[{"type":"text","content":"hi"}]}]`},
				},
			},
			{
				Key: semconvtrace.KeyGenAIRequestToolDefinitions,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: string(defsJSON)},
				},
			},
		},
	}

	transformCallLLM(span)

	attrMap := make(map[string]string)
	for _, attr := range span.Attributes {
		attrMap[attr.Key] = attr.Value.GetStringValue()
		assert.NotEqual(t, semconvtrace.KeyGenAIInputMessages, attr.Key, "input messages should be folded into observation.input")
		assert.NotEqual(t, semconvtrace.KeyGenAIRequestToolDefinitions, attr.Key, "tool definitions should be folded into observation.input")
	}

	inputStr := attrMap[observationInput]
	require.NotEmpty(t, inputStr)

	var input map[string]any
	require.NoError(t, json.Unmarshal([]byte(inputStr), &input))

	msgs, ok := input["messages"].([]any)
	require.True(t, ok)
	require.Len(t, msgs, 1)

	toolsVal, ok := input["tools"].([]any)
	require.True(t, ok)
	require.Len(t, toolsVal, 2)

	seen := map[string]bool{}
	for _, tv := range toolsVal {
		m, ok := tv.(map[string]any)
		require.True(t, ok)
		name, _ := m["name"].(string)
		seen[name] = true
	}
	require.True(t, seen["alpha"])
	require.True(t, seen["beta"])

	// generation_config is still extracted from llm_request.
	assert.Equal(t, `{"temperature": 0.7}`, attrMap[observationModelParameters])
}

func TestTransformCallLLM_UsageDetails(t *testing.T) {
	tests := []struct {
		name                string
		inputTokens         int64
		outputTokens        int64
		cachedTokens        int64
		cacheReadTokens     int64
		cacheCreationTokens int64
		expectedUsage       map[string]int64
	}{
		{
			name:          "basic input/output tokens",
			inputTokens:   100,
			outputTokens:  50,
			expectedUsage: map[string]int64{"input": 100, "output": 50},
		},
		{
			name:          "with OpenAI cached tokens",
			inputTokens:   100,
			outputTokens:  50,
			cachedTokens:  30,
			expectedUsage: map[string]int64{"input": 100, "output": 50, "input_cached": 30},
		},
		{
			name:            "with Anthropic cache_read tokens",
			inputTokens:     200,
			outputTokens:    80,
			cacheReadTokens: 60,
			expectedUsage:   map[string]int64{"input": 200, "output": 80, "input_cache_read": 60},
		},
		{
			name:                "with Anthropic cache_creation tokens",
			inputTokens:         200,
			outputTokens:        80,
			cacheCreationTokens: 40,
			expectedUsage:       map[string]int64{"input": 200, "output": 80, "input_cache_creation": 40},
		},
		{
			name:                "all cache fields present",
			inputTokens:         300,
			outputTokens:        100,
			cachedTokens:        50,
			cacheReadTokens:     70,
			cacheCreationTokens: 20,
			expectedUsage:       map[string]int64{"input": 300, "output": 100, "input_cached": 50, "input_cache_read": 70, "input_cache_creation": 20},
		},
		{
			name:          "zero tokens omitted",
			inputTokens:   0,
			outputTokens:  0,
			expectedUsage: nil, // no usage_details attribute when all zero
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := []*commonpb.KeyValue{
				{
					Key: semconvtrace.KeyLLMRequest,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: `{"prompt":"hello"}`},
					},
				},
				{
					Key: semconvtrace.KeyLLMResponse,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: `{"text":"world"}`},
					},
				},
			}
			// Add token usage attributes (only non-zero values, matching how buildResponseAttributes works)
			if tt.inputTokens != 0 {
				attrs = append(attrs, &commonpb.KeyValue{
					Key:   semconvtrace.KeyGenAIUsageInputTokens,
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: tt.inputTokens}},
				})
			}
			if tt.outputTokens != 0 {
				attrs = append(attrs, &commonpb.KeyValue{
					Key:   semconvtrace.KeyGenAIUsageOutputTokens,
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: tt.outputTokens}},
				})
			}
			if tt.cachedTokens != 0 {
				attrs = append(attrs, &commonpb.KeyValue{
					Key:   semconvtrace.KeyGenAIUsageInputTokensCached,
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: tt.cachedTokens}},
				})
			}
			if tt.cacheReadTokens != 0 {
				attrs = append(attrs, &commonpb.KeyValue{
					Key:   semconvtrace.KeyGenAIUsageInputTokensCacheRead,
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: tt.cacheReadTokens}},
				})
			}
			if tt.cacheCreationTokens != 0 {
				attrs = append(attrs, &commonpb.KeyValue{
					Key:   semconvtrace.KeyGenAIUsageInputTokensCacheCreation,
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: tt.cacheCreationTokens}},
				})
			}

			span := &tracepb.Span{Name: "llm-call", Attributes: attrs}
			transformCallLLM(span)

			// Verify original gen_ai.usage.* attributes are removed
			for _, attr := range span.Attributes {
				assert.NotEqual(t, semconvtrace.KeyGenAIUsageInputTokens, attr.Key)
				assert.NotEqual(t, semconvtrace.KeyGenAIUsageOutputTokens, attr.Key)
				assert.NotEqual(t, semconvtrace.KeyGenAIUsageInputTokensCached, attr.Key)
				assert.NotEqual(t, semconvtrace.KeyGenAIUsageInputTokensCacheRead, attr.Key)
				assert.NotEqual(t, semconvtrace.KeyGenAIUsageInputTokensCacheCreation, attr.Key)
			}

			// Check usage_details attribute
			var usageJSON string
			for _, attr := range span.Attributes {
				if attr.Key == observationUsageDetails {
					usageJSON = attr.Value.GetStringValue()
					break
				}
			}

			if tt.expectedUsage == nil {
				assert.Empty(t, usageJSON, "should not have usage_details when all tokens are zero")
			} else {
				require.NotEmpty(t, usageJSON, "should have usage_details attribute")
				var actual map[string]int64
				require.NoError(t, json.Unmarshal([]byte(usageJSON), &actual))
				assert.Equal(t, tt.expectedUsage, actual)
			}
		})
	}
}

func TestTransformInvokeAgent_CacheTokensFiltered(t *testing.T) {
	span := &tracepb.Span{
		Name: "agent-span",
		Attributes: []*commonpb.KeyValue{
			{
				Key: semconvtrace.KeyGenAIInputMessages,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: `[{"role":"user","parts":[{"type":"text","content":"hi"}]}]`},
				},
			},
			{
				Key: semconvtrace.KeyGenAIOutputMessages,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: `[{"role":"assistant","parts":[{"type":"text","content":"hello"}],"finish_reason":"stop"}]`},
				},
			},
			{
				Key:   semconvtrace.KeyGenAIUsageInputTokens,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 100}},
			},
			{
				Key:   semconvtrace.KeyGenAIUsageOutputTokens,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 50}},
			},
			{
				Key:   semconvtrace.KeyGenAIUsageInputTokensCached,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 30}},
			},
			{
				Key:   semconvtrace.KeyGenAIUsageInputTokensCacheRead,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 20}},
			},
			{
				Key:   semconvtrace.KeyGenAIUsageInputTokensCacheCreation,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 10}},
			},
		},
	}

	transformInvokeAgent(span)

	// All token usage attributes (including cache ones) should be filtered out
	for _, attr := range span.Attributes {
		assert.NotEqual(t, semconvtrace.KeyGenAIUsageInputTokens, attr.Key)
		assert.NotEqual(t, semconvtrace.KeyGenAIUsageOutputTokens, attr.Key)
		assert.NotEqual(t, semconvtrace.KeyGenAIUsageInputTokensCached, attr.Key)
		assert.NotEqual(t, semconvtrace.KeyGenAIUsageInputTokensCacheRead, attr.Key)
		assert.NotEqual(t, semconvtrace.KeyGenAIUsageInputTokensCacheCreation, attr.Key)
	}
}

func TestTransformExecuteTool(t *testing.T) {
	tests := []struct {
		name     string
		input    *tracepb.Span
		expected map[string]string
	}{
		{
			name: "basic tool execution transformation",
			input: &tracepb.Span{
				Name: "tool-call",
				Attributes: []*commonpb.KeyValue{
					{
						Key: semconvtrace.KeyGenAIToolCallArguments,
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: `{"arg1": "value1"}`},
						},
					},
					{
						Key: semconvtrace.KeyGenAIToolCallResult,
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: `{"result": "success"}`},
						},
					},
					{
						Key: "other.attribute",
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: "keep-this"},
						},
					},
				},
			},
			expected: map[string]string{
				observationType:   "tool",
				observationInput:  `{"arg1": "value1"}`,
				observationOutput: `{"result": "success"}`,
				"other.attribute": "keep-this",
			},
		},
		{
			name: "tool execution with nil args",
			input: &tracepb.Span{
				Name: "tool-call",
				Attributes: []*commonpb.KeyValue{
					{
						Key:   semconvtrace.KeyGenAIToolCallArguments,
						Value: nil,
					},
					{
						Key: semconvtrace.KeyGenAIToolCallResult,
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: "response"},
						},
					},
				},
			},
			expected: map[string]string{
				observationType:   "tool",
				observationInput:  "N/A",
				observationOutput: "response",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transformExecuteTool(tt.input)

			// Check that expected attributes are present
			attrMap := make(map[string]string)
			for _, attr := range tt.input.Attributes {
				attrMap[attr.Key] = attr.Value.GetStringValue()
			}

			for key, expectedValue := range tt.expected {
				actualValue, exists := attrMap[key]
				assert.True(t, exists, "attribute %s should exist", key)
				assert.Equal(t, expectedValue, actualValue, "attribute %s value mismatch", key)
			}

			// Check that tool-specific attributes are removed
			for _, attr := range tt.input.Attributes {
				assert.NotEqual(t, semconvtrace.KeyGenAIToolCallArguments, attr.Key, "tool args attribute should be removed")
				assert.NotEqual(t, semconvtrace.KeyGenAIToolCallResult, attr.Key, "tool response attribute should be removed")
			}
		})
	}
}

func TestTransformWorkflow(t *testing.T) {
	span := &tracepb.Span{
		Name: "workflow-span",
		Attributes: []*commonpb.KeyValue{
			{
				Key: semconvtrace.KeyRunnerSessionID,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: "sess-1"},
				},
			},
			{
				Key: semconvtrace.KeyRunnerUserID,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: "user-1"},
				},
			},
			{
				Key: semconvtrace.KeyGenAIWorkflowRequest,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: `{"req":true}`},
				},
			},
			{
				Key: semconvtrace.KeyGenAIWorkflowResponse,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: `{"ok":true}`},
				},
			},
			{
				Key: "other.attribute",
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: "keep-this"},
				},
			},
		},
	}

	transformWorkflow(span)

	attrMap := make(map[string]string)
	for _, attr := range span.Attributes {
		if attr.Value != nil {
			attrMap[attr.Key] = attr.Value.GetStringValue()
		}
	}

	assert.Equal(t, observationTypeChain, attrMap[observationType])
	assert.Equal(t, `{"req":true}`, attrMap[observationInput])
	assert.Equal(t, `{"ok":true}`, attrMap[observationOutput])
	assert.Equal(t, "user-1", attrMap[traceUserID])
	assert.Equal(t, "sess-1", attrMap[traceSessionID])
	assert.Equal(t, "keep-this", attrMap["other.attribute"])

	for _, attr := range span.Attributes {
		assert.NotEqual(t, semconvtrace.KeyGenAIWorkflowRequest, attr.Key)
		assert.NotEqual(t, semconvtrace.KeyGenAIWorkflowResponse, attr.Key)
		assert.NotEqual(t, semconvtrace.KeyRunnerSessionID, attr.Key)
		assert.NotEqual(t, semconvtrace.KeyRunnerUserID, attr.Key)
	}
}

func TestTransformWorkflow_NilRequestResponse(t *testing.T) {
	span := &tracepb.Span{
		Name: "workflow-span-nil",
		Attributes: []*commonpb.KeyValue{
			{
				Key: semconvtrace.KeyRunnerSessionID,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: "sess-2"},
				},
			},
			{
				Key: semconvtrace.KeyRunnerUserID,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: "user-2"},
				},
			},
			{
				Key:   semconvtrace.KeyGenAIWorkflowRequest,
				Value: nil,
			},
			{
				Key:   semconvtrace.KeyGenAIWorkflowResponse,
				Value: nil,
			},
		},
	}

	transformWorkflow(span)

	attrMap := make(map[string]string)
	for _, attr := range span.Attributes {
		if attr.Value != nil {
			attrMap[attr.Key] = attr.Value.GetStringValue()
		}
	}

	assert.Equal(t, observationTypeChain, attrMap[observationType])
	assert.Equal(t, "N/A", attrMap[observationInput])
	assert.Equal(t, "N/A", attrMap[observationOutput])
	assert.Equal(t, "user-2", attrMap[traceUserID])
	assert.Equal(t, "sess-2", attrMap[traceSessionID])

	for _, attr := range span.Attributes {
		assert.NotEqual(t, semconvtrace.KeyGenAIWorkflowRequest, attr.Key)
		assert.NotEqual(t, semconvtrace.KeyGenAIWorkflowResponse, attr.Key)
		assert.NotEqual(t, semconvtrace.KeyRunnerSessionID, attr.Key)
		assert.NotEqual(t, semconvtrace.KeyRunnerUserID, attr.Key)
	}
}

// Mock client for testing
type mockClient struct {
	started bool
}

func (m *mockClient) Start(ctx context.Context) error {
	m.started = true
	return nil
}

func (m *mockClient) Stop(ctx context.Context) error {
	m.started = false
	return nil
}

func (m *mockClient) UploadTraces(ctx context.Context, spans []*tracepb.ResourceSpans) error {
	return nil
}

var _ interface {
	Start(context.Context) error
	Stop(context.Context) error
	UploadTraces(context.Context, []*tracepb.ResourceSpans) error
} = (*mockClient)(nil)

func TestExporterLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("start and shutdown", func(t *testing.T) {
		client := &mockClient{}
		exp := &exporter{client: client}

		// Test start
		err := exp.Start(ctx)
		assert.NoError(t, err)
		assert.True(t, exp.started)
		assert.True(t, client.started)

		// Test double start
		err = exp.Start(ctx)
		assert.Equal(t, errAlreadyStarted, err)

		// Test shutdown
		err = exp.Shutdown(ctx)
		assert.NoError(t, err)
		assert.False(t, exp.started)
		assert.False(t, client.started)

		// Test double shutdown (should not error)
		err = exp.Shutdown(ctx)
		assert.NoError(t, err)
	})

	t.Run("shutdown without start", func(t *testing.T) {
		client := &mockClient{}
		exp := &exporter{client: client}
		err := exp.Shutdown(ctx)
		assert.NoError(t, err)
	})
}

func TestExporterMarshalLog(t *testing.T) {
	client := &mockClient{}
	exp := &exporter{client: client}
	logData := exp.MarshalLog()

	// Check that it returns some kind of struct
	require.NotNil(t, logData, "MarshalLog should return a non-nil value")

	// Use reflection to check the struct fields since we can't predict the exact anonymous struct type
	logValue := reflect.ValueOf(logData)
	require.Equal(t, reflect.Struct, logValue.Kind(), "should return a struct")

	// Check for Type field
	typeField := logValue.FieldByName("Type")
	require.True(t, typeField.IsValid(), "should have Type field")
	assert.Equal(t, "otlptrace", typeField.String())

	// Check for Client field
	clientField := logValue.FieldByName("Client")
	require.True(t, clientField.IsValid(), "should have Client field")
	assert.Equal(t, client, clientField.Interface())
}

func TestExporterExportSpans(t *testing.T) {
	client := &mockClient{}
	exp := &exporter{client: client}

	ctx := context.Background()

	// Test with empty spans
	err := exp.ExportSpans(ctx, nil)
	assert.NoError(t, err)

	// Note: We can't easily test with real ReadOnlySpan objects without more complex setup
	// The transformation logic is already tested in the other test functions
}

// mockErrorClient returns error on upload

type mockErrorClient struct{}

func (m *mockErrorClient) Start(ctx context.Context) error { return nil }
func (m *mockErrorClient) Stop(ctx context.Context) error  { return nil }
func (m *mockErrorClient) UploadTraces(ctx context.Context, spans []*tracepb.ResourceSpans) error {
	return fmt.Errorf("upload failed")
}

func TestExporterExportSpans_Error(t *testing.T) {
	exp := &exporter{client: &mockErrorClient{}}
	err := exp.ExportSpans(context.Background(), nil)
	assert.Error(t, err)
}

// Integration test for the full transformation pipeline
func TestTransformationPipeline(t *testing.T) {
	// Create test data that simulates what tracetransform.Spans would produce
	protoSpans := []*tracepb.ResourceSpans{
		{
			ScopeSpans: []*tracepb.ScopeSpans{
				{
					Spans: []*tracepb.Span{
						{
							TraceId: []byte("test-trace-id-123456"),
							SpanId:  []byte("test-span-id-123"),
							Name:    "test-llm-span",
							Attributes: []*commonpb.KeyValue{
								{
									Key: semconvtrace.KeyGenAIOperationName,
									Value: &commonpb.AnyValue{
										Value: &commonpb.AnyValue_StringValue{StringValue: itelemetry.OperationChat},
									},
								},
								{
									Key: semconvtrace.KeyLLMRequest,
									Value: &commonpb.AnyValue{
										Value: &commonpb.AnyValue_StringValue{StringValue: `{"prompt": "test"}`},
									},
								},
								{
									Key: semconvtrace.KeyLLMResponse,
									Value: &commonpb.AnyValue{
										Value: &commonpb.AnyValue_StringValue{StringValue: `{"text": "response"}`},
									},
								},
								{
									Key: "service.name",
									Value: &commonpb.AnyValue{
										Value: &commonpb.AnyValue_StringValue{StringValue: "test-service"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Apply transformation
	transformedSpans := transform(protoSpans)
	require.Len(t, transformedSpans, 1)

	// Verify transformation occurred
	resourceSpan := transformedSpans[0]
	require.Len(t, resourceSpan.ScopeSpans, 1)
	require.Len(t, resourceSpan.ScopeSpans[0].Spans, 1)

	transformedSpan := resourceSpan.ScopeSpans[0].Spans[0]

	// Check that observation type was added
	foundObservationType := false
	hasLLMRequest := false
	hasLLMResponse := false
	hasObservationInput := false
	hasObservationOutput := false
	hasServiceName := false

	for _, attr := range transformedSpan.Attributes {
		switch attr.Key {
		case observationType:
			foundObservationType = true
			assert.Equal(t, "generation", attr.Value.GetStringValue())
		case semconvtrace.KeyLLMRequest:
			hasLLMRequest = true
		case semconvtrace.KeyLLMResponse:
			hasLLMResponse = true
		case observationInput:
			hasObservationInput = true
			assert.Equal(t, `{"prompt": "test"}`, attr.Value.GetStringValue())
		case observationOutput:
			hasObservationOutput = true
			assert.Equal(t, `{"text": "response"}`, attr.Value.GetStringValue())
		case "service.name":
			hasServiceName = true
			assert.Equal(t, "test-service", attr.Value.GetStringValue())
		}
	}

	assert.True(t, foundObservationType, "should have observation type 'generation'")
	assert.False(t, hasLLMRequest, "original LLM request attribute should be removed")
	assert.False(t, hasLLMResponse, "original LLM response attribute should be removed")
	assert.True(t, hasObservationInput, "should have observation input")
	assert.True(t, hasObservationOutput, "should have observation output")
	assert.True(t, hasServiceName, "should keep other attributes like service.name")
}

func withObservationMaxBytes(t *testing.T, maxBytes int) {
	old := getObservationMaxBytes()
	v := maxBytes
	setObservationMaxBytes(&v)
	t.Cleanup(func() {
		if old < 0 {
			setObservationMaxBytes(nil)
			return
		}
		ov := old
		setObservationMaxBytes(&ov)
	})
}

func TestTruncateObservationValue_DisabledByNil(t *testing.T) {
	const maxBytes = 32 * 1024

	setObservationMaxBytes(nil)

	big := strings.Repeat("a", maxBytes*2)
	out := truncateObservationValue(big)
	require.Equal(t, big, out)
}

func TestTruncateObservationValue_ZeroMeansTruncateAll(t *testing.T) {
	withObservationMaxBytes(t, 0)

	out := truncateObservationValue("abc")
	require.Equal(t, "", out)
}

func TestTruncateObservationValue_UTF8AndMaxBytes(t *testing.T) {
	const maxBytes = 32 * 1024

	withObservationMaxBytes(t, maxBytes)

	// Use multi-byte characters to ensure we never cut into an invalid UTF-8 rune.
	big := strings.Repeat("中", maxBytes)
	out := truncateObservationValue(big)
	require.LessOrEqual(t, len([]byte(out)), maxBytes)
	require.True(t, utf8.ValidString(out))
	require.Contains(t, out, defaultTruncateMarker)
}

func TestTransformInvokeAgent_TruncatesObservationInputOutput(t *testing.T) {
	const maxBytes = 32 * 1024

	withObservationMaxBytes(t, maxBytes)

	bigIn := strings.Repeat("in-", maxBytes)
	bigOut := strings.Repeat("out-", maxBytes)
	span := &tracepb.Span{
		Name: "agent-span",
		Attributes: []*commonpb.KeyValue{
			{
				Key:   semconvtrace.KeyGenAIInputMessages,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: bigIn}},
			},
			{
				Key:   semconvtrace.KeyGenAIOutputMessages,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: bigOut}},
			},
		},
	}

	transformInvokeAgent(span)

	attrMap := make(map[string]string)
	for _, attr := range span.Attributes {
		attrMap[attr.Key] = attr.Value.GetStringValue()
	}

	in := attrMap[observationInput]
	out := attrMap[observationOutput]
	require.LessOrEqual(t, len([]byte(in)), maxBytes)
	require.LessOrEqual(t, len([]byte(out)), maxBytes)
	require.True(t, utf8.ValidString(in))
	require.True(t, utf8.ValidString(out))
}

func TestTransformInvokeAgent_TruncateUsesOTelMessages(t *testing.T) {
	const maxBytes = 1024

	withObservationMaxBytes(t, maxBytes)

	text := strings.Repeat("中", maxBytes)
	input := []itelemetry.OTelInputMessage{{
		Role: model.RoleUser,
		Parts: []itelemetry.OTelMessagePart{
			{
				Type:    "text",
				Content: text,
			},
			{
				Type:     "blob",
				Modality: "file",
				MIMEType: "application/octet-stream",
				Filename: "large.bin",
				Content:  strings.Repeat("a", maxBytes*8),
			},
		},
	}}
	inputJSON, err := json.Marshal(input)
	require.NoError(t, err)

	output := []itelemetry.OTelOutputMessage{{
		Role: model.RoleAssistant,
		Parts: []itelemetry.OTelMessagePart{{
			Type:    "text",
			Content: strings.Repeat("resp-", maxBytes),
		}},
		FinishReason: "stop",
	}}
	outputJSON, err := json.Marshal(output)
	require.NoError(t, err)

	span := &tracepb.Span{
		Name: "agent-span-typed",
		Attributes: []*commonpb.KeyValue{
			{
				Key:   semconvtrace.KeyGenAIInputMessages,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: string(inputJSON)}},
			},
			{
				Key:   semconvtrace.KeyGenAIOutputMessages,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: string(outputJSON)}},
			},
		},
	}

	transformInvokeAgent(span)

	attrMap := make(map[string]string)
	for _, attr := range span.Attributes {
		attrMap[attr.Key] = attr.Value.GetStringValue()
	}

	in := attrMap[observationInput]
	out := attrMap[observationOutput]

	var gotIn []itelemetry.OTelInputMessage
	require.NoError(t, json.Unmarshal([]byte(in), &gotIn))
	require.Len(t, gotIn, 1)
	require.Equal(t, model.RoleUser, gotIn[0].Role)
	require.Len(t, gotIn[0].Parts, 2)
	require.Equal(t, "text", gotIn[0].Parts[0].Type)
	require.LessOrEqual(t, len([]byte(gotIn[0].Parts[0].Content)), maxBytes)
	require.Equal(t, "blob", gotIn[0].Parts[1].Type)
	require.Equal(t, "large.bin", gotIn[0].Parts[1].Filename)
	require.Less(t, len(gotIn[0].Parts[1].Content), maxBytes*8)
	require.LessOrEqual(t, len([]byte(gotIn[0].Parts[1].Content)), maxBytes)

	var gotOut []itelemetry.OTelOutputMessage
	require.NoError(t, json.Unmarshal([]byte(out), &gotOut))
	require.Len(t, gotOut, 1)
	require.Equal(t, model.RoleAssistant, gotOut[0].Role)
	require.Len(t, gotOut[0].Parts, 1)
	require.Equal(t, "text", gotOut[0].Parts[0].Type)
	require.LessOrEqual(t, len([]byte(gotOut[0].Parts[0].Content)), maxBytes)
}

func TestTransformCallLLM_TruncatesObservationInputOutput(t *testing.T) {
	const maxBytes = 32 * 1024

	withObservationMaxBytes(t, maxBytes)

	bigReq := strings.Repeat("req-", maxBytes)
	bigResp := strings.Repeat("resp-", maxBytes)
	span := &tracepb.Span{
		Name: "llm-call",
		Attributes: []*commonpb.KeyValue{
			{
				Key:   semconvtrace.KeyGenAIOperationName,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: itelemetry.OperationChat}},
			},
			{
				Key:   semconvtrace.KeyLLMRequest,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: bigReq}},
			},
			{
				Key:   semconvtrace.KeyLLMResponse,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: bigResp}},
			},
		},
	}

	transformCallLLM(span)

	attrMap := make(map[string]string)
	for _, attr := range span.Attributes {
		attrMap[attr.Key] = attr.Value.GetStringValue()
	}

	in := attrMap[observationInput]
	out := attrMap[observationOutput]
	require.LessOrEqual(t, len([]byte(in)), maxBytes)
	require.LessOrEqual(t, len([]byte(out)), maxBytes)
	require.True(t, utf8.ValidString(in))
	require.True(t, utf8.ValidString(out))
}

func TestTransformExecuteTool_TruncatesObservationInputOutput(t *testing.T) {
	const maxBytes = 32 * 1024

	withObservationMaxBytes(t, maxBytes)

	bigArgs := strings.Repeat("arg-", maxBytes)
	bigRes := strings.Repeat("res-", maxBytes)
	span := &tracepb.Span{
		Name: "tool-call",
		Attributes: []*commonpb.KeyValue{
			{
				Key:   semconvtrace.KeyGenAIToolCallArguments,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: bigArgs}},
			},
			{
				Key:   semconvtrace.KeyGenAIToolCallResult,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: bigRes}},
			},
		},
	}

	transformExecuteTool(span)

	attrMap := make(map[string]string)
	for _, attr := range span.Attributes {
		attrMap[attr.Key] = attr.Value.GetStringValue()
	}

	in := attrMap[observationInput]
	out := attrMap[observationOutput]
	require.LessOrEqual(t, len([]byte(in)), maxBytes)
	require.LessOrEqual(t, len([]byte(out)), maxBytes)
	require.True(t, utf8.ValidString(in))
	require.True(t, utf8.ValidString(out))
}

func TestTransformWorkflow_TruncatesObservationInputOutput(t *testing.T) {
	const maxBytes = 32 * 1024

	withObservationMaxBytes(t, maxBytes)

	bigReq := strings.Repeat("req-", maxBytes)
	bigResp := strings.Repeat("resp-", maxBytes)
	span := &tracepb.Span{
		Name: "workflow-span",
		Attributes: []*commonpb.KeyValue{
			{
				Key:   semconvtrace.KeyGenAIWorkflowRequest,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: bigReq}},
			},
			{
				Key:   semconvtrace.KeyGenAIWorkflowResponse,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: bigResp}},
			},
		},
	}

	transformWorkflow(span)

	attrMap := make(map[string]string)
	for _, attr := range span.Attributes {
		attrMap[attr.Key] = attr.Value.GetStringValue()
	}

	in := attrMap[observationInput]
	out := attrMap[observationOutput]
	require.LessOrEqual(t, len([]byte(in)), maxBytes)
	require.LessOrEqual(t, len([]byte(out)), maxBytes)
	require.True(t, utf8.ValidString(in))
	require.True(t, utf8.ValidString(out))
}

// Benchmark tests
func BenchmarkTransform(b *testing.B) {
	// Create test data
	resourceSpans := []*tracepb.ResourceSpans{
		{
			ScopeSpans: []*tracepb.ScopeSpans{
				{
					Spans: []*tracepb.Span{
						{
							TraceId: []byte("test-trace-id"),
							SpanId:  []byte("test-span-id"),
							Name:    "test-span",
							Attributes: []*commonpb.KeyValue{
								{
									Key: semconvtrace.KeyGenAIOperationName,
									Value: &commonpb.AnyValue{
										Value: &commonpb.AnyValue_StringValue{StringValue: itelemetry.OperationChat},
									},
								},
								{
									Key: semconvtrace.KeyLLMRequest,
									Value: &commonpb.AnyValue{
										Value: &commonpb.AnyValue_StringValue{StringValue: `{"prompt": "test"}`},
									},
								},
								{
									Key: semconvtrace.KeyLLMResponse,
									Value: &commonpb.AnyValue{
										Value: &commonpb.AnyValue_StringValue{StringValue: `{"text": "response"}`},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Make a copy for each iteration to avoid modifying the original
		spans := make([]*tracepb.ResourceSpans, len(resourceSpans))
		for j, rs := range resourceSpans {
			spans[j] = &tracepb.ResourceSpans{
				ScopeSpans: make([]*tracepb.ScopeSpans, len(rs.ScopeSpans)),
			}
			for k, ss := range rs.ScopeSpans {
				spans[j].ScopeSpans[k] = &tracepb.ScopeSpans{
					Spans: make([]*tracepb.Span, len(ss.Spans)),
				}
				for l, span := range ss.Spans {
					spans[j].ScopeSpans[k].Spans[l] = &tracepb.Span{
						TraceId:    span.TraceId,
						SpanId:     span.SpanId,
						Name:       span.Name,
						Attributes: make([]*commonpb.KeyValue, len(span.Attributes)),
					}
					copy(spans[j].ScopeSpans[k].Spans[l].Attributes, span.Attributes)
				}
			}
		}
		transform(spans)
	}
}

func TestTruncateObservationLLMInput_Branches(t *testing.T) {
	withObservationMaxBytes(t, 64)

	messages := `[{"role":"user","content":"` + strings.Repeat("中", 200) + `"}]`
	otelMessages := `[{"role":"user","parts":[{"type":"text","content":"` + strings.Repeat("中", 200) + `"},{"type":"tool_call","id":"call-123","name":"search","arguments":{"q":"` + strings.Repeat("q", 200) + `"}}]}]`
	tools := `[{"name":"` + strings.Repeat("tool", 40) + `"}]`

	t.Run("prompt with tools and messages", func(t *testing.T) {
		raw, err := buildObservationInputPrompt(messages, tools)
		require.NoError(t, err)

		out := truncateObservationLLMInput(raw)
		require.True(t, utf8.ValidString(out))
		require.Less(t, len([]byte(out)), len([]byte(raw)))
		require.Contains(t, out, "truncated")
		var v map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &v))
	})

	t.Run("plain messages array", func(t *testing.T) {
		out := truncateObservationLLMInput(messages)
		require.True(t, utf8.ValidString(out))
		require.Less(t, len([]byte(out)), len([]byte(messages)))
		require.Contains(t, out, "truncated")
		var v []map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &v))
	})

	t.Run("plain otel messages array preserves parts", func(t *testing.T) {
		out := truncateObservationLLMInput(otelMessages)
		require.True(t, utf8.ValidString(out))
		require.Less(t, len([]byte(out)), len([]byte(otelMessages)))
		require.Contains(t, out, "truncated")

		var v []map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &v))
		require.Len(t, v, 1)
		parts, ok := v[0]["parts"].([]any)
		require.True(t, ok)
		require.Len(t, parts, 2)

		textPart, ok := parts[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "text", textPart["type"])

		toolCallPart, ok := parts[1].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "tool_call", toolCallPart["type"])
		require.Equal(t, "call-123", toolCallPart["id"])
		require.Equal(t, "search", toolCallPart["name"])

		arguments, ok := toolCallPart["arguments"].(map[string]any)
		require.True(t, ok)
		require.Contains(t, arguments["q"], "truncated")
	})

	t.Run("fallback json leaf truncation", func(t *testing.T) {
		raw := `{"k":"` + strings.Repeat("v", 200) + `"}`
		out := truncateObservationLLMInput(raw)
		require.True(t, utf8.ValidString(out))
		require.Less(t, len([]byte(out)), len([]byte(raw)))
		require.Contains(t, out, "truncated")
		var v map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &v))
	})
}

func TestTruncateObservationJSONLeafValues_And_TruncateJSONLeafValue(t *testing.T) {
	withObservationMaxBytes(t, 32)

	nested := `{"a":"` + strings.Repeat("x", 120) + `","b":["` + strings.Repeat("y", 120) + `"],"c":{"d":"` + strings.Repeat("z", 120) + `"}}`
	out := truncateObservationJSONLeafValues(nested)
	require.True(t, utf8.ValidString(out))
	require.Contains(t, out, "truncated")
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))

	invalid := strings.Repeat("not-json", 20)
	outInvalid := truncateObservationJSONLeafValues(invalid)
	require.LessOrEqual(t, len([]byte(outInvalid)), 32)
	require.True(t, utf8.ValidString(outInvalid))
}

func TestBuildAndExtractHelpers_Branches(t *testing.T) {
	_, err := buildObservationInputPrompt(`[{"role":"user"}]`, `{`)
	require.Error(t, err)

	msgs, ok := extractMessagesJSONFromRequestJSON("")
	require.False(t, ok)
	require.Equal(t, "", msgs)

	msgs, ok = extractMessagesJSONFromRequestJSON("not-json")
	require.False(t, ok)
	require.Equal(t, "", msgs)

	msgs, ok = extractMessagesJSONFromRequestJSON(`{"prompt":"hi"}`)
	require.False(t, ok)
	require.Equal(t, "", msgs)

	msgs, ok = extractMessagesJSONFromRequestJSON(`{"messages":[{"role":"user","content":"hi"}]}`)
	require.True(t, ok)
	require.Contains(t, msgs, "role")
}

func TestBuildLLMObservationInput_And_WrapWithToolsBranches(t *testing.T) {
	toolDefs := `[{"name":"tool-a"}]`
	invalidToolDefs := `{`
	messages := `[{"role":"user","parts":[{"type":"text","content":"hello"}]}]`

	withInput := llmSpanCollected{inputMessages: strPtr(messages), toolDefinitions: strPtr(toolDefs)}
	out := buildLLMObservationInput(withInput)
	require.Contains(t, out, `"tools"`)
	require.Contains(t, out, `"messages"`)

	withLLMReqMessages := llmSpanCollected{llmRequest: strPtr(`{"messages":[{"role":"user","content":"from-request"}]}`)}
	out = buildLLMObservationInput(withLLMReqMessages)
	require.Contains(t, out, "from-request")

	withLLMReqNoMessages := llmSpanCollected{llmRequest: strPtr(`{"prompt":"raw-request"}`)}
	out = buildLLMObservationInput(withLLMReqNoMessages)
	require.Equal(t, `{"prompt":"raw-request"}`, out)

	out = wrapWithToolsIfPresent(messages, strPtr(invalidToolDefs))
	require.Equal(t, messages, out)

	out = buildLLMObservationInput(llmSpanCollected{})
	require.Equal(t, "N/A", out)
}

func TestSanitizeSingleMessageForObservation_AndTruncateBytesHeadTail(t *testing.T) {
	msg := model.Message{
		Content:          strings.Repeat("content", 20),
		ReasoningContent: strings.Repeat("reason", 20),
		ToolID:           strings.Repeat("id", 30),
		ToolName:         strings.Repeat("tool", 20),
		ToolCalls: []model.ToolCall{{
			ID: strings.Repeat("call", 20),
			Function: model.FunctionDefinitionParam{
				Name:        strings.Repeat("name", 20),
				Description: strings.Repeat("desc", 20),
				Arguments:   []byte(strings.Repeat("arg", 40)),
			},
		}},
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: strPtr(strings.Repeat("text", 30))},
			{Type: model.ContentTypeFile, File: &model.File{
				Name:     strings.Repeat("file", 20),
				FileID:   strings.Repeat("id", 20),
				MimeType: strings.Repeat("mime", 20),
				Data:     []byte(strings.Repeat("f", 200)),
			}},
			{Type: model.ContentTypeImage, Image: &model.Image{
				URL:    strings.Repeat("url", 20),
				Detail: strings.Repeat("detail", 20),
				Format: strings.Repeat("format", 20),
				Data:   []byte(strings.Repeat("i", 200)),
			}},
			{Type: model.ContentTypeAudio, Audio: &model.Audio{
				Format: strings.Repeat("audio", 20),
				Data:   []byte(strings.Repeat("a", 200)),
			}},
		},
	}

	sanitizeSingleMessageForObservation(&msg, truncateMessagesPlan{textLimit: 16, binaryLimit: 16})

	require.LessOrEqual(t, len([]byte(msg.Content)), 16)
	require.LessOrEqual(t, len([]byte(msg.ReasoningContent)), 16)
	require.LessOrEqual(t, len([]byte(msg.ToolCalls[0].Function.Name)), 16)
	require.LessOrEqual(t, len(msg.ToolCalls[0].Function.Arguments), 16)
	require.LessOrEqual(t, len(msg.ContentParts[1].File.Data), 16)
	require.LessOrEqual(t, len(msg.ContentParts[2].Image.Data), 16)
	require.LessOrEqual(t, len(msg.ContentParts[3].Audio.Data), 16)

	tooSmall := truncateBytesHeadTail([]byte(strings.Repeat("b", 100)), 5)
	require.Equal(t, "...[t", string(tooSmall))
}

func TestExporterLifecycle_ErrorBranches(t *testing.T) {
	exp := &exporter{client: &mockStartStopErrorClient{}}
	err := exp.Start(context.Background())
	require.Error(t, err)
	require.True(t, exp.started)

	err = exp.Shutdown(context.Background())
	require.Error(t, err)
	require.False(t, exp.started)
}

func strPtr(s string) *string { return &s }

type mockStartStopErrorClient struct{}

func (m *mockStartStopErrorClient) Start(context.Context) error { return fmt.Errorf("start failed") }
func (m *mockStartStopErrorClient) Stop(context.Context) error  { return fmt.Errorf("stop failed") }
func (m *mockStartStopErrorClient) UploadTraces(context.Context, []*tracepb.ResourceSpans) error {
	return nil
}
