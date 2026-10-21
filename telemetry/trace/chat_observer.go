//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trace

import (
	"go.opentelemetry.io/otel/trace"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
)

// RegisterChatAttributeObserver registers an observer for JSON payload attributes
// produced by framework chat tracing, before OpenTelemetry SDK size/count limits.
// It does not observe arbitrary Span.SetAttributes calls or change exported spans.
//
// The observer receives the span, attribute key, and complete serialized JSON only
// when SpanAttributePolicy allows the entire value. Drop, Omit, Truncate overflow,
// serialization failures, and removed tool definitions deliver nil, so consumers
// can discard previous data for that key. Omit/Truncate values within their byte
// threshold remain available.
// Attribute keys and JSON schemas are the same as the corresponding chat span
// attributes. In particular, request fields excluded from JSON remain excluded.
//
// Callbacks run synchronously before SetAttributes, only for recording spans.
// Requests follow the existing per-span request cache; responses follow tracing
// updates and may contain deltas, final snapshots, or usage-only chunks. Observers
// must be fast, safe for concurrent spans, and must not mutate the span or value,
// panic, or recursively invoke chat tracing. The value is borrowed for the call;
// copy any data retained afterwards. No span lifecycle or storage is provided:
// exporters should pair observation with their provider's SpanProcessor.OnEnd.
//
// Registration is process-wide; consumers may filter by span.TracerProvider().
// Register before starting calls. A nil observer is a no-op. The returned function
// unregisters once and is safe to call concurrently; callbacks already in flight
// may finish afterwards. Unregister before shutting down consumer resources.
func RegisterChatAttributeObserver(observe func(trace.Span, string, []byte)) func() {
	return itelemetry.RegisterChatAttributeObserver(observe)
}
