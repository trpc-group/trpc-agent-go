//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package analysis provides normalization and deduplication for findings.
package analysis

import "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"

// Normalize applies deduplication and buckets low-confidence findings
// into the warning severity.
func Normalize(findings []reviewmodel.Finding) []reviewmodel.Finding {
	findings = Deduplicate(findings)
	for i := range findings {
		f := &findings[i]
		// Low confidence findings go to warning severity and need human review.
		if f.Confidence < 0.5 && f.Severity != reviewmodel.SeverityWarning {
			f.Severity = reviewmodel.SeverityWarning
			f.NeedsHumanReview = true
		}
		// Medium-low confidence also needs human review flag.
		if f.Confidence >= 0.5 && f.Confidence < 0.65 {
			f.NeedsHumanReview = true
		}
	}
	return findings
}

// SeverityCounts returns a map of severity -> count.
func SeverityCounts(findings []reviewmodel.Finding) map[string]int {
	counts := make(map[string]int)
	for _, f := range findings {
		counts[f.Severity]++
	}
	return counts
}

// CategoryCounts returns a map of category -> count.
func CategoryCounts(findings []reviewmodel.Finding) map[string]int {
	counts := make(map[string]int)
	for _, f := range findings {
		counts[f.Category]++
	}
	return counts
}

// HumanReviewItems returns titles of findings that need human review.
func HumanReviewItems(findings []reviewmodel.Finding) []string {
	var items []string
	for _, f := range findings {
		if f.NeedsHumanReview {
			items = append(items, f.Title)
		}
	}
	return items
}
