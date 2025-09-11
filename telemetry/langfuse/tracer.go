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

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace/noop"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// Start starts telemetry with Langfuse integration using the function option pattern.
func Start(ctx context.Context, opts ...Option) (clean func(context.Context) error, err error) {
	// Start with default config from environment
	config := newConfigFromEnv()

	// Apply user-provided options
	for _, opt := range opts {
		opt(config)
	}

	if config.secretKey == "" || config.publicKey == "" || config.host == "" {
		return nil, fmt.Errorf("langfuse: secret key, public key and host must be provided")
	}

	otelOpts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(config.host),
		otlptracehttp.WithInsecure(),
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Basic %s", encodeAuth(config.publicKey, config.secretKey)),
		}),
		otlptracehttp.WithURLPath("/api/public/otel/v1/traces"),
	}

	return start(ctx, otelOpts...)
}

func start(ctx context.Context, opts ...otlptracehttp.Option) (clean func(context.Context) error, err error) {
	p := atrace.TracerProvider
	_, ok := p.(noop.TracerProvider)
	var provider *sdktrace.TracerProvider
	if !ok {
		provider, ok = p.(*sdktrace.TracerProvider)
		if !ok {
			return nil, fmt.Errorf("otel.GetTracerProvider() returned a non-SDK trace p")
		}

	}

	exp, err := newExporter(ctx, opts...)
	if err != nil {
		return nil, err
	}
	processor := newSpanProcessor(exp)
	if provider == nil {
		res, err := resource.New(ctx,
			resource.WithAttributes(
				semconv.ServiceNamespace(itelemetry.ResourceServiceNamespace),
				semconv.ServiceName(itelemetry.ResourceServiceName),
				semconv.ServiceVersion(itelemetry.ResourceServiceVersion),
			),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create resource: %w", err)
		}
		provider = sdktrace.NewTracerProvider(
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
			sdktrace.WithResource(res),
			sdktrace.WithSpanProcessor(processor),
		)
		atrace.TracerProvider = provider
	} else {
		provider.RegisterSpanProcessor(processor)
	}

	atrace.Tracer = provider.Tracer(itelemetry.InstrumentName)
	return provider.Shutdown, nil
}

// encodeAuth encodes the public and secret keys for basic authentication.
func encodeAuth(pk, sk string) string {
	auth := pk + ":" + sk
	return base64.StdEncoding.EncodeToString([]byte(auth))
}
