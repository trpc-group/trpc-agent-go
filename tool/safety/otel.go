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
// span attribute key-value pairs. Callers with OTel enabled can set
// these on their spans:
//
//	for k, v := range report.ToSpanAttributes() {
//	    span.SetAttributes(attribute.String(k, v))
//	}
//
// This module does NOT import any OTel packages — it produces string
// data for OTel consumers, keeping the safety package dependency-free.
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
