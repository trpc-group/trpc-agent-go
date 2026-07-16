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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScanner_NewScanner verifies that NewScanner creates a scanner with default rules.
func TestScanner_NewScanner(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	require.NotNil(t, scanner)
	assert.Equal(t, policy, scanner.policy)
	assert.NotEmpty(t, scanner.rules, "scanner should have default rules")
}

// TestScanner_Scan_NoFindings verifies that a safe input produces no findings.
func TestScanner_Scan_NoFindings(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultAction = DecisionAllow
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command: "go test ./...",
	})

	assert.Equal(t, DecisionAllow, result.Decision)
	assert.Equal(t, RiskLevelInfo, result.RiskLevel)
	assert.Empty(t, result.Findings)
	assert.False(t, result.Intercepted)
}

func TestScanner_Scan_NoFindings_DefaultDeny(t *testing.T) {
	policy := DefaultPolicy()
	// DefaultPolicy has DefaultAction=deny, so no findings → deny.
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command: "go test ./...",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	assert.Equal(t, RiskLevelInfo, result.RiskLevel)
	assert.Empty(t, result.Findings)
	assert.True(t, result.Intercepted)
}

// TestScanner_Scan_MultipleFindings_Aggregation verifies that the deny decision
// takes precedence over ask and allow when multiple findings exist.
func TestScanner_Scan_MultipleFindings_Aggregation(t *testing.T) {
	// Use custom rules that produce different decisions.
	rules := []Rule{
		&stubRule{id: "R-STUB-ASK", decision: DecisionAsk, riskLevel: RiskLevelMedium},
		&stubRule{id: "R-STUB-DENY", decision: DecisionDeny, riskLevel: RiskLevelCritical},
		&stubRule{id: "R-STUB-ALLOW", decision: DecisionAllow, riskLevel: RiskLevelInfo},
	}

	policy := DefaultPolicy()
	scanner := NewScannerWithRules(policy, rules)

	result := scanner.Scan(context.Background(), ScanInput{
		Command: "test_command",
	})

	// Deny should win over ask and allow.
	assert.Equal(t, DecisionDeny, result.Decision)
	assert.Equal(t, RiskLevelCritical, result.RiskLevel)
	assert.Len(t, result.Findings, 3)
	assert.True(t, result.Intercepted)
}

// TestScanner_Scan_RiskLevelAggregation verifies that the highest risk level
// is used in the aggregated result.
func TestScanner_Scan_RiskLevelAggregation(t *testing.T) {
	rules := []Rule{
		&stubRule{id: "R-LOW", decision: DecisionAsk, riskLevel: RiskLevelLow},
		&stubRule{id: "R-HIGH", decision: DecisionAsk, riskLevel: RiskLevelHigh},
		&stubRule{id: "R-MED", decision: DecisionAsk, riskLevel: RiskLevelMedium},
	}

	policy := DefaultPolicy()
	scanner := NewScannerWithRules(policy, rules)

	result := scanner.Scan(context.Background(), ScanInput{
		Command: "test_command",
	})

	assert.Equal(t, DecisionAsk, result.Decision)
	assert.Equal(t, RiskLevelHigh, result.RiskLevel, "highest risk level should be used")
}

// TestScanner_NormalizedScanText verifies that normalizedScanText concatenates
// command and code blocks.
func TestScanner_NormalizedScanText(t *testing.T) {
	tests := []struct {
		name     string
		input    ScanInput
		expected string
	}{
		{
			name:     "empty input",
			input:    ScanInput{},
			expected: "",
		},
		{
			name:     "command only",
			input:    ScanInput{Command: "echo hello"},
			expected: "echo hello",
		},
		{
			name:     "code blocks only",
			input:    ScanInput{CodeBlocks: []string{"print('a')", "print('b')"}},
			expected: "print('a')\nprint('b')",
		},
		{
			name:     "command and code blocks",
			input:    ScanInput{Command: "go run .", CodeBlocks: []string{"func main() {}"}},
			expected: "go run .\nfunc main() {}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizedScanText(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestScanner_Scan_AskOverAllow verifies that ask takes precedence over allow.
func TestScanner_Scan_AskOverAllow(t *testing.T) {
	rules := []Rule{
		&stubRule{id: "R-ALLOW", decision: DecisionAllow, riskLevel: RiskLevelInfo},
		&stubRule{id: "R-ASK", decision: DecisionAsk, riskLevel: RiskLevelMedium},
	}

	policy := DefaultPolicy()
	scanner := NewScannerWithRules(policy, rules)

	result := scanner.Scan(context.Background(), ScanInput{
		Command: "test_command",
	})

	assert.Equal(t, DecisionAsk, result.Decision)
	assert.True(t, result.Intercepted)
}

// TestScanner_Scan_NeedsHumanReview verifies that needs_human_review is between ask and allow.
func TestScanner_Scan_NeedsHumanReview(t *testing.T) {
	rules := []Rule{
		&stubRule{id: "R-REVIEW", decision: DecisionNeedsHumanReview, riskLevel: RiskLevelMedium},
	}

	policy := DefaultPolicy()
	scanner := NewScannerWithRules(policy, rules)

	result := scanner.Scan(context.Background(), ScanInput{
		Command: "test_command",
	})

	assert.Equal(t, DecisionNeedsHumanReview, result.Decision)
	assert.True(t, result.Intercepted)
}

// TestScanner_Scan_NeedsHumanReviewOverAllow verifies needs_human_review > allow.
func TestScanner_Scan_NeedsHumanReviewOverAllow(t *testing.T) {
	rules := []Rule{
		&stubRule{id: "R-ALLOW", decision: DecisionAllow, riskLevel: RiskLevelInfo},
		&stubRule{id: "R-REVIEW", decision: DecisionNeedsHumanReview, riskLevel: RiskLevelMedium},
	}

	policy := DefaultPolicy()
	scanner := NewScannerWithRules(policy, rules)

	result := scanner.Scan(context.Background(), ScanInput{
		Command: "test_command",
	})

	assert.Equal(t, DecisionNeedsHumanReview, result.Decision)
}

// TestScanner_Scan_DenyOverNeedsHumanReview verifies deny > needs_human_review.
func TestScanner_Scan_DenyOverNeedsHumanReview(t *testing.T) {
	rules := []Rule{
		&stubRule{id: "R-REVIEW", decision: DecisionNeedsHumanReview, riskLevel: RiskLevelMedium},
		&stubRule{id: "R-DENY", decision: DecisionDeny, riskLevel: RiskLevelCritical},
	}

	policy := DefaultPolicy()
	scanner := NewScannerWithRules(policy, rules)

	result := scanner.Scan(context.Background(), ScanInput{
		Command: "test_command",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
}

// TestScanner_Scan_SetsFields verifies that ScanResult fields are populated correctly.
func TestScanner_Scan_SetsFields(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "go test ./...",
		ToolName: "my_tool",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, "go test ./...", result.Command)
	assert.Equal(t, "my_tool", result.ToolName)
	assert.Equal(t, "workspaceexec", result.Backend)
}

// TestScanner_ScanWithCustomRules verifies NewScannerWithRules works correctly.
func TestScanner_ScanWithCustomRules(t *testing.T) {
	rule := &stubRule{id: "R-CUSTOM", decision: DecisionDeny, riskLevel: RiskLevelHigh}
	policy := DefaultPolicy()
	scanner := NewScannerWithRules(policy, []Rule{rule})

	result := scanner.Scan(context.Background(), ScanInput{
		Command: "anything",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	assert.Len(t, result.Findings, 1)
	assert.Equal(t, "R-CUSTOM", result.Findings[0].RuleID)
}

// stubRule is a test helper that implements the Rule interface with fixed output.
type stubRule struct {
	id        string
	decision  Decision
	riskLevel RiskLevel
}

func (r *stubRule) ID() string   { return r.id }
func (r *stubRule) Name() string { return "Stub Rule " + r.id }

func (r *stubRule) Scan(_ context.Context, _ ScanInput, _ PolicyFile) []Finding {
	return []Finding{
		{
			RuleID:    r.id,
			RuleName:  r.Name(),
			RiskLevel: r.riskLevel,
			Decision:  r.decision,
			Evidence:  "stub evidence",
		},
	}
}
