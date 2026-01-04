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
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// dummyExporter implements sdktrace.SpanExporter for testing
type dummyExporter struct{}

func (d *dummyExporter) ExportSpans(_ context.Context, _ []sdktrace.ReadOnlySpan) error { return nil }
func (d *dummyExporter) Shutdown(_ context.Context) error                               { return nil }

type recordingExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *recordingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *recordingExporter) Shutdown(_ context.Context) error { return nil }

func (e *recordingExporter) snapshot() []sdktrace.ReadOnlySpan {
	e.mu.Lock()
	defer e.mu.Unlock()
	cp := make([]sdktrace.ReadOnlySpan, len(e.spans))
	copy(cp, e.spans)
	return cp
}

func TestNewSpanProcessor(t *testing.T) {
	exp := &dummyExporter{}
	sp := newSpanProcessor(exp)
	if sp == nil {
		t.Fatalf("newSpanProcessor returned nil")
	}
}

func TestNewSpanProcessor_CopiesBaggageToSpanAttributes(t *testing.T) {
	ctx := context.Background()

	// Use NewMemberRaw to allow spaces (mirrors opentelemetry-go-contrib/processors/baggagecopy tests).
	m, err := baggage.NewMemberRaw(traceUserID, "user-123")
	if err != nil {
		t.Fatalf("baggage.NewMemberRaw: %v", err)
	}
	b, err := baggage.New(m)
	if err != nil {
		t.Fatalf("baggage.New: %v", err)
	}
	ctx = baggage.ContextWithBaggage(ctx, b)

	exp := &recordingExporter{}
	sp := newSpanProcessor(exp)

	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sp))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tr := tp.Tracer("test")
	_, span := tr.Start(ctx, "span")
	span.End()

	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	spans := exp.snapshot()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}

	want := attribute.String(traceUserID, "user-123")
	found := false
	for _, kv := range spans[0].Attributes() {
		if kv == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected baggage attribute %v to be present in exported span attributes: %v", want, spans[0].Attributes())
	}
}

func TestNewSpanProcessor_DefaultFilter_IgnoresNonLangfuseKeys(t *testing.T) {
	ctx := context.Background()

	m, err := baggage.NewMemberRaw("baggage.test", "baggage value")
	if err != nil {
		t.Fatalf("baggage.NewMemberRaw: %v", err)
	}
	b, err := baggage.New(m)
	if err != nil {
		t.Fatalf("baggage.New: %v", err)
	}
	ctx = baggage.ContextWithBaggage(ctx, b)

	exp := &recordingExporter{}
	sp := newSpanProcessor(exp)

	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sp))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tr := tp.Tracer("test")
	_, span := tr.Start(ctx, "span")
	span.End()

	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	spans := exp.snapshot()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}

	unwanted := attribute.String("baggage.test", "baggage value")
	for _, kv := range spans[0].Attributes() {
		if kv == unwanted {
			t.Fatalf("did not expect non-langfuse baggage attribute %v to be present; got: %v", unwanted, spans[0].Attributes())
		}
	}
}
