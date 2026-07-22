//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package reviewmodel defines stable review result types.
package reviewmodel

// Bucket separates actionable findings from lower-confidence candidates.
type Bucket string

const (
	// BucketFindings contains high-confidence actionable findings.
	BucketFindings Bucket = "findings"
	// BucketWarnings contains lower-confidence warnings.
	BucketWarnings Bucket = "warnings"
	// BucketHumanReview contains candidates requiring manual review.
	BucketHumanReview Bucket = "needs_human_review"
)

// Finding is one structured code review result.
type Finding struct {
	Bucket         Bucket  `json:"bucket"`
	Severity       string  `json:"severity"`
	Category       string  `json:"category"`
	File           string  `json:"file"`
	Line           int     `json:"line"`
	Title          string  `json:"title"`
	Evidence       string  `json:"evidence"`
	Recommendation string  `json:"recommendation"`
	Confidence     float64 `json:"confidence"`
	Source         string  `json:"source"`
	RuleID         string  `json:"rule_id"`
}
