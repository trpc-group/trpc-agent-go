//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSpanAttributes verifies that SpanAttributes returns correct key-value pairs.
func TestSpanAttributes(t *testing.T) {
	result := ScanResult{
		Decision:  DecisionDeny,
		RiskLevel: RiskLevelCritical,
		Findings: []Finding{
			{RuleID: "R-DEL-001"},
		},
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	}

	attrs := SpanAttributes(result)

	assert.Equal(t, "deny", attrs[SpanKeyDecision])
	assert.Equal(t, "critical", attrs[SpanKeyRiskLevel])
	assert.Equal(t, "R-DEL-001", attrs[SpanKeyRuleID])
	assert.Equal(t, "workspaceexec", attrs[SpanKeyBackend])
}

// TestSpanAttributes_NoFindings verifies that SpanAttributes handles empty findings.
func TestSpanAttributes_NoFindings(t *testing.T) {
	result := ScanResult{
		Decision:  DecisionAllow,
		RiskLevel: RiskLevelInfo,
		Findings:  []Finding{},
		ToolName:  "workspace_exec",
		Backend:   "workspaceexec",
	}

	attrs := SpanAttributes(result)

	assert.Equal(t, "allow", attrs[SpanKeyDecision])
	assert.Equal(t, "info", attrs[SpanKeyRiskLevel])
	assert.Equal(t, "", attrs[SpanKeyRuleID], "rule ID should be empty when no findings")
	assert.Equal(t, "workspaceexec", attrs[SpanKeyBackend])
}

// TestSetSpanAttributes verifies that SetSpanAttributes calls the setter function.
func TestSetSpanAttributes(t *testing.T) {
	result := ScanResult{
		Decision:  DecisionDeny,
		RiskLevel: RiskLevelHigh,
		Findings: []Finding{
			{RuleID: "R-NET-001"},
		},
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	}

	collected := make(map[string]string)
	SetSpanAttributes(result, func(k, v string) {
		collected[k] = v
	})

	assert.Equal(t, "deny", collected[SpanKeyDecision])
	assert.Equal(t, "high", collected[SpanKeyRiskLevel])
	assert.Equal(t, "R-NET-001", collected[SpanKeyRuleID])
	assert.Equal(t, "workspaceexec", collected[SpanKeyBackend])
}

// TestSetSpanAttributes_NilSetter verifies that a nil setter does not panic.
func TestSetSpanAttributes_NilSetter(t *testing.T) {
	result := ScanResult{
		Decision:  DecisionAllow,
		RiskLevel: RiskLevelInfo,
	}

	// Should not panic.
	SetSpanAttributes(result, nil)
}

// TestSpanAttributeConstants verifies span attribute key constants.
func TestSpanAttributeConstants(t *testing.T) {
	assert.Equal(t, "tool.safety.decision", SpanKeyDecision)
	assert.Equal(t, "tool.safety.risk_level", SpanKeyRiskLevel)
	assert.Equal(t, "tool.safety.rule_id", SpanKeyRuleID)
	assert.Equal(t, "tool.safety.backend", SpanKeyBackend)
}
