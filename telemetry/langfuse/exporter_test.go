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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
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
						Key: itelemetry.KeyGenAIOperationName,
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
						Key: itelemetry.KeyGenAIOperationName,
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
						Key: itelemetry.KeyGenAIOperationName,
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
	span := &tracepb.Span{
		Name: "agent-span",
		Attributes: []*commonpb.KeyValue{
			{
				Key: itelemetry.KeyGenAIInputMessages,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: `[{"role":"user","content":"hi"}]`},
				},
			},
			{
				Key: itelemetry.KeyGenAIOutputMessages,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: `[{"role":"assistant","content":"hello"}]`},
				},
			},
			{
				Key: itelemetry.KeyGenAIUsageInputTokens,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_IntValue{IntValue: 123},
				},
			},
			{
				Key: itelemetry.KeyGenAIUsageOutputTokens,
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
	assert.Equal(t, `[{"role":"user","content":"hi"}]`, attrMap[observationInput])
	assert.Equal(t, `[{"role":"assistant","content":"hello"}]`, attrMap[observationOutput])
	assert.Equal(t, "keep-this", attrMap["other.attribute"])

	// Token usage attributes should be filtered out for InvokeAgent observations.
	for _, attr := range span.Attributes {
		assert.NotEqual(t, itelemetry.KeyGenAIUsageInputTokens, attr.Key)
		assert.NotEqual(t, itelemetry.KeyGenAIUsageOutputTokens, attr.Key)
	}
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
						Key: itelemetry.KeyLLMRequest,
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: `{"prompt": "Hello", "generation_config": {"temperature": 0.7}}`},
						},
					},
					{
						Key: itelemetry.KeyLLMResponse,
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
						Key:   itelemetry.KeyLLMRequest,
						Value: nil,
					},
					{
						Key: itelemetry.KeyLLMResponse,
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
						Key: itelemetry.KeyLLMRequest,
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: "request"},
						},
					},
					{
						Key:   itelemetry.KeyLLMResponse,
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
				assert.NotEqual(t, itelemetry.KeyLLMRequest, attr.Key, "LLM request attribute should be removed")
				assert.NotEqual(t, itelemetry.KeyLLMResponse, attr.Key, "LLM response attribute should be removed")
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
				Key: itelemetry.KeyLLMRequest,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: `{"generation_config": {"temperature": 0.7}}`},
				},
			},
			{
				Key: itelemetry.KeyGenAIInputMessages,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: `[{"role":"user","content":"hi"}]`},
				},
			},
			{
				Key: itelemetry.KeyGenAIRequestToolDefinitions,
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
		assert.NotEqual(t, itelemetry.KeyGenAIInputMessages, attr.Key, "input messages should be folded into observation.input")
		assert.NotEqual(t, itelemetry.KeyGenAIRequestToolDefinitions, attr.Key, "tool definitions should be folded into observation.input")
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
					Key: itelemetry.KeyLLMRequest,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: `{"prompt":"hello"}`},
					},
				},
				{
					Key: itelemetry.KeyLLMResponse,
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: `{"text":"world"}`},
					},
				},
			}
			// Add token usage attributes (only non-zero values, matching how buildResponseAttributes works)
			if tt.inputTokens != 0 {
				attrs = append(attrs, &commonpb.KeyValue{
					Key:   itelemetry.KeyGenAIUsageInputTokens,
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: tt.inputTokens}},
				})
			}
			if tt.outputTokens != 0 {
				attrs = append(attrs, &commonpb.KeyValue{
					Key:   itelemetry.KeyGenAIUsageOutputTokens,
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: tt.outputTokens}},
				})
			}
			if tt.cachedTokens != 0 {
				attrs = append(attrs, &commonpb.KeyValue{
					Key:   itelemetry.KeyGenAIUsageInputTokensCached,
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: tt.cachedTokens}},
				})
			}
			if tt.cacheReadTokens != 0 {
				attrs = append(attrs, &commonpb.KeyValue{
					Key:   itelemetry.KeyGenAIUsageInputTokensCacheRead,
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: tt.cacheReadTokens}},
				})
			}
			if tt.cacheCreationTokens != 0 {
				attrs = append(attrs, &commonpb.KeyValue{
					Key:   itelemetry.KeyGenAIUsageInputTokensCacheCreation,
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: tt.cacheCreationTokens}},
				})
			}

			span := &tracepb.Span{Name: "llm-call", Attributes: attrs}
			transformCallLLM(span)

			// Verify original gen_ai.usage.* attributes are removed
			for _, attr := range span.Attributes {
				assert.NotEqual(t, itelemetry.KeyGenAIUsageInputTokens, attr.Key)
				assert.NotEqual(t, itelemetry.KeyGenAIUsageOutputTokens, attr.Key)
				assert.NotEqual(t, itelemetry.KeyGenAIUsageInputTokensCached, attr.Key)
				assert.NotEqual(t, itelemetry.KeyGenAIUsageInputTokensCacheRead, attr.Key)
				assert.NotEqual(t, itelemetry.KeyGenAIUsageInputTokensCacheCreation, attr.Key)
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
				Key: itelemetry.KeyGenAIInputMessages,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: `[{"role":"user","content":"hi"}]`},
				},
			},
			{
				Key: itelemetry.KeyGenAIOutputMessages,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: `[{"role":"assistant","content":"hello"}]`},
				},
			},
			{
				Key:   itelemetry.KeyGenAIUsageInputTokens,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 100}},
			},
			{
				Key:   itelemetry.KeyGenAIUsageOutputTokens,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 50}},
			},
			{
				Key:   itelemetry.KeyGenAIUsageInputTokensCached,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 30}},
			},
			{
				Key:   itelemetry.KeyGenAIUsageInputTokensCacheRead,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 20}},
			},
			{
				Key:   itelemetry.KeyGenAIUsageInputTokensCacheCreation,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 10}},
			},
		},
	}

	transformInvokeAgent(span)

	// All token usage attributes (including cache ones) should be filtered out
	for _, attr := range span.Attributes {
		assert.NotEqual(t, itelemetry.KeyGenAIUsageInputTokens, attr.Key)
		assert.NotEqual(t, itelemetry.KeyGenAIUsageOutputTokens, attr.Key)
		assert.NotEqual(t, itelemetry.KeyGenAIUsageInputTokensCached, attr.Key)
		assert.NotEqual(t, itelemetry.KeyGenAIUsageInputTokensCacheRead, attr.Key)
		assert.NotEqual(t, itelemetry.KeyGenAIUsageInputTokensCacheCreation, attr.Key)
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
						Key: itelemetry.KeyGenAIToolCallArguments,
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: `{"arg1": "value1"}`},
						},
					},
					{
						Key: itelemetry.KeyGenAIToolCallResult,
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
						Key:   itelemetry.KeyGenAIToolCallArguments,
						Value: nil,
					},
					{
						Key: itelemetry.KeyGenAIToolCallResult,
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
				assert.NotEqual(t, itelemetry.KeyGenAIToolCallArguments, attr.Key, "tool args attribute should be removed")
				assert.NotEqual(t, itelemetry.KeyGenAIToolCallResult, attr.Key, "tool response attribute should be removed")
			}
		})
	}
}

func TestTransformWorkflow(t *testing.T) {
	span := &tracepb.Span{
		Name: "workflow-span",
		Attributes: []*commonpb.KeyValue{
			{
				Key: itelemetry.KeyRunnerSessionID,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: "sess-1"},
				},
			},
			{
				Key: itelemetry.KeyRunnerUserID,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: "user-1"},
				},
			},
			{
				Key: itelemetry.KeyGenAIWorkflowRequest,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: `{"req":true}`},
				},
			},
			{
				Key: itelemetry.KeyGenAIWorkflowResponse,
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
		assert.NotEqual(t, itelemetry.KeyGenAIWorkflowRequest, attr.Key)
		assert.NotEqual(t, itelemetry.KeyGenAIWorkflowResponse, attr.Key)
		assert.NotEqual(t, itelemetry.KeyRunnerSessionID, attr.Key)
		assert.NotEqual(t, itelemetry.KeyRunnerUserID, attr.Key)
	}
}

func TestTransformWorkflow_NilRequestResponse(t *testing.T) {
	span := &tracepb.Span{
		Name: "workflow-span-nil",
		Attributes: []*commonpb.KeyValue{
			{
				Key: itelemetry.KeyRunnerSessionID,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: "sess-2"},
				},
			},
			{
				Key: itelemetry.KeyRunnerUserID,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: "user-2"},
				},
			},
			{
				Key:   itelemetry.KeyGenAIWorkflowRequest,
				Value: nil,
			},
			{
				Key:   itelemetry.KeyGenAIWorkflowResponse,
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
		assert.NotEqual(t, itelemetry.KeyGenAIWorkflowRequest, attr.Key)
		assert.NotEqual(t, itelemetry.KeyGenAIWorkflowResponse, attr.Key)
		assert.NotEqual(t, itelemetry.KeyRunnerSessionID, attr.Key)
		assert.NotEqual(t, itelemetry.KeyRunnerUserID, attr.Key)
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
									Key: itelemetry.KeyGenAIOperationName,
									Value: &commonpb.AnyValue{
										Value: &commonpb.AnyValue_StringValue{StringValue: itelemetry.OperationChat},
									},
								},
								{
									Key: itelemetry.KeyLLMRequest,
									Value: &commonpb.AnyValue{
										Value: &commonpb.AnyValue_StringValue{StringValue: `{"prompt": "test"}`},
									},
								},
								{
									Key: itelemetry.KeyLLMResponse,
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
		case itelemetry.KeyLLMRequest:
			hasLLMRequest = true
		case itelemetry.KeyLLMResponse:
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
									Key: itelemetry.KeyGenAIOperationName,
									Value: &commonpb.AnyValue{
										Value: &commonpb.AnyValue_StringValue{StringValue: itelemetry.OperationChat},
									},
								},
								{
									Key: itelemetry.KeyLLMRequest,
									Value: &commonpb.AnyValue{
										Value: &commonpb.AnyValue_StringValue{StringValue: `{"prompt": "test"}`},
									},
								},
								{
									Key: itelemetry.KeyLLMResponse,
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
