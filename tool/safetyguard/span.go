//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safetyguard

import (
	"context"
	"encoding/json"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// OpenTelemetry span attribute keys reserved by the safety Guard. They are
// exported so exporters can reference the same names in their span
// attribute policy (SpanAttributePolicy) and dashboards.
const (
	// SpanAttrDecision records the resulting permission action
	// (allow/deny/ask).
	SpanAttrDecision = "tool.safety.decision"
	// SpanAttrRiskLevel records the aggregate risk level
	// (none/low/medium/high/critical).
	SpanAttrRiskLevel = "tool.safety.risk_level"
	// SpanAttrFindingCount records the number of findings produced.
	SpanAttrFindingCount = "tool.safety.finding_count"
	// SpanAttrFindingTypes records the distinct finding categories,
	// comma-joined, ordered by descending risk.
	SpanAttrFindingTypes = "tool.safety.finding_types"
	// SpanAttrPolicyVersion records the active policy version.
	SpanAttrPolicyVersion = "tool.safety.policy_version"
	// SpanAttrHostExec records whether the scanned tool was flagged as a
	// host-exec surface ("true"/"false").
	SpanAttrHostExec = "tool.safety.host_exec"
)

// recordSpan sets the reserved safety span attributes on the active span
// in ctx. When no span is active (span.IsRecording() == false) the call is
// a no-op, so the Guard can be used without an OpenTelemetry tracer
// installed. Attribute values are best-effort: the finding-types list is
// marshaled to JSON and capped at a small size so the span is not bloated
// by a pathological command.
func recordSpan(ctx context.Context, report ScanReport) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(
		attribute.String(SpanAttrDecision, report.Decision),
		attribute.String(SpanAttrRiskLevel, string(report.RiskLevel)),
		attribute.Int(SpanAttrFindingCount, len(report.Findings)),
		attribute.String(SpanAttrFindingTypes, findingTypes(report.Findings)),
		attribute.String(SpanAttrPolicyVersion, report.PolicyVersion),
		attribute.String(SpanAttrHostExec, boolStr(report.HostExec)),
	)
}

func findingTypes(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(findings))
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		if _, ok := seen[f.Type]; ok {
			continue
		}
		seen[f.Type] = struct{}{}
		out = append(out, f.Type)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(b)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
