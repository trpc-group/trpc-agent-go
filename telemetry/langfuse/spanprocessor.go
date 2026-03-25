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
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func newSpanProcessor(e sdktrace.SpanExporter, mode SpanProcessorMode) (sdktrace.SpanProcessor, error) {
	var next sdktrace.SpanProcessor
	switch normalizeSpanProcessorMode(mode) {
	case SpanProcessorModeBatch:
		next = sdktrace.NewBatchSpanProcessor(e)
	case SpanProcessorModeSimple:
		next = sdktrace.NewSimpleSpanProcessor(e)
	default:
		return nil, fmt.Errorf("langfuse: unsupported span processor mode %q", mode)
	}
	return &baggageSpanProcessor{next: next}, nil
}

// baggageSpanProcessor wraps a span processor and copies baggage members
// from the span's parent context onto the span as attributes at start time.
//
// This mirrors the behavior of go.opentelemetry.io/contrib/processors/baggagecopy.
type baggageSpanProcessor struct {
	next sdktrace.SpanProcessor
}

var _ sdktrace.SpanProcessor = (*baggageSpanProcessor)(nil)

func (p *baggageSpanProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	for _, member := range baggage.FromContext(ctx).Members() {
		if defaultLangfuseTraceAttributeFilter(member) {
			span.SetAttributes(attribute.String(member.Key(), member.Value()))
		}
	}
	if p.next != nil {
		p.next.OnStart(ctx, span)
	}
}

// defaultLangfuseTraceAttributeFilter limits which baggage entries get propagated
// onto all spans as attributes for Langfuse querying/aggregation compatibility.
// https://langfuse.com/integrations/native/opentelemetry#propagating-attributes
//
// Propagated attributes:
// - userId: langfuse.user.id or user.id
// - sessionId: langfuse.session.id or session.id
// - metadata: langfuse.trace.metadata.* (top-level metadata keys)
// - version: langfuse.version
// - release: langfuse.release
// - tags: langfuse.trace.tags
func defaultLangfuseTraceAttributeFilter(member baggage.Member) bool {
	k := member.Key()
	switch k {
	case traceUserID, "user.id",
		traceSessionID, "session.id",
		version, release,
		traceTags:
		return true
	default:
		// Only propagate top-level metadata keys.
		// `traceMetadata` is "langfuse.trace.metadata", so allow "langfuse.trace.metadata.<key>".
		return strings.HasPrefix(k, traceMetadata+".")
	}
}

func (p *baggageSpanProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	if p.next != nil {
		p.next.OnEnd(span)
	}
}

func (p *baggageSpanProcessor) Shutdown(ctx context.Context) error {
	if p.next == nil {
		return nil
	}
	return p.next.Shutdown(ctx)
}

func (p *baggageSpanProcessor) ForceFlush(ctx context.Context) error {
	if p.next == nil {
		return nil
	}
	return p.next.ForceFlush(ctx)
}
