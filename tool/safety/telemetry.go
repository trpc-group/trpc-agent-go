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

	"go.opentelemetry.io/otel/trace"
)

// SpanAttribute is one OTel span attribute the guard projects onto the
// active execute-tool span. The Value is one of string, bool, int, int64,
// or []string; the helper in telemetry.go normalizes it.
type SpanAttribute struct {
	Key   string
	Value any
}

// telemetryProject reports the safety attributes for one scan onto the
// active span in ctx. It does not create a new span; the framework
// already runs the execute-tool span, and the guard adds attributes to
// it. When no span is active, the call is a no-op.
func telemetryProject(ctx context.Context, attrs []SpanAttribute) {
	if ctx == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	if span == nil || !span.IsRecording() {
		return
	}
	for _, a := range attrs {
		setSpanAttribute(span, a)
	}
}

// setSpanAttribute converts a.Value to the OTel-typed setter and applies
// it to span. Unknown value types are skipped; the guard only emits
// bounded enums, booleans, and rule ids.
func setSpanAttribute(span trace.Span, a SpanAttribute) {
	switch v := a.Value.(type) {
	case string:
		span.SetAttributes(keyString(a.Key, v))
	case bool:
		span.SetAttributes(keyBool(a.Key, v))
	case int:
		span.SetAttributes(keyInt(a.Key, v))
	case int64:
		span.SetAttributes(keyInt64(a.Key, v))
	case []string:
		span.SetAttributes(keyStringSlice(a.Key, v))
	}
}

// safetyAttributes builds the OTel attribute set for a ScanReport.
// It includes both the singular primary rule id (KeyToolSafetyRuleID)
// and the full rule id list (KeyToolSafetyRuleIDs) so consumers can
// query either level.
func safetyAttributes(report ScanReport) []SpanAttribute {
	attrs := []SpanAttribute{
		{Key: KeyToolSafetyDecision, Value: string(report.Decision)},
		{Key: KeyToolSafetyRiskLevel, Value: string(report.RiskLevel)},
		{Key: KeyToolSafetyBackend, Value: string(report.Backend)},
		{Key: KeyToolSafetyRuleIDs, Value: ruleIDsFromFindings(report.Findings)},
		{Key: KeyToolSafetyIntercepted, Value: report.Intercepted},
		{Key: KeyToolSafetyRedacted, Value: report.Redacted},
	}
	// Singular primary rule id: the first finding's rule id, or empty
	// when there are no findings. Consumers that only query one
	// attribute get the most important rule id without scanning the
	// list.
	primary := ""
	if len(report.Findings) > 0 {
		primary = report.Findings[0].RuleID
	}
	attrs = append(attrs, SpanAttribute{
		Key: KeyToolSafetyRuleID, Value: primary,
	})
	return attrs
}

// OTel attribute key constants. These are exported so callers can
// reference them from telemetry policies or test assertions.
const (
	// KeyToolSafetyDecision records the safety decision.
	KeyToolSafetyDecision = "tool.safety.decision"
	// KeyToolSafetyRiskLevel records the aggregated risk level.
	KeyToolSafetyRiskLevel = "tool.safety.risk_level"
	// KeyToolSafetyRuleID records the primary (first) rule id. Always
	// accompanied by KeyToolSafetyRuleIDs.
	KeyToolSafetyRuleID = "tool.safety.rule_id"
	// KeyToolSafetyRuleIDs records every firing rule id.
	KeyToolSafetyRuleIDs = "tool.safety.rule_ids"
	// KeyToolSafetyBackend records the execution backend.
	KeyToolSafetyBackend = "tool.safety.backend"
	// KeyToolSafetyIntercepted records whether the call was blocked.
	KeyToolSafetyIntercepted = "tool.safety.intercepted"
	// KeyToolSafetyRedacted records whether any redaction was applied.
	KeyToolSafetyRedacted = "tool.safety.redacted"
)
