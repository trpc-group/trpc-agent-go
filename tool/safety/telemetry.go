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
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	// AttributeDecision is the normalized safety decision span attribute.
	AttributeDecision = "tool.safety.decision"
	// AttributeRiskLevel is the highest safety risk span attribute.
	AttributeRiskLevel = "tool.safety.risk_level"
	// AttributeRuleID is the primary matched rule span attribute.
	AttributeRuleID = "tool.safety.rule_id"
	// AttributeBackend identifies the execution boundary span attribute.
	AttributeBackend = "tool.safety.backend"
	// AttributeBlocked reports whether execution was intercepted.
	AttributeBlocked = "tool.safety.blocked"
	// AttributeRedacted reports whether sensitive text was removed.
	AttributeRedacted = "tool.safety.redacted"
	// AttributeScanDurationMS records scan latency in milliseconds.
	AttributeScanDurationMS = "tool.safety.scan_duration_ms"
)

// RecordSpan writes only bounded, non-payload safety attributes to the current
// span. Commands, scripts, evidence, recommendations, arguments, environment
// values, and outputs are deliberately excluded.
func RecordSpan(ctx context.Context, report Report) {
	span := oteltrace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String(AttributeDecision, string(report.Decision)),
		attribute.String(AttributeRiskLevel, string(report.RiskLevel)),
		attribute.String(AttributeRuleID, report.RuleID),
		attribute.String(AttributeBackend, string(normalizeBackend(report.Backend))),
		attribute.Bool(AttributeBlocked, report.Blocked),
		attribute.Bool(AttributeRedacted, report.Redacted),
		attribute.Int64(AttributeScanDurationMS, report.DurationMS),
	)
}
