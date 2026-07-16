//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

// OTel span attribute keys for tool safety.
const (
	// SpanKeyDecision is the OTel span attribute key for the safety decision.
	SpanKeyDecision = "tool.safety.decision"
	// SpanKeyRiskLevel is the OTel span attribute key for the risk level.
	SpanKeyRiskLevel = "tool.safety.risk_level"
	// SpanKeyRuleID is the OTel span attribute key for the matched rule ID.
	SpanKeyRuleID = "tool.safety.rule_id"
	// SpanKeyBackend is the OTel span attribute key for the execution backend.
	SpanKeyBackend = "tool.safety.backend"
)

// SpanAttributes returns key-value pairs for OTel span attributes from a
// ScanResult. This does not depend on the OTel SDK directly; callers can
// use these to set span attributes via their preferred OTel integration.
func SpanAttributes(result ScanResult) map[string]string {
	ruleID := ""
	if len(result.Findings) > 0 {
		ruleID = result.Findings[0].RuleID
	}
	return map[string]string{
		SpanKeyDecision:  string(result.Decision),
		SpanKeyRiskLevel: string(result.RiskLevel),
		SpanKeyRuleID:    ruleID,
		SpanKeyBackend:   result.Backend,
	}
}

// SetSpanAttributes is a convenience function that sets safety span attributes
// on the given attribute setter, if the setter is non-nil. The setter function
// receives key-value pairs.
func SetSpanAttributes(result ScanResult, setter func(string, string)) {
	if setter == nil {
		return
	}
	for k, v := range SpanAttributes(result) {
		setter(k, v)
	}
}
