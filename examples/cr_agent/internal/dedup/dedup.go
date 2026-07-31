//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package dedup

import (
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/types"
)

// dedupeKey uniquely identifies a finding so that duplicate detections
// from different rules or rule passes can be collapsed.
type dedupeKey struct {
	file     string
	line     int
	category types.Category
	ruleID   string
}

// Apply removes duplicate findings and demotes low-confidence ones
// to warnings.
//
// A duplicate is defined as two findings with the same file, line,
// category, and rule_id. The first occurrence is kept; subsequent
// matches are dropped.
//
// Findings with confidence below the confidence threshold are moved
// into the warnings slice and flagged NeedsHumanReview, rather than
// being reported as definitive findings.
func Apply(findings []types.Finding, confidenceThreshold float64) (
	deduped []types.Finding, warnings []string,
) {
	seen := make(map[dedupeKey]bool)
	for _, f := range findings {
		key := dedupeKey{
			file:     f.File,
			line:     f.Line,
			category: f.Category,
			ruleID:   f.RuleID,
		}
		if seen[key] {
			continue
		}
		seen[key] = true

		if f.Confidence < confidenceThreshold {
			f.NeedsHumanReview = true
			warnings = append(warnings, formatWarning(f))
		}
		deduped = append(deduped, f)
	}
	return deduped, warnings
}

func formatWarning(f types.Finding) string {
	return "Low-confidence finding (confidence=" +
		formatFloat(f.Confidence) + "): " + f.Title +
		" at " + f.File + ":" + itoa(f.Line) + " [" + f.RuleID + "]"
}

// formatFloat formats a float64 as a simple decimal without importing
// strconv (to keep the dedup package dependency-free).
func formatFloat(f float64) string {
	whole := int(f)
	frac := int((f - float64(whole)) * 100)
	return itoa(whole) + "." + padTwo(frac)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func padTwo(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}
