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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// OpenTelemetry span attribute keys set by the safety guard. They follow the
// OTel dotted-namespace convention.
const (
	AttrDecision  = "tool.safety.decision"
	AttrRiskLevel = "tool.safety.risk_level"
	AttrRuleID    = "tool.safety.rule_id"
	AttrBackend   = "tool.safety.backend"
)

// SetSpanAttributes records the scan verdict on the span in ctx, if any is
// recording. It is a no-op when there is no active recording span.
func SetSpanAttributes(ctx context.Context, report ScanReport) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String(AttrDecision, string(report.Decision)),
		attribute.String(AttrRiskLevel, string(report.RiskLevel)),
		attribute.String(AttrBackend, string(report.Backend)),
	}
	if id := report.PrimaryRuleID(); id != "" {
		attrs = append(attrs, attribute.String(AttrRuleID, id))
	}
	span.SetAttributes(attrs...)
}
