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

	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

func spanSnapshot(name string) sdktrace.ReadOnlySpan {
	return tracetest.SpanStub{
		Name:                 name,
		InstrumentationScope: instrumentation.Scope{Name: "test"},
	}.Snapshot()
}
