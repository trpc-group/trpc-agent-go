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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace/noop"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

func TestResolveBaggageFilter_ExtraKeys(t *testing.T) {
	filter := resolveBaggageFilter(&config{extraBaggageKeys: []string{"guild.agent_name"}})

	m, err := baggage.NewMemberRaw("guild.agent_name", "persona-a")
	require.NoError(t, err)
	assert.True(t, filter(m))

	ignored, err := baggage.NewMemberRaw("baggage.test", "x")
	require.NoError(t, err)
	assert.False(t, filter(ignored))

	traceNameMember, err := baggage.NewMemberRaw(traceName, "persona-a")
	require.NoError(t, err)
	assert.True(t, filter(traceNameMember))
}

func TestResolveBaggageFilter_CustomFilterOverridesExtras(t *testing.T) {
	filter := resolveBaggageFilter(&config{
		extraBaggageKeys: []string{"guild.agent_name"},
		baggageFilter: func(m baggage.Member) bool {
			return m.Key() == "custom.only"
		},
	})
	custom, err := baggage.NewMemberRaw("custom.only", "1")
	require.NoError(t, err)
	assert.True(t, filter(custom))

	extra, err := baggage.NewMemberRaw("guild.agent_name", "persona-a")
	require.NoError(t, err)
	assert.False(t, filter(extra))
}

func TestAttributeRewritingExporter_RewritesBeforeNext(t *testing.T) {
	rec := &recordingExporter{}
	exp := &attributeRewritingExporter{
		next: rec,
		rewrite: func(attrs []attribute.KeyValue) []attribute.KeyValue {
			out := make([]attribute.KeyValue, 0, len(attrs))
			for _, a := range attrs {
				if a.Key == semconvtrace.KeyGenAISystem {
					out = append(out, attribute.String(semconvtrace.KeyGenAISystem, "custom.system"))
					continue
				}
				out = append(out, a)
			}
			return out
		},
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	_, span := tp.Tracer("test").Start(context.Background(), "span")
	span.SetAttributes(attribute.String(semconvtrace.KeyGenAISystem, semconvtrace.SystemTRPCGoAgent))
	span.End()

	spans := rec.snapshot()
	require.Len(t, spans, 1)
	assert.Contains(t, spans[0].Attributes(), attribute.String(semconvtrace.KeyGenAISystem, "custom.system"))
	assert.NotContains(t, spans[0].Attributes(), attribute.String(semconvtrace.KeyGenAISystem, semconvtrace.SystemTRPCGoAgent))
}

func TestNewSpanProcessor_ExtraBaggageKeysCopied(t *testing.T) {
	ctx := context.Background()
	m, err := baggage.NewMemberRaw("guild.agent_name", "persona-a")
	require.NoError(t, err)
	b, err := baggage.New(m)
	require.NoError(t, err)
	ctx = baggage.ContextWithBaggage(ctx, b)

	exp := &recordingExporter{}
	filter := resolveBaggageFilter(&config{extraBaggageKeys: []string{"guild.agent_name"}})
	sp := newSpanProcessor(exp, filter)

	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sp))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	_, span := tp.Tracer("test").Start(ctx, "span")
	span.End()
	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := exp.snapshot()
	require.Len(t, spans, 1)
	assert.Contains(t, spans[0].Attributes(), attribute.String("guild.agent_name", "persona-a"))
}

func TestStart_AppliesIdentityOptions(t *testing.T) {
	ctx := context.Background()
	oldProvider := atrace.TracerProvider
	oldTracer := atrace.Tracer
	defer func() {
		atrace.TracerProvider = oldProvider
		atrace.Tracer = oldTracer
		itelemetry.SetGenAISystem("")
	}()
	atrace.TracerProvider = noop.NewTracerProvider()

	cfg := &config{
		serviceName:      "brand-service",
		serviceNamespace: "brand-ns",
		serviceVersion:   "9.9.9",
		instrumentName:   "brand.scope",
		genAISystem:      "brand.system",
	}
	clean, err := start(ctx, cfg,
		otlptracehttp.WithEndpoint("localhost:4318"),
		otlptracehttp.WithInsecure(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clean(ctx) })

	assert.Equal(t, "brand.system", itelemetry.GenAISystem())

	sdkTP, ok := atrace.TracerProvider.(*sdktrace.TracerProvider)
	require.True(t, ok)

	_, span := sdkTP.Tracer("probe").Start(ctx, "probe")
	readWrite, ok := span.(sdktrace.ReadWriteSpan)
	require.True(t, ok)
	attrs := readWrite.Resource().Attributes()
	assert.Contains(t, attrs, semconv.ServiceName("brand-service"))
	assert.Contains(t, attrs, semconv.ServiceNamespace("brand-ns"))
	assert.Contains(t, attrs, semconv.ServiceVersion("9.9.9"))
	span.End()
}

func TestOptionHelpers_ApplyToConfig(t *testing.T) {
	cfg := &config{}
	WithServiceName("svc")(cfg)
	WithServiceNamespace("ns")(cfg)
	WithServiceVersion("1.2.3")(cfg)
	WithInstrumentName("scope")(cfg)
	WithGenAISystem("sys")(cfg)
	WithExtraBaggageAttributeKeys("k1", "k2")(cfg)
	WithAttributeRewriter(func(attrs []attribute.KeyValue) []attribute.KeyValue { return attrs })(cfg)

	assert.Equal(t, "svc", cfg.serviceName)
	assert.Equal(t, "ns", cfg.serviceNamespace)
	assert.Equal(t, "1.2.3", cfg.serviceVersion)
	assert.Equal(t, "scope", cfg.instrumentName)
	assert.Equal(t, "sys", cfg.genAISystem)
	assert.Equal(t, []string{"k1", "k2"}, cfg.extraBaggageKeys)
	assert.NotNil(t, cfg.attributeRewriter)
}
