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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)
	b, err := baggage.New(m)
	require.NoError(t, err)
	ctx = baggage.ContextWithBaggage(ctx, b)

	exp := &recordingExporter{}
	sp := newSpanProcessor(exp)

	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sp))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tr := tp.Tracer("test")
	_, span := tr.Start(ctx, "span")
	span.End()

	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := exp.snapshot()
	require.Len(t, spans, 1)

	want := attribute.String(traceUserID, "user-123")
	assert.Contains(t, spans[0].Attributes(), want)
}

func TestNewSpanProcessor_DefaultFilter_IgnoresNonLangfuseKeys(t *testing.T) {
	ctx := context.Background()

	m, err := baggage.NewMemberRaw("baggage.test", "baggage value")
	require.NoError(t, err)
	b, err := baggage.New(m)
	require.NoError(t, err)
	ctx = baggage.ContextWithBaggage(ctx, b)

	exp := &recordingExporter{}
	sp := newSpanProcessor(exp)

	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sp))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tr := tp.Tracer("test")
	_, span := tr.Start(ctx, "span")
	span.End()

	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := exp.snapshot()
	require.Len(t, spans, 1)

	unwanted := attribute.String("baggage.test", "baggage value")
	assert.NotContains(t, spans[0].Attributes(), unwanted)
}

func TestBaggageBatchSpanProcessor_NilNext_Noops(t *testing.T) {
	p := &baggageBatchSpanProcessor{next: nil}
	ctx := context.Background()

	require.NoError(t, p.Shutdown(ctx))
	require.NoError(t, p.ForceFlush(ctx))
}
