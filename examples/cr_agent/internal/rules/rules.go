//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package rules implements the static-analysis rule engine for the CR
// agent. Each rule inspects a diff FileChange and emits zero or more
// Findings.
//
// Rules are intentionally lightweight pattern matchers — they do not
// perform full AST analysis. This keeps the example self-contained
// while still catching the most common and impactful bug classes in
// Go code. Each rule documents its detection pattern so reviewers can
// understand false-positive boundaries.
package rules

import (
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/types"
)

// Rule is the contract every static-analysis rule satisfies.
type Rule interface {
	// ID returns the stable, unique rule identifier (e.g.
	// "SEC-001").
	ID() string

	// Category returns the finding category this rule produces.
	Category() types.Category

	// Evaluate inspects a file change and returns findings.
	Evaluate(fc *diff.FileChange) []types.Finding
}

// Registry holds all registered rules indexed by ID.
type Registry struct {
	rules []Rule
	byID  map[string]Rule
}

// NewRegistry creates a Registry pre-loaded with all built-in rules.
func NewRegistry() *Registry {
	r := &Registry{byID: make(map[string]Rule)}
	for _, rule := range allRules() {
		r.rules = append(r.rules, rule)
		r.byID[rule.ID()] = rule
	}
	return r
}

// Rules returns all registered rules in registration order.
func (r *Registry) Rules() []Rule { return r.rules }

// Get returns the rule with the given ID, or nil.
func (r *Registry) Get(id string) Rule { return r.byID[id] }

// allRules returns the full set of built-in rules. The order is
// intentional: higher-impact rules run first so findings appear in
// severity-friendly order.
func allRules() []Rule {
	return []Rule{
		// Security
		&sqlInjectionRule{},
		&hardcodedSecretRule{},
		&commandInjectionRule{},

		// Goroutine / context
		&goroutineNoExitRule{},
		&uncanceledContextRule{},
		&blockedSendRule{},

		// Resource close
		&missingDeferCloseRule{},
		&unclosedHTTPBodyRule{},

		// Error handling
		&ignoredErrorRule{},
		&errorShadowingRule{},

		// Test missing
		&missingTestRule{},

		// Sensitive info
		&logCredentialsRule{},

		// DB lifecycle
		&transactionNotRolledBackRule{},
	}
}

// --- helper functions ---

// finding creates a Finding with common fields filled.
func finding(ruleID string, sev types.Severity, cat types.Category,
	fc *diff.FileChange, lineNum int, title, evidence, rec string,
) types.Finding {
	return types.Finding{
		Severity:       sev,
		Category:       cat,
		File:           fc.NewPath,
		Line:           lineNum,
		Title:          title,
		Evidence:       evidence,
		Recommendation: rec,
		Confidence:     0.8,
		Source:         "static-rule",
		RuleID:         ruleID,
	}
}

// lineInfo pairs a line's content with its 1-based line number in the
// new file.
type lineInfo struct {
	content string
	lineNum int
}

// addedLines returns the added lines of a FileChange along with their
// 1-based line numbers in the new file.
func addedLines(fc *diff.FileChange) []lineInfo {
	var result []lineInfo
	idx := 0
	for _, h := range fc.Hunks {
		nl := h.NewStart
		for _, line := range h.Lines {
			if strings.HasPrefix(line, "+") {
				var ln int
				if idx < len(fc.AddedLineNumbers) {
					ln = fc.AddedLineNumbers[idx]
				} else {
					ln = nl
				}
				result = append(result, lineInfo{
					content: line[1:],
					lineNum: ln,
				})
				idx++
				nl++
			} else if !strings.HasPrefix(line, "-") {
				nl++
			}
		}
	}
	return result
}

// allLines returns every line in the new file (context + added),
// excluding removed lines. This gives rules a view of the final state
// of the file after the diff is applied, so they can check whether
// safety patterns (defer Close, defer Rollback, etc.) are present.
func allLines(fc *diff.FileChange) []lineInfo {
	var result []lineInfo
	for _, h := range fc.Hunks {
		nl := h.NewStart
		for _, line := range h.Lines {
			if strings.HasPrefix(line, "-") {
				// Removed lines don't appear in the new file and don't
				// advance the new-file counter.
				continue
			} else if strings.HasPrefix(line, "+") {
				result = append(result, lineInfo{
					content: line[1:],
					lineNum: nl,
				})
				nl++
			} else {
				// Context line (leading space or empty).
				result = append(result, lineInfo{
					content: strings.TrimPrefix(line, " "),
					lineNum: nl,
				})
				nl++
			}
		}
	}
	return result
}

// compileRegexp is a small helper that panics on invalid patterns.
// All patterns in this package are compile-time constants, so a panic
// indicates a programming error.
func compileRegexp(pattern string) *regexp.Regexp {
	return regexp.MustCompile(pattern)
}
