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
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
)

// MockSpan is a mock implementation of trace.Span for testing
type MockSpan struct {
	embedded.Span
	mock.Mock
	attributes []attribute.KeyValue
	mutex      sync.RWMutex
}

func (m *MockSpan) End(options ...trace.SpanEndOption) {
	m.Called(options)
}

func (m *MockSpan) AddEvent(name string, options ...trace.EventOption) {
	m.Called(name, options)
}

func (m *MockSpan) IsRecording() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockSpan) RecordError(err error, options ...trace.EventOption) {
	m.Called(err, options)
}

func (m *MockSpan) SpanContext() trace.SpanContext {
	args := m.Called()
	return args.Get(0).(trace.SpanContext)
}

func (m *MockSpan) SetStatus(code codes.Code, description string) {
	m.Called(code, description)
}

func (m *MockSpan) SetName(name string) {
	m.Called(name)
}

func (m *MockSpan) SetAttributes(kv ...attribute.KeyValue) {
	m.mutex.Lock()
	m.attributes = append(m.attributes, kv...)
	m.mutex.Unlock()
	m.Called(kv)
}

func (m *MockSpan) AddLink(link trace.Link) {
	m.Called(link)
}

func (m *MockSpan) TracerProvider() trace.TracerProvider {
	args := m.Called()
	return args.Get(0).(trace.TracerProvider)
}

func (m *MockSpan) GetAttributes() []attribute.KeyValue {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return m.attributes
}

func TestSpan_SetAttributes(t *testing.T) {
	mockSpan := &MockSpan{}
	mockSpan.On("SetAttributes", mock.Anything).Return()

	s := &span{
		underlying: mockSpan,
		attrs:      make(map[attribute.Key]attribute.Value),
		mutex:      sync.RWMutex{},
	}

	attrs := []attribute.KeyValue{
		attribute.String("test.key1", "value1"),
		attribute.String("test.key2", "value2"),
	}

	s.SetAttributes(attrs...)

	// Verify attributes are stored in internal map
	assert.Equal(t, "value1", s.attrs[attribute.Key("test.key1")].AsString())
	assert.Equal(t, "value2", s.attrs[attribute.Key("test.key2")].AsString())

	// Verify underlying span was called
	mockSpan.AssertCalled(t, "SetAttributes", attrs)
}

func TestSpan_End(t *testing.T) {
	mockSpan := &MockSpan{}
	mockSpan.On("SetAttributes", mock.Anything).Return()
	mockSpan.On("End", mock.Anything).Return()

	s := &span{
		underlying: mockSpan,
		attrs:      make(map[attribute.Key]attribute.Value),
		mutex:      sync.RWMutex{},
	}

	// Set some attributes first
	s.attrs[attribute.Key(itelemetry.KeyGenAIOperationName)] = attribute.StringValue("call_llm")
	s.attrs[attribute.Key(itelemetry.KeyLLMRequest)] = attribute.StringValue(`{"model": "test"}`)

	s.End()

	mockSpan.AssertCalled(t, "End", mock.Anything)
}

func TestSpan_TransformCallLLM(t *testing.T) {
	mockSpan := &MockSpan{}
	mockSpan.On("SetAttributes", mock.Anything).Return()

	s := &span{
		underlying: mockSpan,
		attrs:      make(map[attribute.Key]attribute.Value),
		mutex:      sync.RWMutex{},
	}

	tests := []struct {
		name  string
		attrs map[attribute.Key]attribute.Value
	}{
		{
			name: "with request and response",
			attrs: map[attribute.Key]attribute.Value{
				attribute.Key(itelemetry.KeyLLMRequest):  attribute.StringValue(`{"model": "test", "generation_config": {"temperature": 0.5}}`),
				attribute.Key(itelemetry.KeyLLMResponse): attribute.StringValue(`{"choices": [{"text": "response"}]}`),
			},
		},
		{
			name:  "without request and response",
			attrs: map[attribute.Key]attribute.Value{},
		},
		{
			name: "with invalid JSON request",
			attrs: map[attribute.Key]attribute.Value{
				attribute.Key(itelemetry.KeyLLMRequest): attribute.StringValue(`invalid json`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSpan.ExpectedCalls = nil // Reset mock calls
			mockSpan.On("SetAttributes", mock.Anything).Return()

			s.transformCallLLM(tt.attrs)

			mockSpan.AssertCalled(t, "SetAttributes", mock.MatchedBy(func(kv []attribute.KeyValue) bool {
				// Check that observationType is set to "generation"
				for _, attr := range kv {
					if attr.Key == observationType && attr.Value.AsString() == "generation" {
						return true
					}
				}
				return false
			}))
		})
	}
}

func TestSpan_TransformExecuteTool(t *testing.T) {
	mockSpan := &MockSpan{}
	mockSpan.On("SetAttributes", mock.Anything).Return()

	s := &span{
		underlying: mockSpan,
		attrs:      make(map[attribute.Key]attribute.Value),
		mutex:      sync.RWMutex{},
	}

	tests := []struct {
		name  string
		attrs map[attribute.Key]attribute.Value
	}{
		{
			name: "with tool call args and response",
			attrs: map[attribute.Key]attribute.Value{
				attribute.Key(itelemetry.KeyToolCallArgs): attribute.StringValue(`{"arg1": "value1"}`),
				attribute.Key(itelemetry.KeyToolResponse): attribute.StringValue(`{"result": "success"}`),
			},
		},
		{
			name:  "without tool call args and response",
			attrs: map[attribute.Key]attribute.Value{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSpan.ExpectedCalls = nil // Reset mock calls
			mockSpan.On("SetAttributes", mock.Anything).Return()

			s.transformExecuteTool(tt.attrs)

			mockSpan.AssertCalled(t, "SetAttributes", mock.MatchedBy(func(kv []attribute.KeyValue) bool {
				// Check that observationType is set to "tool"
				for _, attr := range kv {
					if attr.Key == observationType && attr.Value.AsString() == "tool" {
						return true
					}
				}
				return false
			}))
		})
	}
}

func TestSpan_TransformRunRunner(t *testing.T) {
	mockSpan := &MockSpan{}
	mockSpan.On("SetAttributes", mock.Anything).Return()

	s := &span{
		underlying: mockSpan,
		attrs:      make(map[attribute.Key]attribute.Value),
		mutex:      sync.RWMutex{},
	}

	tests := []struct {
		name  string
		attrs map[attribute.Key]attribute.Value
	}{
		{
			name: "with all runner attributes",
			attrs: map[attribute.Key]attribute.Value{
				attribute.Key(itelemetry.KeyRunnerName):      attribute.StringValue("test-runner"),
				attribute.Key(itelemetry.KeyRunnerUserID):    attribute.StringValue("user123"),
				attribute.Key(itelemetry.KeyRunnerSessionID): attribute.StringValue("session456"),
				attribute.Key(itelemetry.KeyRunnerInput):     attribute.StringValue("test input"),
				attribute.Key(itelemetry.KeyRunnerOutput):    attribute.StringValue("test output"),
			},
		},
		{
			name:  "without runner attributes",
			attrs: map[attribute.Key]attribute.Value{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSpan.ExpectedCalls = nil // Reset mock calls
			mockSpan.On("SetAttributes", mock.Anything).Return()

			s.transformRunRunner(tt.attrs)

			// Each transform operation calls SetAttributes multiple times
			// Don't check exact number as it may vary
			mockSpan.AssertCalled(t, "SetAttributes", mock.Anything)
		})
	}
}

func TestSpan_TransformAttributes(t *testing.T) {
	mockSpan := &MockSpan{}
	mockSpan.On("SetAttributes", mock.Anything).Return()

	s := &span{
		underlying: mockSpan,
		attrs:      make(map[attribute.Key]attribute.Value),
		mutex:      sync.RWMutex{},
	}

	tests := []struct {
		name           string
		attrs          map[attribute.Key]attribute.Value
		expectedCalled bool
	}{
		{
			name: "call_llm operation",
			attrs: map[attribute.Key]attribute.Value{
				attribute.Key(itelemetry.KeyGenAIOperationName): attribute.StringValue("call_llm"),
			},
			expectedCalled: true,
		},
		{
			name: "execute_tool operation",
			attrs: map[attribute.Key]attribute.Value{
				attribute.Key(itelemetry.KeyGenAIOperationName): attribute.StringValue("execute_tool"),
			},
			expectedCalled: true,
		},
		{
			name: "run_runner operation",
			attrs: map[attribute.Key]attribute.Value{
				attribute.Key(itelemetry.KeyGenAIOperationName): attribute.StringValue("run_runner"),
			},
			expectedCalled: true,
		},
		{
			name: "unknown operation",
			attrs: map[attribute.Key]attribute.Value{
				attribute.Key(itelemetry.KeyGenAIOperationName): attribute.StringValue("unknown"),
			},
			expectedCalled: false,
		},
		{
			name:           "no operation attribute",
			attrs:          map[attribute.Key]attribute.Value{},
			expectedCalled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSpan.ExpectedCalls = nil // Reset mock calls
			mockSpan.On("SetAttributes", mock.Anything).Return()

			s.transformAttributes(tt.attrs)

			if tt.expectedCalled {
				mockSpan.AssertCalled(t, "SetAttributes", mock.Anything)
			}
		})
	}
}

func TestSpan_DelegatedMethods(t *testing.T) {
	mockSpan := &MockSpan{}
	mockSpan.On("SpanContext").Return(trace.SpanContext{})
	mockSpan.On("SetStatus", codes.Ok, "test").Return()
	mockSpan.On("SetName", "test-name").Return()
	mockSpan.On("AddEvent", "test-event", mock.Anything).Return()
	mockSpan.On("IsRecording").Return(true)
	mockSpan.On("RecordError", mock.AnythingOfType("*errors.errorString"), mock.Anything).Return()
	mockSpan.On("TracerProvider").Return(trace.NewNoopTracerProvider())
	mockSpan.On("AddLink", mock.Anything).Return()

	s := &span{
		underlying: mockSpan,
		attrs:      make(map[attribute.Key]attribute.Value),
		mutex:      sync.RWMutex{},
	}

	// Test SpanContext
	s.SpanContext()
	mockSpan.AssertCalled(t, "SpanContext")

	// Test SetStatus
	s.SetStatus(codes.Ok, "test")
	mockSpan.AssertCalled(t, "SetStatus", codes.Ok, "test")

	// Test SetName
	s.SetName("test-name")
	mockSpan.AssertCalled(t, "SetName", "test-name")

	// Test AddEvent
	s.AddEvent("test-event")
	mockSpan.AssertCalled(t, "AddEvent", "test-event", mock.Anything)

	// Test IsRecording
	result := s.IsRecording()
	assert.True(t, result)
	mockSpan.AssertCalled(t, "IsRecording")

	// Test RecordError
	err := assert.AnError
	s.RecordError(err)
	mockSpan.AssertCalled(t, "RecordError", err, mock.Anything)

	// Test TracerProvider
	s.TracerProvider()
	mockSpan.AssertCalled(t, "TracerProvider")

	// Test AddLink
	link := trace.Link{}
	s.AddLink(link)
	mockSpan.AssertCalled(t, "AddLink", link)
}

func TestSpan_ConcurrentAccess(t *testing.T) {
	mockSpan := &MockSpan{}
	mockSpan.On("SetAttributes", mock.Anything).Return()
	mockSpan.On("End", mock.Anything).Return()

	s := &span{
		underlying: mockSpan,
		attrs:      make(map[attribute.Key]attribute.Value),
		mutex:      sync.RWMutex{},
	}

	// Test concurrent access to attributes
	var wg sync.WaitGroup
	numGoroutines := 10

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(i int) {
			defer wg.Done()
			key := attribute.Key("test.key." + string(rune(i)))
			value := "value" + string(rune(i))
			s.SetAttributes(attribute.String(string(key), value))
		}(i)
	}

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			s.End()
		}()
	}

	wg.Wait()

	// Verify no race conditions occurred
	assert.NotNil(t, s.attrs)
}

func TestTransformCallLLM_GenerationConfig(t *testing.T) {
	mockSpan := &MockSpan{}
	capturedAttrs := make([]attribute.KeyValue, 0)
	mockSpan.On("SetAttributes", mock.Anything).Run(func(args mock.Arguments) {
		attrs := args.Get(0).([]attribute.KeyValue)
		capturedAttrs = append(capturedAttrs, attrs...)
	}).Return()

	s := &span{
		underlying: mockSpan,
		attrs:      make(map[attribute.Key]attribute.Value),
		mutex:      sync.RWMutex{},
	}

	// Test with generation_config in request
	requestWithConfig := map[string]interface{}{
		"model": "test-model",
		"generation_config": map[string]interface{}{
			"temperature":      0.7,
			"max_tokens":       100,
			"top_p":            0.9,
			"presence_penalty": 0.1,
		},
	}
	requestJSON, _ := json.Marshal(requestWithConfig)

	attrs := map[attribute.Key]attribute.Value{
		attribute.Key(itelemetry.KeyLLMRequest): attribute.StringValue(string(requestJSON)),
	}

	s.transformCallLLM(attrs)

	// Check that generation config was extracted and set as model parameters
	foundModelParams := false
	for _, attr := range capturedAttrs {
		if attr.Key == observationModelParameters {
			foundModelParams = true
			var config map[string]interface{}
			err := json.Unmarshal([]byte(attr.Value.AsString()), &config)
			assert.NoError(t, err)
			assert.Equal(t, 0.7, config["temperature"])
			assert.Equal(t, float64(100), config["max_tokens"])
		}
	}
	assert.True(t, foundModelParams, "Model parameters should be extracted from generation_config")
}
