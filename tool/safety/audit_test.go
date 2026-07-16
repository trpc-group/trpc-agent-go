//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewAuditWriter verifies that an AuditWriter can be created.
func TestNewAuditWriter(t *testing.T) {
	var buf bytes.Buffer
	aw := NewAuditWriter(&buf)
	require.NotNil(t, aw)
}

// TestAuditWriter_WriteEvent verifies writing a single audit event.
func TestAuditWriter_WriteEvent(t *testing.T) {
	var buf bytes.Buffer
	aw := NewAuditWriter(&buf)

	event := AuditEvent{
		Timestamp:   "2025-07-15T10:00:00Z",
		ToolName:    "workspace_exec",
		Decision:    DecisionDeny,
		RiskLevel:   RiskLevelCritical,
		RuleID:      "R-DEL-001",
		DurationMS:  5,
		Redacted:    false,
		Intercepted: true,
		Backend:     "workspaceexec",
	}

	err := aw.WriteEvent(event)
	require.NoError(t, err)

	// Verify the output is valid JSONL.
	data := buf.Bytes()
	assert.True(t, bytes.HasSuffix(data, []byte("\n")), "should end with newline")

	var parsed AuditEvent
	require.NoError(t, json.Unmarshal(data[:len(data)-1], &parsed))
	assert.Equal(t, DecisionDeny, parsed.Decision)
	assert.Equal(t, "R-DEL-001", parsed.RuleID)
}

// TestAuditWriter_WriteMultipleEvents verifies writing multiple events.
func TestAuditWriter_WriteMultipleEvents(t *testing.T) {
	var buf bytes.Buffer
	aw := NewAuditWriter(&buf)

	events := []AuditEvent{
		{ToolName: "tool1", Decision: DecisionDeny, RiskLevel: RiskLevelHigh},
		{ToolName: "tool2", Decision: DecisionAllow, RiskLevel: RiskLevelInfo},
	}

	for _, e := range events {
		require.NoError(t, aw.WriteEvent(e))
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Len(t, lines, 2)
}

// TestAuditEventFromScanResult verifies creating an AuditEvent from a ScanResult.
func TestAuditEventFromScanResult(t *testing.T) {
	result := ScanResult{
		Decision:    DecisionDeny,
		RiskLevel:   RiskLevelCritical,
		Findings:    []Finding{{RuleID: "R-DEL-001"}},
		ToolName:    "workspace_exec",
		Intercepted: true,
		Backend:     "workspaceexec",
	}

	event := auditEventFromScanResult(result, 5*time.Second, true)

	assert.Equal(t, DecisionDeny, event.Decision)
	assert.Equal(t, RiskLevelCritical, event.RiskLevel)
	assert.Equal(t, "R-DEL-001", event.RuleID)
	assert.Equal(t, int64(5000), event.DurationMS)
	assert.True(t, event.Redacted)
	assert.True(t, event.Intercepted)
	assert.Equal(t, "workspaceexec", event.Backend)
}

// TestAuditEventFromScanResult_NoFindings verifies creating an AuditEvent with no findings.
func TestAuditEventFromScanResult_NoFindings(t *testing.T) {
	result := ScanResult{
		Decision:    DecisionAllow,
		RiskLevel:   RiskLevelInfo,
		Findings:    nil,
		ToolName:    "workspace_exec",
		Intercepted: false,
		Backend:     "workspaceexec",
	}

	event := auditEventFromScanResult(result, 100*time.Millisecond, false)

	assert.Equal(t, DecisionAllow, event.Decision)
	assert.Equal(t, "", event.RuleID, "rule ID should be empty with no findings")
	assert.False(t, event.Redacted)
}

// TestDecisionOrder verifies the decision priority ordering.
func TestDecisionOrder(t *testing.T) {
	assert.Less(t, decisionOrder(DecisionDeny), decisionOrder(DecisionAsk))
	assert.Less(t, decisionOrder(DecisionAsk), decisionOrder(DecisionNeedsHumanReview))
	assert.Less(t, decisionOrder(DecisionNeedsHumanReview), decisionOrder(DecisionAllow))
}

// TestRiskLevelOrder verifies the risk level ordering.
func TestRiskLevelOrder(t *testing.T) {
	assert.Less(t, riskLevelOrder(RiskLevelInfo), riskLevelOrder(RiskLevelLow))
	assert.Less(t, riskLevelOrder(RiskLevelLow), riskLevelOrder(RiskLevelMedium))
	assert.Less(t, riskLevelOrder(RiskLevelMedium), riskLevelOrder(RiskLevelHigh))
	assert.Less(t, riskLevelOrder(RiskLevelHigh), riskLevelOrder(RiskLevelCritical))
}

// TestAggregateDecision verifies the aggregateDecision helper.
func TestAggregateDecision(t *testing.T) {
	tests := []struct {
		name     string
		findings []Finding
		expected Decision
	}{
		{
			name:     "no findings",
			findings: nil,
			expected: DecisionAllow,
		},
		{
			name:     "all allow",
			findings: []Finding{{Decision: DecisionAllow}, {Decision: DecisionAllow}},
			expected: DecisionAllow,
		},
		{
			name:     "deny wins",
			findings: []Finding{{Decision: DecisionAllow}, {Decision: DecisionDeny}},
			expected: DecisionDeny,
		},
		{
			name:     "ask over allow",
			findings: []Finding{{Decision: DecisionAllow}, {Decision: DecisionAsk}},
			expected: DecisionAsk,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := aggregateDecision(tt.findings)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestAggregateRiskLevel verifies the aggregateRiskLevel helper.
func TestAggregateRiskLevel(t *testing.T) {
	tests := []struct {
		name     string
		findings []Finding
		expected RiskLevel
	}{
		{
			name:     "no findings",
			findings: nil,
			expected: RiskLevelInfo,
		},
		{
			name:     "highest wins",
			findings: []Finding{{RiskLevel: RiskLevelLow}, {RiskLevel: RiskLevelCritical}},
			expected: RiskLevelCritical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := aggregateRiskLevel(tt.findings)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestDecisionFromTool verifies the decisionFromTool conversion.
func TestDecisionFromTool(t *testing.T) {
	tests := []struct {
		name           string
		decision       Decision
		expectedAction string
	}{
		{"allow", DecisionAllow, "allow"},
		{"deny", DecisionDeny, "deny"},
		{"ask", DecisionAsk, "ask"},
		{"needs_human_review", DecisionNeedsHumanReview, "ask"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perm := decisionFromTool(tt.decision)
			assert.Equal(t, tt.expectedAction, string(perm.Action))
		})
	}
}
