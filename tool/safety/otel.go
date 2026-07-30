//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "strconv"

// ToSpanAttributes converts the report into a map of OpenTelemetry
// span attributes. Callers with OTel enabled can set these directly
// on their spans:
//
//	report := scanner.Scan(ctx, req)
//	span.SetAttributes(report.ToSpanAttributes()...)
//
// The attribute keys follow the convention specified in the issue:
// tool.safety.decision, tool.safety.risk_level, tool.safety.rule_id,
// tool.safety.backend, tool.safety.blocked, tool.safety.duration_ms.
//
// This module does NOT import any OTel packages — it only produces a
// key-value map that OTel consumers can apply themselves. This keeps
// the safety package dependency-free from telemetry SDKs.
func (r *SafetyReport) ToSpanAttributes() map[string]string {
	return map[string]string{
		"tool.safety.decision":    string(r.Decision),
		"tool.safety.risk_level":  string(r.RiskLevel),
		"tool.safety.rule_id":     r.RuleID,
		"tool.safety.backend":     r.Backend,
		"tool.safety.blocked":     strconv.FormatBool(r.Blocked),
		"tool.safety.duration_ms": strconv.FormatInt(r.DurationMs, 10),
	}
}
