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
	// RuleID identifies which rule produced this result (e.g. "danger_cmd_001").
	RuleID string `json:"rule_id"`
	// Evidence is the exact keyword or pattern that triggered the rule.
	Evidence string `json:"evidence"`
	// Reason is a human-readable explanation suitable for the model or operator.
	Reason string `json:"reason"`
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
	// Language is the source language identifier, e.g. "python", "javascript".
	Language string
	// Code is the raw source text that will be scanned for safety issues.
	Code string
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
//
// Precedence (highest priority first):
//   - DecisionDeny > DecisionAsk > DecisionAllow
//   - Within the same decision, the highest RiskLevel wins.
//
// All rules are evaluated; the most severe result is returned so that
// the reported rule/risk does not depend on registration order.
func (s *Scanner) Scan(input ScanInput) *ScanResult {
	var worst *ScanResult
	for _, r := range s.rules {
		res := r.Check(input)
		if res == nil {
			continue
		}
		if isMoreSevere(res, worst) {
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

// severity returns a numeric severity score for r. Higher numbers
// correspond to more dangerous risk levels; unknown values collapse to
// RiskNone so future additions are fail-safe.
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

// decisionPriority returns the priority of a decision: deny > ask > allow.
// Unknown decisions get the lowest priority so a typo in a future Decision
// constant cannot accidentally out-rank a real result.
func decisionPriority(d Decision) int {
	switch d {
	case DecisionDeny:
		return 3
	case DecisionAsk:
		return 2
	case DecisionAllow:
		return 1
	default:
		return 0
	}
}

// isMoreSevere reports whether candidate is more severe than current.
// A result is more severe if its decision has higher priority, or if
// the decision is the same and its risk level is higher.
//
// When current is nil every candidate is considered more severe so the
// first firing rule is recorded. From then on the strict comparison
// ensures the final ScanResult is the single most-severe entry across
// all rules, regardless of rule registration order.
func isMoreSevere(candidate, current *ScanResult) bool {
	if current == nil {
		return true
	}
	if decisionPriority(candidate.Decision) != decisionPriority(current.Decision) {
		return decisionPriority(candidate.Decision) > decisionPriority(current.Decision)
	}
	return severity(candidate.RiskLevel) > severity(current.RiskLevel)
}

// policyConfig mirrors a YAML/JSON policy file.
//
// It is package-private because callers should not construct a Scanner
// directly from a config struct - use NewScanner with the rule helpers
// (NewDangerousCommandRule, ...) instead. The struct is kept here so
// Scan tests and future YAML loaders can share one shape.
type policyConfig struct {
	// DeniedCommands is the deny list of command keywords.
	DeniedCommands []string `yaml:"denied_commands"    json:"denied_commands"`
	// DeniedPaths is the deny list of sensitive path patterns.
	DeniedPaths []string `yaml:"denied_paths"       json:"denied_paths"`
	// DeniedDomains is the network domain deny list.
	DeniedDomains []string `yaml:"denied_domains"     json:"denied_domains"`
	// AllowedDomains is the network domain allow list.
	AllowedDomains []string `yaml:"allowed_domains"    json:"allowed_domains"`
	// MaxTimeoutSeconds is the maximum command execution timeout in seconds.
	MaxTimeoutSeconds int `yaml:"max_timeout_seconds" json:"max_timeout_seconds"`
	// MaxOutputBytes is the maximum output size in bytes.
	MaxOutputBytes int `yaml:"max_output_bytes"    json:"max_output_bytes"`
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
