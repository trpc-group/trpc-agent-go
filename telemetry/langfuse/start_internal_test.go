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

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"

	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

func TestStart_Internal_WithNoopProvider(t *testing.T) {
	ctx := context.Background()
	old := atrace.TracerProvider
	defer func() { atrace.TracerProvider = old }()

	atrace.TracerProvider = noop.NewTracerProvider()

	clean, err := start(ctx, otlptracehttp.WithEndpoint("localhost:4318"), otlptracehttp.WithInsecure())
	if err != nil {
		t.Fatalf("start() error = %v", err)
	}
	_ = clean(ctx)
}

func TestStart_Internal_WithExistingSDKProvider(t *testing.T) {
	ctx := context.Background()
	old := atrace.TracerProvider
	defer func() { atrace.TracerProvider = old }()

	// Use a real sdk tracer provider to hit the branch that registers processor
	atrace.TracerProvider = sdktrace.NewTracerProvider()

	clean, err := start(ctx, otlptracehttp.WithEndpoint("localhost:4318"), otlptracehttp.WithInsecure())
	if err != nil {
		t.Fatalf("start() error = %v", err)
	}
	// Cleanup
	_ = clean(ctx)
}
