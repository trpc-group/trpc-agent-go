// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

const (
	// spanNameSafetyScan is the OTel span name for a safety scan.
	// It follows the dot-separated naming convention established in
	// graph/state_graph.go and internal/flow/llmflow/diagnostics.go.
	spanNameSafetyScan = "tool.safety.scan"
)

// Span attribute keys. Only non-sensitive metadata is recorded.
// Complete commands, environment variables, secrets, and unredacted
// evidence snippets are deliberately excluded from spans.
const (
	spanAttrDecision    = "tool.safety.decision"
	spanAttrRiskLevel   = "tool.safety.risk_level"
	spanAttrRuleID      = "tool.safety.rule_id"
	spanAttrBackend     = "tool.safety.backend"
	spanAttrIntercepted = "tool.safety.intercepted"
	spanAttrDurationMS  = "tool.safety.duration_ms"
)

// startSafetySpan starts an OTel span for a safety scan. The span is a
// child of whatever span is already present in ctx, attaching to the
// existing trace context when one exists.
//
// When no OTel provider is configured the global tracer is a no-op
// (see telemetry/trace.trace.go), so this call has no side effects and
// the returned span is a no-op. The started flag is always true because
// the global tracer always returns a usable span (real or no-op). It is
// retained for consistency with the startedSpan pattern used in
// graph/state_graph.go (startNodeSpan) and
// internal/flow/llmflow/diagnostics.go (startLatencySpan).
func startSafetySpan(ctx context.Context) (context.Context, oteltrace.Span, bool) {
	ctx, span := trace.Tracer.Start(ctx, spanNameSafetyScan)
	return ctx, span, true
}

// finishSafetySpan ends the span and records an error if err is non-nil.
// It is the safety-package counterpart to finishLatencySpan in
// internal/flow/llmflow/diagnostics.go.
func finishSafetySpan(span oteltrace.Span, started bool, err error) {
	if !started {
		return
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// recordSafetyAttributes sets non-sensitive attributes on the span
// derived from the report. Only metadata-level fields are recorded:
// decision, risk level, first rule ID, backend, intercepted flag, and
// duration.
//
// The raw command, evidence snippets (MatchedSnippet), reasons, and
// recommendation are never recorded to prevent secret leakage — even
// though the Scanner redacts Report.Command and Evidence.MatchedSnippet,
// the redacted text is still excluded from spans as defense-in-depth.
func recordSafetyAttributes(span oteltrace.Span, started bool, report Report) {
	if !started {
		return
	}
	span.SetAttributes(
		attribute.String(spanAttrDecision, string(report.Decision)),
		attribute.String(spanAttrRiskLevel, string(report.RiskLevel)),
		attribute.String(spanAttrRuleID, firstRuleID(report.Evidences)),
		attribute.String(spanAttrBackend, report.Backend),
		attribute.Bool(spanAttrIntercepted, report.Intercepted),
		attribute.Int64(spanAttrDurationMS, report.DurationMS),
	)
}

// firstRuleID returns the first evidence rule ID or an empty string.
// Only the first rule ID is recorded to keep span attributes compact;
// the full list is available in the audit log via JSONLAuditor.
func firstRuleID(evidences []Evidence) string {
	if len(evidences) == 0 {
		return ""
	}
	return evidences[0].RuleID
}
