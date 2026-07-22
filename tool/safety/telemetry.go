//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "go.opentelemetry.io/otel/attribute"

// SpanAttributes converts a report into low-cardinality OpenTelemetry fields.
func SpanAttributes(report Report) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("tool.safety.decision", string(report.Decision)),
		attribute.String("tool.safety.risk_level", string(report.RiskLevel)),
		attribute.String("tool.safety.rule_id", report.RuleID),
		attribute.String("tool.safety.backend", string(report.Backend)),
	}
}
