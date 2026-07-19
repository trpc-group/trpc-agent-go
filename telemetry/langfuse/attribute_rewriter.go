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
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// attributeRewritingExporter wraps a SpanExporter and rewrites span attributes
// immediately before export. Prefer wiring AttributeRewriter into the Langfuse
// exporter (after observation transforms); this wrapper remains for tests and
// non-Langfuse exporters.
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

// rewriteTransformedSpans applies rewriter to each span's attributes after
// Langfuse observation transforms. Nil rewriter is a no-op.
func rewriteTransformedSpans(ss []*tracepb.ResourceSpans, rewrite AttributeRewriter) []*tracepb.ResourceSpans {
	if rewrite == nil || len(ss) == 0 {
		return ss
	}
	for _, rs := range ss {
		if rs == nil {
			continue
		}
		for _, scopeSpans := range rs.ScopeSpans {
			if scopeSpans == nil {
				continue
			}
			for _, span := range scopeSpans.Spans {
				if span == nil {
					continue
				}
				span.Attributes = rewriteProtoAttributes(span.Attributes, rewrite)
			}
		}
	}
	return ss
}

// rewriteProtoAttributes converts OTLP attributes to SDK KeyValues, runs
// rewrite, and converts back. Unsupported value types are preserved as-is by
// stringifying when possible; unknown types are dropped on the round-trip.
func rewriteProtoAttributes(attrs []*commonpb.KeyValue, rewrite AttributeRewriter) []*commonpb.KeyValue {
	if rewrite == nil || len(attrs) == 0 {
		return attrs
	}
	sdkAttrs := make([]attribute.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		if attr == nil {
			continue
		}
		if kv, ok := protoAttrToKeyValue(attr); ok {
			sdkAttrs = append(sdkAttrs, kv)
		}
	}
	rewritten := rewrite(sdkAttrs)
	out := make([]*commonpb.KeyValue, 0, len(rewritten))
	for _, kv := range rewritten {
		out = append(out, keyValueToProtoAttr(kv))
	}
	return out
}

func protoAttrToKeyValue(attr *commonpb.KeyValue) (attribute.KeyValue, bool) {
	if attr == nil || attr.Value == nil {
		return attribute.KeyValue{}, false
	}
	key := attribute.Key(attr.Key)
	switch v := attr.Value.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return key.String(v.StringValue), true
	case *commonpb.AnyValue_IntValue:
		return key.Int64(v.IntValue), true
	case *commonpb.AnyValue_DoubleValue:
		return key.Float64(v.DoubleValue), true
	case *commonpb.AnyValue_BoolValue:
		return key.Bool(v.BoolValue), true
	case *commonpb.AnyValue_BytesValue:
		return key.String(string(v.BytesValue)), true
	default:
		return attribute.KeyValue{}, false
	}
}

func keyValueToProtoAttr(kv attribute.KeyValue) *commonpb.KeyValue {
	attr := &commonpb.KeyValue{Key: string(kv.Key)}
	switch kv.Value.Type() {
	case attribute.BOOL:
		attr.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: kv.Value.AsBool()}}
	case attribute.INT64:
		attr.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: kv.Value.AsInt64()}}
	case attribute.FLOAT64:
		attr.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: kv.Value.AsFloat64()}}
	case attribute.STRING:
		attr.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: kv.Value.AsString()}}
	default:
		attr.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: kv.Value.Emit()}}
	}
	return attr
}
