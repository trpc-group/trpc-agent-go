//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package langfuse provides custom tracer provider and span for Langfuse integration.
package langfuse

import (
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

var _ trace.TracerProvider = (*tracerProvider)(nil)

// tracerProvider wraps an existing TracerProvider to return custom tracers.
type tracerProvider struct {
	embedded.TracerProvider
	underlying trace.TracerProvider
}

// newTracerProvider creates a new tracerProvider.
func newTracerProvider(underlying trace.TracerProvider) *tracerProvider {
	return &tracerProvider{
		underlying: underlying,
	}
}

// Tracer returns a custom tracer that wraps the underlying tracer.
func (tp *tracerProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	underlyingTracer := tp.underlying.Tracer(name, options...)
	return &tracer{
		underlying: underlyingTracer,
	}
}
