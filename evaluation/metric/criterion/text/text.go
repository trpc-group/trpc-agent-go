//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package text defines text comparison criteria.
package text

import (
	"fmt"
	"regexp"
	"strings"
)

// TextCriterion governs how two strings should be compared.
type TextCriterion struct {
	// Ignore skips comparison when true.
	Ignore bool `json:"ignore,omitempty"`
	// CaseInsensitive toggles lowercase comparison.
	CaseInsensitive bool `json:"caseInsensitive,omitempty"`
	// MatchStrategy selects the comparison rule.
	MatchStrategy TextMatchStrategy `json:"matchStrategy,omitempty"`
	// Compare overrides built-in strategies.
	Compare func(actual, expected string) (bool, error) `json:"-"`
}

// TextMatchStrategy enumerates supported text comparison strategies.
type TextMatchStrategy string

const (
	// TextMatchStrategyExact matches strings exactly.
	TextMatchStrategyExact TextMatchStrategy = "exact"
	// TextMatchStrategyContains matches strings that contain the target.
	TextMatchStrategyContains TextMatchStrategy = "contains"
	// TextMatchStrategyRegex matches strings that match the regex.
	TextMatchStrategyRegex TextMatchStrategy = "regex"
)

// Match compares source and target using the configured strategy.
func (t *TextCriterion) Match(source, target string) (bool, error) {
	if t.Ignore {
		return true, nil
	}
	if t.Compare != nil {
		return t.Compare(source, target)
	}
	if t.CaseInsensitive {
		source = strings.ToLower(source)
		target = strings.ToLower(target)
	}
	switch t.MatchStrategy {
	// Default to exact match.
	case TextMatchStrategyExact, "":
		if source == target {
			return true, nil
		}
		return false, fmt.Errorf("source %s and target %s do not match", source, target)
	case TextMatchStrategyContains:
		if strings.Contains(source, target) {
			return true, nil
		}
		return false, fmt.Errorf("source %s does not contain target %s", source, target)
	case TextMatchStrategyRegex:
		re, err := regexp.Compile(target)
		if err != nil {
			return false, fmt.Errorf("invalid regex %s: %w", target, err)
		}
		if re.MatchString(source) {
			return true, nil
		}
		return false, fmt.Errorf("source %s does not match regex %s", source, target)
	default:
		return false, fmt.Errorf("invalid match strategy %s", t.MatchStrategy)
	}
}
