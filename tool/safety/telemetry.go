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

	semconv "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

// spanAttribute is one OTel span attribute the guard projects onto the
// active execute-tool span. The Value is one of string, bool, int, int64,
// or []string; the helper in telemetry.go normalizes it.
type spanAttribute struct {
	Key   string
	Value any
}

// telemetryProject reports the safety attributes for one scan onto the
// active span in ctx. It does not create a new span; the framework
// already runs the execute-tool span, and the guard adds attributes to
// it. When no span is active, the call is a no-op.
func telemetryProject(ctx context.Context, attrs []spanAttribute) {
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
func setSpanAttribute(span trace.Span, a spanAttribute) {
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
func safetyAttributes(report ScanReport) []spanAttribute {
	attrs := []spanAttribute{
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
	attrs = append(attrs, spanAttribute{
		Key: KeyToolSafetyRuleID, Value: primary,
	})
	return attrs
}

// OTel attribute key constants. These alias the canonical keys defined in
// telemetry/semconv/trace so consumers of either package always see the
// same attribute names. They are exported so callers can reference them
// from telemetry policies or test assertions.
const (
	// KeyToolSafetyDecision records the safety decision.
	KeyToolSafetyDecision = semconv.KeyToolSafetyDecision
	// KeyToolSafetyRiskLevel records the aggregated risk level.
	KeyToolSafetyRiskLevel = semconv.KeyToolSafetyRiskLevel
	// KeyToolSafetyRuleID records the primary (first) rule id. Always
	// accompanied by KeyToolSafetyRuleIDs.
	KeyToolSafetyRuleID = semconv.KeyToolSafetyRuleID
	// KeyToolSafetyRuleIDs records every firing rule id.
	KeyToolSafetyRuleIDs = semconv.KeyToolSafetyRuleIDs
	// KeyToolSafetyBackend records the execution backend.
	KeyToolSafetyBackend = semconv.KeyToolSafetyBackend
	// KeyToolSafetyIntercepted records whether the call was blocked.
	KeyToolSafetyIntercepted = semconv.KeyToolSafetyIntercepted
	// KeyToolSafetyRedacted records whether any redaction was applied.
	KeyToolSafetyRedacted = semconv.KeyToolSafetyRedacted
)
