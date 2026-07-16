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
	"strings"
)

// Rule is the interface for a safety check rule.
type Rule interface {
	// ID returns the unique rule identifier.
	ID() string
	// Name returns the human-readable rule name.
	Name() string
	// Scan evaluates the input and returns findings.
	Scan(ctx context.Context, input ScanInput, policy PolicyFile) []Finding
}

// Scanner evaluates a set of rules against a ScanInput.
type Scanner struct {
	rules  []Rule
	policy PolicyFile
}

// NewScanner creates a Scanner with the given policy and default rules.
func NewScanner(policy PolicyFile) *Scanner {
	return &Scanner{
		rules:  defaultRules(),
		policy: policy,
	}
}

// NewScannerWithRules creates a Scanner with the given policy and custom rules.
func NewScannerWithRules(policy PolicyFile, rules []Rule) *Scanner {
	return &Scanner{
		rules:  rules,
		policy: policy,
	}
}

// Scan evaluates all rules against the input and returns a ScanResult.
// Decision aggregation: deny > ask > needs_human_review > allow.
// Risk level aggregation: critical > high > medium > low > info.
// If no findings are produced, the decision is taken from
// policy.DefaultAction (fail-closed when set to DecisionDeny).
func (s *Scanner) Scan(ctx context.Context, input ScanInput) ScanResult {
	var allFindings []Finding
	for _, rule := range s.rules {
		findings := rule.Scan(ctx, input, s.policy)
		allFindings = append(allFindings, findings...)
	}

	result := ScanResult{
		ToolName: input.ToolName,
		Command:  input.Command,
		Backend:  input.Backend,
		Findings: allFindings,
	}

	if len(allFindings) == 0 {
		result.Decision = s.policy.DefaultAction
		result.RiskLevel = RiskLevelInfo
		result.Intercepted = result.Decision != DecisionAllow
		return result
	}

	result.Decision = aggregateDecision(allFindings)
	result.RiskLevel = aggregateRiskLevel(allFindings)
	result.Intercepted = result.Decision != DecisionAllow

	return result
}

// defaultRules returns all built-in rules in evaluation order.
func defaultRules() []Rule {
	return []Rule{
		&DangerousCommandRule{},
		&CredentialAccessRule{},
		&ShellBypassRule{},
		&HostLongSessionRule{},
		&DependencyInstallRule{},
		&ResourceAbuseRule{},
		&SecretLeakageRule{},
		&AllowListMissRule{},
		&EnvPolicyRule{},
		&NetworkEgressRule{},
		&AskForReviewRule{},
	}
}

// normalizedScanText concatenates the command and all code blocks into a
// single string for comprehensive pattern matching.
func normalizedScanText(input ScanInput) string {
	parts := make([]string, 0, 1+len(input.CodeBlocks))
	if input.Command != "" {
		parts = append(parts, input.Command)
	}
	parts = append(parts, input.CodeBlocks...)
	return strings.Join(parts, "\n")
}
