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

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// attributeRewritingExporter wraps a SpanExporter and rewrites span attributes
// immediately before export. In-process span processors still observe original
// attributes; only the export path sees the rewritten set.
type attributeRewritingExporter struct {
	next    sdktrace.SpanExporter
	rewrite AttributeRewriter
}

var _ sdktrace.SpanExporter = (*attributeRewritingExporter)(nil)

func (e *attributeRewritingExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if e == nil || e.next == nil {
		return nil
	}
	if e.rewrite == nil {
		return e.next.ExportSpans(ctx, spans)
	}
	out := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, span := range spans {
		out[i] = &attrRewrittenSpan{
			ReadOnlySpan: span,
			attrs:        e.rewrite(span.Attributes()),
		}
	}
	return e.next.ExportSpans(ctx, out)
}

func (e *attributeRewritingExporter) Shutdown(ctx context.Context) error {
	if e == nil || e.next == nil {
		return nil
	}
	return e.next.Shutdown(ctx)
}

// attrRewrittenSpan overrides Attributes() while delegating all other methods.
type attrRewrittenSpan struct {
	sdktrace.ReadOnlySpan
	attrs []attribute.KeyValue
}

func (s *attrRewrittenSpan) Attributes() []attribute.KeyValue {
	return s.attrs
}
