// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

// TelemetryAttributes returns low-cardinality safety attributes.
func TelemetryAttributes(report Report) map[string]any {
	return map[string]any{
		AttrDecision:  string(report.Decision),
		AttrRiskLevel: string(report.RiskLevel),
		AttrRuleID:    primaryRuleID(report.RuleIDs),
		AttrBackend:   string(report.Backend),
		AttrBlocked:   report.Blocked,
		AttrRedacted:  report.Redacted,
	}
}
