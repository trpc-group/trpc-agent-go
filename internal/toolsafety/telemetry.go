//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolsafety

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Span attribute keys injected by the safety scanner. These are
// reserved but not auto-injected unless SetupSpan is called, so
// callers that don't use OpenTelemetry pay zero cost.
const (
	AttrDecision  = "tool.safety.decision"
	AttrRiskLevel = "tool.safety.risk_level"
	AttrRuleID    = "tool.safety.rule_id"
	AttrBackend   = "tool.safety.backend"
	AttrToolName  = "tool.safety.tool_name"
	AttrIntercept = "tool.safety.intercepted"
)

// SetupSpan injects safety-related attributes into the current
// OpenTelemetry span from context. It is a no-op when ctx has no
// active span or when the tracer is unavailable.
func SetupSpan(ctx context.Context, report ScanReport) {
	span := trace.SpanFromContext(ctx)
	if span == nil || !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String(AttrDecision, string(report.Decision)),
		attribute.String(AttrRiskLevel, string(report.RiskLevel)),
		attribute.String(AttrBackend, report.Backend),
		attribute.String(AttrToolName, report.ToolName),
		attribute.Bool(AttrIntercept, report.Intercepted),
	}
	if len(report.Findings) > 0 {
		// Attach the highest-severity rule ID.
		attrs = append(attrs,
			attribute.String(AttrRuleID, report.Findings[0].RuleID),
		)
	}
	span.SetAttributes(attrs...)
}
