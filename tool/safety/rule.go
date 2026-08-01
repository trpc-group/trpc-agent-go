//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "strings"

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
	if r.fn == nil {
		return Finding{}, false
	}
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

func nameSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" {
			out[n] = struct{}{}
		}
	}
	return out
}

// DenyToolNames denies tool calls whose name matches (case-insensitive).
// Use with WithExtraRules when an org must block a whole tool without editing YAML.
func DenyToolNames(names ...string) Rule {
	set := nameSet(names)
	return NamedRule("extra.deny_tool_name", func(ex Extracted, _ Policy) (Finding, bool) {
		if _, ok := set[strings.ToLower(strings.TrimSpace(ex.ToolName))]; !ok {
			return Finding{}, false
		}
		return Finding{
			RuleID:   "extra.deny_tool_name",
			Decision: DecisionDeny,
			Risk:     RiskHigh,
			Evidence: "tool " + ex.ToolName + " is denied by site rule DenyToolNames",
			Advice:   "remove the tool from the agent or drop it from DenyToolNames",
		}, true
	})
}

// AskToolNames forces ask for matching tool names when built-ins would allow.
func AskToolNames(names ...string) Rule {
	set := nameSet(names)
	return NamedRule("extra.ask_tool_name", func(ex Extracted, _ Policy) (Finding, bool) {
		if _, ok := set[strings.ToLower(strings.TrimSpace(ex.ToolName))]; !ok {
			return Finding{}, false
		}
		return Finding{
			RuleID:   "extra.ask_tool_name",
			Decision: DecisionAsk,
			Risk:     RiskMedium,
			Evidence: "tool " + ex.ToolName + " requires approval by site rule AskToolNames",
			Advice:   "approve only after reviewing arguments, or remove from AskToolNames",
		}, true
	})
}

// DenyCommandSubstrings denies when the folded command text contains any needle
// (case-insensitive). Prefer DeniedCommands for basename matches; use this for
// org-specific argument patterns (e.g. "terraform apply").
func DenyCommandSubstrings(needles ...string) Rule {
	cleaned := make([]string, 0, len(needles))
	for _, n := range needles {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" {
			cleaned = append(cleaned, n)
		}
	}
	return NamedRule("extra.deny_command_substring", func(ex Extracted, _ Policy) (Finding, bool) {
		hay := strings.ToLower(ex.Command + "\n" + ex.RawText)
		for _, n := range cleaned {
			if strings.Contains(hay, n) {
				return Finding{
					RuleID:   "extra.deny_command_substring",
					Decision: DecisionDeny,
					Risk:     RiskHigh,
					Evidence: "payload matches denied substring " + n,
					Advice:   "rewrite the command or remove the substring from DenyCommandSubstrings",
				}, true
			}
		}
		return Finding{}, false
	})
}
