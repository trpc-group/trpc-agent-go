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
	"encoding/base64"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

var _ trace.Tracer = (*tracer)(nil)

// Start starts telemetry with Langfuse integration using the function option pattern.
func Start(ctx context.Context, opts ...Option) (clean func() error, err error) {
	// Start with default config from environment
	config := newConfigFromEnv()

	// Apply user-provided options
	for _, opt := range opts {
		opt(config)
	}

	if config.secretKey == "" || config.publicKey == "" || config.host == "" {
		return nil, fmt.Errorf("langfuse: secret key, public key and host must be provided")
	}

	langfuseOpts := []atrace.Option{
		atrace.WithEndpointURL(config.host + "/api/public/otel/v1/traces"),
		atrace.WithProtocol("http"),
		atrace.WithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Basic %s", encodeAuth(config.publicKey, config.secretKey)),
		}),
	}

	return start(ctx, langfuseOpts...)
}

func start(ctx context.Context, opts ...atrace.Option) (clean func() error, err error) {
	// Start the standard tracer first
	clean, err = atrace.Start(ctx, opts...)
	if err != nil {
		return nil, err
	}

	// Get the current tracer provider
	currentProvider := otel.GetTracerProvider()

	// Wrap it with our custom provider
	customProvider := newTracerProvider(currentProvider)

	// Set the custom provider as the global provider
	otel.SetTracerProvider(customProvider)

	// Update the global tracer to use the custom provider
	atrace.Tracer = customProvider.Tracer("trpc-agent-go")

	return clean, nil
}

// encodeAuth encodes the public and secret keys for basic authentication.
func encodeAuth(pk, sk string) string {
	auth := pk + ":" + sk
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

// tracer wraps an existing Tracer to return custom spans.
type tracer struct {
	embedded.Tracer
	underlying trace.Tracer
}

// Start creates a custom span that wraps the underlying span.
func (t *tracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	ctx, underlyingSpan := t.underlying.Start(ctx, spanName, opts...)
	s := &span{
		underlying: underlyingSpan,
		spanName:   spanName, // Store span name for transformation logic
		attrs:      make(map[attribute.Key]attribute.Value),
	}
	return ctx, s
}
