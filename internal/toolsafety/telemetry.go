// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName = "trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

// SpanAttrs returns OTel span attributes from a ScanReport for tracing.
func SpanAttrs(report *ScanReport) []attribute.KeyValue {
	if report == nil {
		return nil
	}
	attrs := []attribute.KeyValue{
		attribute.String("tool.safety.decision", string(report.Decision)),
		attribute.String("tool.safety.risk_level", string(report.RiskLevel)),
		attribute.String("tool.safety.backend", report.Backend),
		attribute.Int64("tool.safety.duration_ms", report.Duration.Milliseconds()),
	}
	if len(report.Findings) > 0 {
		attrs = append(attrs, attribute.String("tool.safety.rule_id", string(report.Findings[0].RuleID)))
	}
	return attrs
}

// AddSpanEvent adds a safety event to the current span if one exists in ctx.
func AddSpanEvent(ctx context.Context, report *ScanReport) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	attrs := SpanAttrs(report)
	span.AddEvent("tool.safety.check", trace.WithAttributes(attrs...))
}

// Tracer returns the package-level tracer.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}
