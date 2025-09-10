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
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

// MockTracer is a mock implementation of trace.Tracer for testing
type MockTracer struct {
	embedded.Tracer
	mock.Mock
}

func (m *MockTracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	args := m.Called(ctx, spanName, opts)
	return args.Get(0).(context.Context), args.Get(1).(trace.Span)
}

func TestStart(t *testing.T) {
	tests := []struct {
		name        string
		config      *config
		envVars     map[string]string
		shouldError bool
	}{
		{
			name: "missing secretKey",
			config: &config{
				publicKey: "test-public",
				host:      "https://test.langfuse.com",
			},
			shouldError: true,
		},
		{
			name:        "with nil config (uses env)",
			config:      nil,
			shouldError: false,
			envVars: map[string]string{
				"LANGFUSE_SECRET_KEY": "env-secret",
				"LANGFUSE_PUBLIC_KEY": "env-public",
				"LANGFUSE_HOST":       "https://env.langfuse.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables if provided
			if tt.envVars != nil {
				for key, value := range tt.envVars {
					os.Setenv(key, value)
				}
				defer func() {
					for key := range tt.envVars {
						os.Unsetenv(key)
					}
				}()
			} else {
				// 清除所有相关环境变量，确保为空
				os.Unsetenv("LANGFUSE_SECRET_KEY")
				os.Unsetenv("LANGFUSE_PUBLIC_KEY")
				os.Unsetenv("LANGFUSE_HOST")
			}

			ctx := context.Background()
			var opts []Option
			if tt.config != nil {
				if tt.config.secretKey != "" {
					opts = append(opts, WithSecretKey(tt.config.secretKey))
				}
				if tt.config.publicKey != "" {
					opts = append(opts, WithPublicKey(tt.config.publicKey))
				}
				if tt.config.host != "" {
					opts = append(opts, WithHost(tt.config.host))
				}
			}
			clean, err := Start(ctx, opts...)

			if tt.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if clean != nil {
				clean()
			}
		})
	}
}

func TestEncodeAuth(t *testing.T) {
	tests := []struct {
		name      string
		publicKey string
		secretKey string
		expected  string
	}{
		{
			name:      "basic encoding",
			publicKey: "public",
			secretKey: "secret",
			expected:  "cHVibGljOnNlY3JldA==", // base64 encoding of "public:secret"
		},
		{
			name:      "empty keys",
			publicKey: "",
			secretKey: "",
			expected:  "Og==", // base64 encoding of ":"
		},
		{
			name:      "special characters",
			publicKey: "pub@key",
			secretKey: "sec#ret!",
			expected:  "cHViQGtleTpzZWMjcmV0IQ==", // base64 encoding of "pub@key:sec#ret!"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := encodeAuth(tt.publicKey, tt.secretKey)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTracer_Start(t *testing.T) {
	mockUnderlyingTracer := &MockTracer{}
	mockSpan := &MockSpan{}

	ctx := context.Background()
	spanName := "test-span"

	mockUnderlyingTracer.On("Start", ctx, spanName, mock.Anything).Return(ctx, mockSpan)

	tracer := &tracer{
		underlying: mockUnderlyingTracer,
	}

	resultCtx, resultSpan := tracer.Start(ctx, spanName)

	assert.Equal(t, ctx, resultCtx)
	assert.IsType(t, &span{}, resultSpan)

	customSpan := resultSpan.(*span)
	assert.Equal(t, mockSpan, customSpan.underlying)
	assert.Equal(t, spanName, customSpan.spanName)
	assert.NotNil(t, customSpan.attrs)

	mockUnderlyingTracer.AssertCalled(t, "Start", ctx, spanName, mock.Anything)
}

func TestTracer_StartWithOptions(t *testing.T) {
	mockUnderlyingTracer := &MockTracer{}
	mockSpan := &MockSpan{}

	ctx := context.Background()
	spanName := "test-span"
	opts := []trace.SpanStartOption{
		trace.WithTimestamp(time.Now()),
	}

	mockUnderlyingTracer.On("Start", ctx, spanName, opts).Return(ctx, mockSpan)

	tracer := &tracer{
		underlying: mockUnderlyingTracer,
	}

	resultCtx, resultSpan := tracer.Start(ctx, spanName, opts...)

	assert.Equal(t, ctx, resultCtx)
	assert.IsType(t, &span{}, resultSpan)

	mockUnderlyingTracer.AssertCalled(t, "Start", ctx, spanName, opts)
}
