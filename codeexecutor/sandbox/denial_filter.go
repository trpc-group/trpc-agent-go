//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"path/filepath"
	"strings"
)

// DenialFilterScope selects which diagnostic outputs a filter rule
// applies to.
type DenialFilterScope string

const (
	// DenialFilterDenials applies only to Diagnostics.Denials.
	DenialFilterDenials DenialFilterScope = "denials"
	// DenialFilterAll applies to all diagnostic outputs.
	DenialFilterAll DenialFilterScope = "all"
)

// DenialTargetMatcher matches denial targets using structured fields.
type DenialTargetMatcher struct {
	Exact  string
	Prefix string
	Suffix string
	Glob   string
}

// DenialIgnoreRule ignores matching sandbox denials from diagnostic
// output.
type DenialIgnoreRule struct {
	Scope DenialFilterScope
	// Command, when non-empty, must be a substring of RunProgramSpec.Cmd. It
	// intentionally does not match Args because arguments may contain secrets.
	Command     string
	Operations  []string
	Targets     []DenialTargetMatcher
	RawContains []string
}

// DenialFilter configures automatic and user-defined sandbox denial
// filtering for diagnostic output.
type DenialFilter struct {
	DisableAutomatic bool
	Ignore           []DenialIgnoreRule
}

func applySandboxDenialFilters(
	denials []Denial,
	cmd string,
	filter DenialFilter,
) []Denial {
	if len(denials) == 0 {
		return nil
	}
	out := make([]Denial, 0, len(denials))
	seen := map[string]bool{}
	for _, denial := range denials {
		if shouldFilterSandboxDenial(denial, cmd, filter, DenialFilterDenials) {
			continue
		}
		key := denial.Operation + "\x00" + denial.Target
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, denial)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneDenialFilter(filter DenialFilter) DenialFilter {
	if len(filter.Ignore) == 0 {
		return filter
	}
	clone := DenialFilter{
		DisableAutomatic: filter.DisableAutomatic,
		Ignore:           make([]DenialIgnoreRule, len(filter.Ignore)),
	}
	for i, rule := range filter.Ignore {
		clone.Ignore[i] = DenialIgnoreRule{
			Scope:       rule.Scope,
			Command:     rule.Command,
			Operations:  append([]string(nil), rule.Operations...),
			Targets:     append([]DenialTargetMatcher(nil), rule.Targets...),
			RawContains: append([]string(nil), rule.RawContains...),
		}
	}
	return clone
}

func shouldFilterSandboxDenial(
	denial Denial,
	cmd string,
	filter DenialFilter,
	scope DenialFilterScope,
) bool {
	if !filter.DisableAutomatic && macosSandboxDenialAutoNoise(denial) {
		return true
	}
	for _, rule := range filter.Ignore {
		if !sandboxDenialFilterScopeMatches(rule.Scope, scope) {
			continue
		}
		if rule.Command != "" && !strings.Contains(cmd, rule.Command) {
			continue
		}
		if len(rule.Operations) > 0 && !stringSliceContains(rule.Operations, denial.Operation) {
			continue
		}
		if len(rule.Targets) > 0 && !sandboxDenialTargetMatches(denial.Target, rule.Targets) {
			continue
		}
		if len(rule.RawContains) > 0 && !stringSliceContainsSubstring(rule.RawContains, denial.Raw) {
			continue
		}
		if rule.Command == "" && len(rule.Operations) == 0 &&
			len(rule.Targets) == 0 && len(rule.RawContains) == 0 {
			continue
		}
		return true
	}
	return false
}

func sandboxDenialFilterScopeMatches(ruleScope, want DenialFilterScope) bool {
	switch ruleScope {
	case "", DenialFilterAll:
		return true
	case DenialFilterDenials:
		return want == DenialFilterDenials || want == DenialFilterAll
	default:
		return false
	}
}

func sandboxDenialTargetMatches(target string, matchers []DenialTargetMatcher) bool {
	for _, matcher := range matchers {
		if matcher.Exact != "" && target == matcher.Exact {
			return true
		}
		if matcher.Prefix != "" && strings.HasPrefix(target, matcher.Prefix) {
			return true
		}
		if matcher.Suffix != "" && strings.HasSuffix(target, matcher.Suffix) {
			return true
		}
		if matcher.Glob != "" {
			ok, err := filepath.Match(matcher.Glob, target)
			if err == nil && ok {
				return true
			}
		}
	}
	return false
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func stringSliceContainsSubstring(values []string, raw string) bool {
	for _, value := range values {
		if strings.Contains(raw, value) {
			return true
		}
	}
	return false
}
