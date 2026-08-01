//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

// Rule is an optional extension point for site-specific checks.
// Built-in scanning stays fail-closed; extra rules can only tighten
// (deny/ask) decisions, never loosen an earlier deny.
type Rule interface {
	// ID returns a stable rule id for reports and audit events.
	ID() string
	// Check inspects the extracted payload. ok=false means no finding.
	Check(ex Extracted, policy Policy) (Finding, bool)
}

// RuleFunc adapts a function to Rule.
type RuleFunc func(ex Extracted, policy Policy) (Finding, bool)

// ID implements Rule.
func (RuleFunc) ID() string { return "extra.rule" }

// Check implements Rule.
func (f RuleFunc) Check(ex Extracted, policy Policy) (Finding, bool) {
	if f == nil {
		return Finding{}, false
	}
	return f(ex, policy)
}

type namedRule struct {
	id string
	fn RuleFunc
}

func (r namedRule) ID() string { return r.id }

func (r namedRule) Check(ex Extracted, policy Policy) (Finding, bool) {
	f, ok := r.fn(ex, policy)
	if ok && f.RuleID == "" {
		f.RuleID = r.id
	}
	return f, ok
}

// NamedRule wraps fn with an explicit rule id.
func NamedRule(id string, fn RuleFunc) Rule {
	return namedRule{id: id, fn: fn}
}
