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

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// dummyExporter implements sdktrace.SpanExporter for testing
type dummyExporter struct{}

func (d *dummyExporter) ExportSpans(_ context.Context, _ []sdktrace.ReadOnlySpan) error { return nil }
func (d *dummyExporter) Shutdown(_ context.Context) error                               { return nil }

func TestNewSpanProcessor(t *testing.T) {
	exp := &dummyExporter{}
	sp := newSpanProcessor(exp)
	if sp == nil {
		t.Fatalf("newSpanProcessor returned nil")
	}
}
