//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

//

package safety

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	// SpanAttrDecision is the OTel span attribute key for the safety decision.
	SpanAttrDecision = "tool.safety.decision"
	// SpanAttrRiskLevel is the OTel span attribute key for the risk level.
	SpanAttrRiskLevel = "tool.safety.risk_level"
	// SpanAttrRuleID is the OTel span attribute key for the matched rule ID.
	SpanAttrRuleID = "tool.safety.rule_id"
	// SpanAttrBackend is the OTel span attribute key for the execution backend.
	SpanAttrBackend = "tool.safety.backend"
	// SpanNameToolSafety is the span name for tool safety checks.
	SpanNameToolSafety = "tool.safety.check"
)

// tracerName is the OTel tracer name for the safety package.
const tracerName = "trpc-agent-go/tool/safety"

// AddSafetySpanAttributes sets the standard safety span attributes
// on the current span in the context.
func AddSafetySpanAttributes(ctx context.Context, report ScanReport) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(
		attribute.String(SpanAttrDecision, string(report.Decision)),
		attribute.String(SpanAttrRiskLevel, string(report.RiskLevel)),
		attribute.String(SpanAttrRuleID, report.RuleID),
		attribute.String(SpanAttrBackend, report.Backend),
	)
}

// StartSafetySpan starts a new safety-check span and returns the
// updated context. The caller is responsible for calling span.End().
func StartSafetySpan(ctx context.Context, toolName string) (context.Context, trace.Span) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, SpanNameToolSafety,
		trace.WithAttributes(
			attribute.String("tool.name", toolName),
		),
	)
	return ctx, span
}
