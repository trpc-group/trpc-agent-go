//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package trace extends the trpc-agent-go trace semantic conventions with
// tool-safety guard attributes. The constants here are the canonical OTel
// attribute keys for safety scanning; tool/safety re-exports them as
// safety.Key* aliases for callers that do not want to import this
// package.
package trace

const (
	// KeyToolSafetyDecision records the safety decision
	// (allow/deny/ask) for one tool call.
	KeyToolSafetyDecision = "tool.safety.decision"
	// KeyToolSafetyRiskLevel records the aggregated risk level.
	KeyToolSafetyRiskLevel = "tool.safety.risk_level"
	// KeyToolSafetyRuleID records the primary rule id.
	KeyToolSafetyRuleID = "tool.safety.rule_id"
	// KeyToolSafetyRuleIDs records every firing rule id.
	KeyToolSafetyRuleIDs = "tool.safety.rule_ids"
	// KeyToolSafetyBackend records the execution backend.
	KeyToolSafetyBackend = "tool.safety.backend"
	// KeyToolSafetyIntercepted records whether execution was blocked.
	KeyToolSafetyIntercepted = "tool.safety.intercepted"
	// KeyToolSafetyRedacted records whether any redaction was applied.
	KeyToolSafetyRedacted = "tool.safety.redacted"
)
