//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides a Tool execution safety scanner.
//
// It checks incoming tool execution requests (shell commands, code blocks)
// against a set of configurable rules and returns an allow/deny/ask decision
// with a structured report.
//
// This package is the implementation target for Issue #2002.
package safety

import (
	"strings"
)

// RiskLevel describes how dangerous a scanned input is.
type RiskLevel string

// Risk severity levels.
const (
	// RiskNone indicates no risk.
	RiskNone RiskLevel = "none"
	// RiskLow indicates low risk.
	RiskLow RiskLevel = "low"
	// RiskMedium indicates medium risk.
	RiskMedium RiskLevel = "medium"
	// RiskHigh indicates high risk.
	RiskHigh RiskLevel = "high"
	// RiskCritical indicates critical risk.
	RiskCritical RiskLevel = "critical"
)

// Decision is the final safety judgement.
type Decision string

// Safety decision values.
const (
	// DecisionAllow permits the tool call to execute.
	DecisionAllow Decision = "allow"
	// DecisionDeny blocks the tool call from executing.
	DecisionDeny Decision = "deny"
	// DecisionAsk requires human approval before execution.
	DecisionAsk Decision = "ask"
)

// ScanResult is the structured output of a single rule check.
type ScanResult struct {
	Decision  Decision  `json:"decision"`
	RiskLevel RiskLevel `json:"risk_level"`
	RuleID    string    `json:"rule_id"`
	Evidence  string    `json:"evidence"`
	Reason    string    `json:"reason"`
}

// ScanInput is what the Scanner inspects before execution.
type ScanInput struct {
	// Command is used by hostexec / workspaceexec.
	Command string

	// CodeBlocks is used by codeexec.
	CodeBlocks []CodeBlock

	// ExecutorType tells the Scanner which backend will run the code.
	// "local" means highest risk.
	ExecutorType string
}

// CodeBlock represents one code snippet from a codeexec call.
type CodeBlock struct {
	Language string
	Code     string
}

// Rule is a single safety check.
// Each rule inspects the input and returns nil if safe,
// or a ScanResult if a risk is detected.
type Rule interface {
	// ID returns a short unique identifier, e.g. "danger_cmd_001".
	ID() string
	// Check inspects the input and returns a ScanResult if the rule fires.
	Check(input ScanInput) *ScanResult
}

// Scanner runs a collection of rules against a ScanInput.
type Scanner struct {
	rules []Rule
}

// NewScanner creates a Scanner with the given rules.
func NewScanner(rules ...Rule) *Scanner {
	return &Scanner{rules: rules}
}

// Scan runs every rule and returns the most severe result.
// Precedence: deny > ask > allow (first deny wins).
func (s *Scanner) Scan(input ScanInput) *ScanResult {
	var worst *ScanResult
	for _, r := range s.rules {
		res := r.Check(input)
		if res == nil {
			continue
		}
		// If any rule says deny, stop immediately.
		if res.Decision == DecisionDeny {
			return res
		}
		// Track the "worst" non-deny result.
		if worst == nil || severity(res.RiskLevel) > severity(worst.RiskLevel) {
			worst = res
		}
	}
	if worst != nil {
		return worst
	}
	return &ScanResult{
		Decision:  DecisionAllow,
		RiskLevel: RiskNone,
		Reason:    "no safety rules triggered",
	}
}

func severity(r RiskLevel) int {
	switch r {
	case RiskNone:
		return 0
	case RiskLow:
		return 1
	case RiskMedium:
		return 2
	case RiskHigh:
		return 3
	case RiskCritical:
		return 4
	default:
		return 0
	}
}

// policyConfig mirrors a YAML/JSON policy file.
type policyConfig struct {
	DeniedCommands    []string `yaml:"denied_commands"    json:"denied_commands"`
	DeniedPaths       []string `yaml:"denied_paths"       json:"denied_paths"`
	DeniedDomains     []string `yaml:"denied_domains"     json:"denied_domains"`
	AllowedDomains    []string `yaml:"allowed_domains"    json:"allowed_domains"`
	MaxTimeoutSeconds int      `yaml:"max_timeout_seconds" json:"max_timeout_seconds"`
	MaxOutputBytes    int      `yaml:"max_output_bytes"    json:"max_output_bytes"`
}

// containsSubstring checks whether any pattern in patterns appears in s.
// Matching is case-insensitive (most safety checks should be conservative).
func containsSubstring(s string, patterns []string) bool {
	lower := strings.ToLower(s)
	for _, p := range patterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}


