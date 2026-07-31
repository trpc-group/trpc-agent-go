//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package domain defines the review contracts shared by the example pipeline.
package domain

import (
	"fmt"
	"sort"
)

// Severity describes the review impact level.
type Severity string

const (
	// SeverityCritical marks a finding that can cause immediate compromise or data loss.
	SeverityCritical Severity = "critical"
	// SeverityHigh marks a finding that is likely to be exploitable or disruptive.
	SeverityHigh Severity = "high"
	// SeverityMedium marks a finding that should be fixed before merge.
	SeverityMedium Severity = "medium"
	// SeverityLow marks a lower-risk finding or maintainability issue.
	SeverityLow Severity = "low"
)

// Review categories emitted by the bundled rules.
const (
	CategorySecurity    = "security"
	CategorySecrets     = "secrets"
	CategoryConcurrency = "concurrency"
	CategoryResources   = "resources"
	CategoryErrors      = "errors"
	CategoryDatabase    = "database"
	CategoryTests       = "tests"
)

// Finding is one review issue or human-review candidate.
type Finding struct {
	Severity       Severity `json:"severity"`
	Category       string   `json:"category"`
	File           string   `json:"file"`
	Line           int      `json:"line"`
	Title          string   `json:"title"`
	Evidence       string   `json:"evidence"`
	Recommendation string   `json:"recommendation"`
	Confidence     float64  `json:"confidence"`
	Source         string   `json:"source"`
	RuleID         string   `json:"rule_id"`
}

// Validate checks the caller-visible finding contract.
func (f Finding) Validate() error {
	if f.Severity == "" || f.Category == "" || f.File == "" ||
		f.Title == "" || f.Evidence == "" || f.Recommendation == "" ||
		f.Source == "" || f.RuleID == "" {
		return fmt.Errorf("finding missing required field")
	}
	if f.Line < 0 {
		return fmt.Errorf("finding line must be zero or greater")
	}
	if f.Confidence < 0 || f.Confidence > 1 {
		return fmt.Errorf("finding confidence must be between 0 and 1")
	}
	return nil
}

// Bucket decides where a finding is routed.
type Bucket string

const (
	// BucketFinding routes high-confidence findings to the main report.
	BucketFinding Bucket = "finding"
	// BucketHumanReview routes uncertain findings to human review.
	BucketHumanReview Bucket = "human_review"
	// BucketSuppressed hides low-confidence candidates from user-facing findings.
	BucketSuppressed Bucket = "suppressed"
)

// BucketForConfidence returns the routing bucket for a confidence score.
func BucketForConfidence(confidence float64) Bucket {
	switch {
	case confidence >= 0.80:
		return BucketFinding
	case confidence >= 0.55:
		return BucketHumanReview
	default:
		return BucketSuppressed
	}
}

// Status is the persisted review task state.
type Status string

const (
	// StatusPending means the review has not started.
	StatusPending Status = "pending"
	// StatusRunning means parsing, rules, or sandbox checks are active.
	StatusRunning Status = "running"
	// StatusFinalizing means reports and audit rows are being written.
	StatusFinalizing Status = "finalizing"
	// StatusCompleted means all durable artifacts were written.
	StatusCompleted Status = "completed"
	// StatusNeedsHumanReview means the review could not produce a clean automated conclusion.
	StatusNeedsHumanReview Status = "needs_human_review"
	// StatusFailed means the review failed before a trustworthy report could be finalized.
	StatusFailed Status = "failed"
)

// CanTransition reports whether a task may move between states.
func CanTransition(from, to Status) bool {
	if from == to {
		return true
	}
	switch from {
	case StatusPending:
		return to == StatusRunning || to == StatusFailed
	case StatusRunning:
		return to == StatusFinalizing || to == StatusNeedsHumanReview || to == StatusFailed
	case StatusFinalizing:
		return to == StatusCompleted || to == StatusNeedsHumanReview || to == StatusFailed
	default:
		return false
	}
}

// SortFindings applies the report's stable finding order.
func SortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if rankSeverity(a.Severity) != rankSeverity(b.Severity) {
			return rankSeverity(a.Severity) < rankSeverity(b.Severity)
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		return a.RuleID < b.RuleID
	})
}

func rankSeverity(sev Severity) int {
	switch sev {
	case SeverityCritical:
		return 0
	case SeverityHigh:
		return 1
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 3
	default:
		return 4
	}
}
