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

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace/noop"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"

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

	// Apply truncation config early so callers can rely on it even if Start returns an error.
	setObservationMaxBytes(config.maxObservationLeafValueBytes)

	if config.secretKey == "" || config.publicKey == "" || config.host == "" {
		return nil, fmt.Errorf("langfuse: secret key, public key and host must be provided. Host should be in 'hostname:port' format (e.g., 'cloud.langfuse.com:443')")
	}

	otelOpts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(config.host),
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Basic %s", encodeAuth(config.publicKey, config.secretKey)),
		}),
		otlptracehttp.WithURLPath("/api/public/otel/v1/traces"),
	}

	// Add insecure option only when explicitly configured
	if config.insecure {
		otelOpts = append(otelOpts, otlptracehttp.WithInsecure())
	}

	return start(ctx, config, otelOpts...)
}

func start(ctx context.Context, cfg *config, opts ...otlptracehttp.Option) (clean func(context.Context) error, err error) {
	if cfg == nil {
		cfg = &config{}
	}

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
	var spanExp sdktrace.SpanExporter = exp
	if cfg.attributeRewriter != nil {
		spanExp = &attributeRewritingExporter{next: exp, rewrite: cfg.attributeRewriter}
	}
	processor := newSpanProcessor(spanExp, resolveBaggageFilter(cfg))
	if provider == nil {
		serviceNamespace := semconvtrace.ResourceServiceNamespace
		if cfg.serviceNamespace != "" {
			serviceNamespace = cfg.serviceNamespace
		}
		serviceName := semconvtrace.ResourceServiceName
		if cfg.serviceName != "" {
			serviceName = cfg.serviceName
		}
		serviceVersion := semconvtrace.ResourceServiceVersion
		if cfg.serviceVersion != "" {
			serviceVersion = cfg.serviceVersion
		}
		res, err := resource.New(ctx,
			resource.WithAttributes(
				semconv.ServiceNamespace(serviceNamespace),
				semconv.ServiceName(serviceName),
				semconv.ServiceVersion(serviceVersion),
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

	instrumentName := itelemetry.InstrumentName
	if cfg.instrumentName != "" {
		instrumentName = cfg.instrumentName
	}
	if cfg.genAISystem != "" {
		itelemetry.SetGenAISystem(cfg.genAISystem)
	}
	atrace.Tracer = provider.Tracer(instrumentName)
	return provider.Shutdown, nil
}

// encodeAuth encodes the public and secret keys for basic authentication.
func encodeAuth(pk, sk string) string {
	auth := pk + ":" + sk
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func resolveBaggageFilter(cfg *config) BaggageAttributeFilter {
	if cfg != nil && cfg.baggageFilter != nil {
		return cfg.baggageFilter
	}
	if cfg == nil || len(cfg.extraBaggageKeys) == 0 {
		return defaultLangfuseTraceAttributeFilter
	}
	extras := make(map[string]struct{}, len(cfg.extraBaggageKeys))
	for _, k := range cfg.extraBaggageKeys {
		if k == "" {
			continue
		}
		extras[k] = struct{}{}
	}
	return func(member baggage.Member) bool {
		if defaultLangfuseTraceAttributeFilter(member) {
			return true
		}
		_, ok := extras[member.Key()]
		return ok
	}
}
