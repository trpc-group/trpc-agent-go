//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package runner defines the code review agent orchestration and rule interfaces.
package runner

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

// CRConfig holds configuration for the code review agent and its rules.
type CRConfig struct {
	EnabledRules      []string          `yaml:"enabled_rules"`
	DisabledRules     []string          `yaml:"disabled_rules"`
	SeverityOverrides map[string]string `yaml:"severity_overrides"`
}

// CRRule is a single code review rule that checks a changed file.
type CRRule interface {
	// ID returns the unique rule identifier.
	ID() string
	// Category returns the finding category this rule belongs to.
	Category() finding.Category
	// DefaultSeverity returns the default severity level for this rule.
	DefaultSeverity() finding.Severity
	// Check examines a changed file's content and returns findings.
	Check(ctx context.Context, file finding.ChangedFileInfo, content string) ([]finding.Finding, error)
	// IsEnabled checks whether this rule is enabled according to the config.
	IsEnabled(config *CRConfig) bool
}

// RuleRegistry manages a set of code review rules.
type RuleRegistry struct {
	rules map[string]CRRule
}

// NewRuleRegistry creates an empty rule registry.
func NewRuleRegistry() *RuleRegistry {
	return &RuleRegistry{rules: make(map[string]CRRule)}
}

// Register adds a rule to the registry. Returns error if rule ID already exists.
func (r *RuleRegistry) Register(rule CRRule) error {
	id := rule.ID()
	if _, exists := r.rules[id]; exists {
		return fmt.Errorf("rule %q already registered", id)
	}
	r.rules[id] = rule
	return nil
}

// Get returns a rule by ID.
func (r *RuleRegistry) Get(id string) (CRRule, error) {
	rule, exists := r.rules[id]
	if !exists {
		return nil, fmt.Errorf("rule %q not found", id)
	}
	return rule, nil
}

// EnabledRules returns all rules that are enabled according to the given config.
func (r *RuleRegistry) EnabledRules(config *CRConfig) []CRRule {
	var result []CRRule
	for _, rule := range r.rules {
		if rule.IsEnabled(config) {
			result = append(result, rule)
		}
	}
	return result
}

// AllRules returns all registered rules.
func (r *RuleRegistry) AllRules() []CRRule {
	result := make([]CRRule, 0, len(r.rules))
	for _, rule := range r.rules {
		result = append(result, rule)
	}
	return result
}

// IsEnabled returns true if the rule is enabled according to the given config.
// A rule is enabled if it is in EnabledRules (or EnabledRules is empty meaning all enabled)
// and not in DisabledRules.
func IsEnabled(ruleID string, config *CRConfig) bool {
	// If disabled list explicitly contains this rule, it's disabled.
	if config != nil {
		for _, d := range config.DisabledRules {
			if d == ruleID {
				return false
			}
		}
		// If enabled list is non-empty, rule must be in it.
		if len(config.EnabledRules) > 0 {
			for _, e := range config.EnabledRules {
				if e == ruleID {
					return true
				}
			}
			return false
		}
	}
	return true
}

// EffectiveSeverity returns the configured severity if overridden, otherwise the default.
func EffectiveSeverity(ruleID string, defaultSev finding.Severity, config *CRConfig) finding.Severity {
	if config != nil {
		if override, ok := config.SeverityOverrides[ruleID]; ok {
			return finding.Severity(override)
		}
	}
	return defaultSev
}

// RuleBase provides common fields for rule implementations.
type RuleBase struct {
	IDValue       string
	CategoryValue finding.Category
	DefaultSev    finding.Severity
}

// ID returns the rule identifier.
func (b *RuleBase) ID() string { return b.IDValue }

// Category returns the finding category.
func (b *RuleBase) Category() finding.Category { return b.CategoryValue }

// DefaultSeverity returns the default severity.
func (b *RuleBase) DefaultSeverity() finding.Severity { return b.DefaultSev }

// IsEnabled checks if the rule is enabled in the given config.
func (b *RuleBase) IsEnabled(config *CRConfig) bool {
	return IsEnabled(b.IDValue, config)
}

// NewFinding creates a finding from a rule match for convenience.
func NewFinding(base *RuleBase, file string, line int, title, evidence, recommendation string, confidence finding.Confidence) finding.Finding {
	return finding.Finding{
		Severity:       base.DefaultSev,
		Category:       base.CategoryValue,
		File:           file,
		Line:           line,
		Title:          title,
		Evidence:       strings.TrimSpace(evidence),
		Recommendation: recommendation,
		Confidence:     confidence,
		Source:         finding.SourceCustomRule,
		RuleID:         base.IDValue,
	}
}

// NewFindingWithSeverity creates a finding with a specific severity (useful when config provides overrides).
func NewFindingWithSeverity(sev finding.Severity, base *RuleBase, file string, line int, title, evidence, recommendation string, confidence finding.Confidence) finding.Finding {
	f := NewFinding(base, file, line, title, evidence, recommendation, confidence)
	f.Severity = sev
	return f
}
