//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// OpenTelemetry attribute keys emitted by the safety guard.
const (
	AttrDecision  = "tool.safety.decision"
	AttrRiskLevel = "tool.safety.risk_level"
	AttrRuleID    = "tool.safety.rule_id"
	AttrBackend   = "tool.safety.backend"
)

// SetSpanAttributes records report attributes on the active recording span.
func SetSpanAttributes(ctx context.Context, report Report) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(report.SpanAttributes()...)
}

func safetyAttributes(report Report) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(AttrDecision, string(report.Decision)),
		attribute.String(AttrRiskLevel, string(report.RiskLevel)),
		attribute.String(AttrRuleID, report.PrimaryRuleID()),
		attribute.String(AttrBackend, string(report.Backend)),
	}
}
