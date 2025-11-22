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

// TextCriterion governs how two strings should be compared.
type TextCriterion struct {
	// Ignore skips comparison when true.
	Ignore bool `json:"ignore,omitempty"`
	// CaseInsensitive toggles lowercase comparison.
	CaseInsensitive bool `json:"caseInsensitive,omitempty"`
	// MatchStrategy selects the comparison rule.
	MatchStrategy TextMatchStrategy `json:"matchStrategy,omitempty"`
	// Compare overrides built-in strategies.
	Compare func(actual, expected string) error `json:"-"`
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
