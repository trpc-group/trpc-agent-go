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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

// MockTracerProvider is a mock implementation of trace.TracerProvider for testing
type MockTracerProvider struct {
	embedded.TracerProvider
	mock.Mock
}

func (m *MockTracerProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	args := m.Called(name, options)
	return args.Get(0).(trace.Tracer)
}

func TestNewTracerProvider(t *testing.T) {
	mockUnderlyingProvider := &MockTracerProvider{}

	tp := newTracerProvider(mockUnderlyingProvider)

	assert.NotNil(t, tp)
	assert.Equal(t, mockUnderlyingProvider, tp.underlying)
}

func TestTracerProvider_Tracer(t *testing.T) {
	mockUnderlyingProvider := &MockTracerProvider{}
	mockUnderlyingTracer := &MockTracer{}

	tracerName := "test-tracer"
	options := []trace.TracerOption{}

	mockUnderlyingProvider.On("Tracer", tracerName, options).Return(mockUnderlyingTracer)

	tp := &tracerProvider{
		underlying: mockUnderlyingProvider,
	}

	result := tp.Tracer(tracerName, options...)

	assert.IsType(t, &tracer{}, result)
	customTracer := result.(*tracer)
	assert.Equal(t, mockUnderlyingTracer, customTracer.underlying)

	mockUnderlyingProvider.AssertCalled(t, "Tracer", tracerName, options)
}

func TestTracerProvider_TracerWithOptions(t *testing.T) {
	mockUnderlyingProvider := &MockTracerProvider{}
	mockUnderlyingTracer := &MockTracer{}

	tracerName := "test-tracer"

	mockUnderlyingProvider.On("Tracer", tracerName, mock.Anything).Return(mockUnderlyingTracer)

	tp := &tracerProvider{
		underlying: mockUnderlyingProvider,
	}

	result := tp.Tracer(tracerName)

	assert.IsType(t, &tracer{}, result)
	customTracer := result.(*tracer)
	assert.Equal(t, mockUnderlyingTracer, customTracer.underlying)

	mockUnderlyingProvider.AssertCalled(t, "Tracer", tracerName, mock.Anything)
}

func TestTracerProvider_Interface(t *testing.T) {
	mockUnderlyingProvider := &MockTracerProvider{}
	tp := newTracerProvider(mockUnderlyingProvider)

	// Verify that our tracerProvider implements trace.TracerProvider
	var _ trace.TracerProvider = tp
	assert.Implements(t, (*trace.TracerProvider)(nil), tp)
}
