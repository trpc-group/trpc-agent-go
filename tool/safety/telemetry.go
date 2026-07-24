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

// Span attribute keys set on the active execute-tool span. They let an
// OpenTelemetry backend filter and aggregate guard decisions.
const (
	// AttrDecision is the guard decision (allow/deny/needs_human_review).
	AttrDecision = "tool.safety.decision"
	// AttrRiskLevel is the highest risk level observed.
	AttrRiskLevel = "tool.safety.risk_level"
	// AttrRuleID is the set of triggered rule ids.
	AttrRuleID = "tool.safety.rule_id"
	// AttrBackend is the resolved backend (workspace_exec/host/code).
	AttrBackend = "tool.safety.backend"
	// AttrBlocked reports whether execution was prevented.
	AttrBlocked = "tool.safety.blocked"
)

// writeSpanAttrs records the guard decision on the active span. When there is
// no recording span (no tracer configured) SpanFromContext returns a no-op and
// this is a cheap no-op too.
func writeSpanAttrs(ctx context.Context, r Report) {
	span := oteltrace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(
		attribute.String(AttrDecision, string(r.Decision)),
		attribute.String(AttrRiskLevel, string(r.RiskLevel)),
		attribute.String(AttrBackend, r.Backend),
		attribute.StringSlice(AttrRuleID, r.ruleIDs()),
		attribute.Bool(AttrBlocked, r.Blocked),
	)
}
