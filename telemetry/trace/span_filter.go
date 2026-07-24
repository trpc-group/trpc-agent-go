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

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// SpanStartFilter decides whether a span should be recorded when it is
// created. The parameters contain only information available at span start.
// Returning false drops the span before any attributes or events are recorded.
type SpanStartFilter func(sdktrace.SamplingParameters) bool

// SpanExportFilter decides whether a completed span should be exported.
// Returning false prevents the span from being sent to the configured
// exporter. Unlike SpanStartFilter, it can inspect final attributes, status,
// events, and duration.
type SpanExportFilter func(sdktrace.ReadOnlySpan) bool

// WithSpanStartFilter filters spans when they are created. A nil filter
// disables start filtering. When specified more than once, the last option
// wins.
func WithSpanStartFilter(filter SpanStartFilter) Option {
	return func(opts *options) {
		opts.spanStartFilter = filter
	}
}

// WithSpanExportFilter filters completed spans immediately before export. A
// nil filter disables export filtering. When specified more than once, the
// last option wins.
func WithSpanExportFilter(filter SpanExportFilter) Option {
	return func(opts *options) {
		opts.spanExportFilter = filter
	}
}

type filteringSampler struct {
	filter SpanStartFilter
	next   sdktrace.Sampler
}

func (s filteringSampler) ShouldSample(params sdktrace.SamplingParameters) sdktrace.SamplingResult {
	if s.filter != nil && !s.filter(params) {
		parent := oteltrace.SpanContextFromContext(params.ParentContext)
		return sdktrace.SamplingResult{
			Decision:   sdktrace.Drop,
			Tracestate: parent.TraceState(),
		}
	}
	return s.next.ShouldSample(params)
}

func (s filteringSampler) Description() string {
	return "trpc-agent-go span start filter"
}

type filteringSpanExporter struct {
	filter SpanExportFilter
	next   sdktrace.SpanExporter
}

func newFilteringSpanExporter(
	next sdktrace.SpanExporter,
	filter SpanExportFilter,
) sdktrace.SpanExporter {
	if filter == nil {
		return next
	}
	return &filteringSpanExporter{
		filter: filter,
		next:   next,
	}
}

func (e *filteringSpanExporter) ExportSpans(
	ctx context.Context,
	spans []sdktrace.ReadOnlySpan,
) error {
	filtered := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, span := range spans {
		if e.filter(span) {
			filtered = append(filtered, span)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return e.next.ExportSpans(ctx, filtered)
}

func (e *filteringSpanExporter) Shutdown(ctx context.Context) error {
	return e.next.Shutdown(ctx)
}
