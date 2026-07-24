//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trace

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestWithSpanFilters(t *testing.T) {
	startFilter := func(params sdktrace.SamplingParameters) bool {
		return params.Name != "health"
	}
	exportFilter := func(span sdktrace.ReadOnlySpan) bool {
		return span.Name() != "internal"
	}
	opts := &options{}
	WithSpanStartFilter(startFilter)(opts)
	WithSpanExportFilter(exportFilter)(opts)

	if opts.spanStartFilter == nil || opts.spanExportFilter == nil {
		t.Fatal("expected both span filters to be configured")
	}
}

func TestFilteringSampler(t *testing.T) {
	sampler := filteringSampler{
		filter: func(params sdktrace.SamplingParameters) bool {
			return params.Name != "health"
		},
		next: sdktrace.AlwaysSample(),
	}

	if got := sampler.ShouldSample(sdktrace.SamplingParameters{
		Name: "health",
	}).Decision; got != sdktrace.Drop {
		t.Fatalf("health decision = %v, want Drop", got)
	}
	if got := sampler.ShouldSample(sdktrace.SamplingParameters{
		Name: "chat",
	}).Decision; got != sdktrace.RecordAndSample {
		t.Fatalf("chat decision = %v, want RecordAndSample", got)
	}
}

func TestFilteringSampler_PreservesParentTraceState(t *testing.T) {
	traceState, err := oteltrace.ParseTraceState("vendor=value")
	if err != nil {
		t.Fatalf("ParseTraceState() error = %v", err)
	}
	parent := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{1},
		SpanID:     oteltrace.SpanID{1},
		TraceState: traceState,
	})
	sampler := filteringSampler{
		filter: func(sdktrace.SamplingParameters) bool { return false },
		next:   sdktrace.AlwaysSample(),
	}

	result := sampler.ShouldSample(sdktrace.SamplingParameters{
		ParentContext: oteltrace.ContextWithSpanContext(
			context.Background(),
			parent,
		),
	})
	if got := result.Tracestate.String(); got != "vendor=value" {
		t.Fatalf("Tracestate = %q, want vendor=value", got)
	}
}

func TestFilteringSpanExporter(t *testing.T) {
	next := tracetest.NewInMemoryExporter()
	exporter := newFilteringSpanExporter(
		next,
		func(span sdktrace.ReadOnlySpan) bool {
			return span.Name() != "internal"
		},
	)
	spans := []sdktrace.ReadOnlySpan{
		spanSnapshot("chat"),
		spanSnapshot("internal"),
		spanSnapshot("tool"),
	}

	if err := exporter.ExportSpans(context.Background(), spans); err != nil {
		t.Fatalf("ExportSpans() error = %v", err)
	}
	got := next.GetSpans()
	if len(got) != 2 {
		t.Fatalf("exported span count = %d, want 2", len(got))
	}
	if got[0].Name != "chat" || got[1].Name != "tool" {
		t.Fatalf("exported spans = [%s, %s], want [chat, tool]", got[0].Name, got[1].Name)
	}
}

func TestFilteringSpanExporter_AllFiltered(t *testing.T) {
	next := tracetest.NewInMemoryExporter()
	exporter := newFilteringSpanExporter(
		next,
		func(sdktrace.ReadOnlySpan) bool { return false },
	)

	if err := exporter.ExportSpans(
		context.Background(),
		[]sdktrace.ReadOnlySpan{spanSnapshot("internal")},
	); err != nil {
		t.Fatalf("ExportSpans() error = %v", err)
	}
	if got := next.GetSpans(); len(got) != 0 {
		t.Fatalf("exported span count = %d, want 0", len(got))
	}
}

func TestNewFilteringSpanExporter_NilFilter(t *testing.T) {
	next := tracetest.NewInMemoryExporter()
	if got := newFilteringSpanExporter(next, nil); got != next {
		t.Fatal("nil filter should return the original exporter")
	}
}

func TestSetupTracerProvider_WiresFilters(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	restoreProvider := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(restoreProvider) })
	shutdown := setupTracerProvider(resource.Empty(), exporter, &options{
		spanStartFilter: func(params sdktrace.SamplingParameters) bool {
			return params.Name != "start-drop"
		},
		spanExportFilter: func(span sdktrace.ReadOnlySpan) bool {
			return span.Name() != "export-drop"
		},
	})
	t.Cleanup(func() { _ = shutdown(context.Background()) })
	tracer := otel.Tracer("test")
	for _, name := range []string{"keep", "start-drop", "export-drop"} {
		_, span := tracer.Start(context.Background(), name)
		span.End()
	}
	provider, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	if !ok {
		t.Fatalf("TracerProvider type = %T, want *sdktrace.TracerProvider", otel.GetTracerProvider())
	}
	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush() error = %v", err)
	}

	got := exporter.GetSpans()
	if len(got) != 1 || got[0].Name != "keep" {
		t.Fatalf("exported spans = %v, want only keep", spanNames(got))
	}
}

func TestSetupTracerProvider_DefaultExportsAll(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	restoreProvider := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(restoreProvider) })
	shutdown := setupTracerProvider(resource.Empty(), exporter, &options{})
	t.Cleanup(func() { _ = shutdown(context.Background()) })
	tracer := otel.Tracer("test")
	for _, name := range []string{"first", "second"} {
		_, span := tracer.Start(context.Background(), name)
		span.End()
	}
	provider, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	if !ok {
		t.Fatalf("TracerProvider type = %T, want *sdktrace.TracerProvider", otel.GetTracerProvider())
	}
	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush() error = %v", err)
	}

	got := exporter.GetSpans()
	if len(got) != 2 {
		t.Fatalf("exported spans = %v, want first and second", spanNames(got))
	}
}

func spanNames(spans tracetest.SpanStubs) []string {
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.Name)
	}
	return names
}

func spanSnapshot(name string) sdktrace.ReadOnlySpan {
	return tracetest.SpanStub{
		Name:                 name,
		InstrumentationScope: instrumentation.Scope{Name: "test"},
	}.Snapshot()
}
