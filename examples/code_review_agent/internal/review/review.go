//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package review defines the canonical review-finding domain type and
// applies fingerprinting, deduplication, and confidence-based partitioning
// to the findings produced by the rules engine.
package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
)

// Finding is the canonical review finding. It enriches rules.Finding with
// a Fingerprint (for dedup and traceability) and a TaskID (the run-unique
// identifier of the review task that produced it).
type Finding struct {
	TaskID         string
	Severity       string // "critical"|"high"|"medium"|"low"
	Category       string
	File           string
	Line           int
	Title          string
	Evidence       string
	Recommendation string
	Confidence     float64
	Source         string // e.g. "rule:SI-001"
	RuleID         string
	Fingerprint    string // sha256(task_id + rule_id + file + line + category)
}

// Warning wraps a low-confidence finding together with the reason it was
// not promoted to a confirmed finding.
type Warning struct {
	Finding Finding
	Reason  string // e.g. "low confidence: 0.40"
}

// Report is the aggregated output of a review task.
type Report struct {
	TaskID           string
	Findings         []Finding // confidence >= 0.6
	Warnings         []Warning // confidence < 0.6, non-critical
	NeedsHumanReview []Finding // critical findings with low confidence
}

// confirmedThreshold is the minimum confidence for a finding to be treated
// as confirmed rather than a warning.
const confirmedThreshold = 0.6

// Fingerprint computes the sha256 hash of task_id + rule_id + file + line +
// category. It MUST be called BEFORE dedup checks.
func Fingerprint(taskID string, f rules.Finding) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%d|%s", taskID, f.RuleID, f.File, f.Line, f.Category)
	return hex.EncodeToString(h.Sum(nil))
}

// FromRules converts rules.Finding (engine output) to review.Finding,
// computing the fingerprint. taskID is the run-unique task id.
func FromRules(taskID string, ruleFindings []rules.Finding) []Finding {
	out := make([]Finding, 0, len(ruleFindings))
	for _, rf := range ruleFindings {
		out = append(out, Finding{
			TaskID:         taskID,
			Severity:       rf.Severity,
			Category:       rf.Category,
			File:           rf.File,
			Line:           rf.Line,
			Title:          rf.Title,
			Evidence:       rf.Evidence,
			Recommendation: rf.Recommendation,
			Confidence:     rf.Confidence,
			Source:         rf.Source,
			RuleID:         rf.RuleID,
			Fingerprint:    Fingerprint(taskID, rf),
		})
	}
	return out
}

// Dedup removes findings with the same file+line+rule_id, keeping the first.
// The fingerprint MUST already be computed on each finding.
func Dedup(findings []Finding) []Finding {
	seen := make(map[string]struct{}, len(findings))
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		key := fmt.Sprintf("%s|%d|%s", f.File, f.Line, f.RuleID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	return out
}

// Partition splits findings into confirmed (confidence >= 0.6) and warnings
// (confidence < 0.6). Critical-severity findings with low confidence go to
// needsHumanReview instead of warnings.
func Partition(findings []Finding) (confirmed []Finding, warnings []Warning, needsHumanReview []Finding) {
	for _, f := range findings {
		if f.Confidence >= confirmedThreshold {
			confirmed = append(confirmed, f)
			continue
		}
		if f.Severity == "critical" {
			needsHumanReview = append(needsHumanReview, f)
			continue
		}
		warnings = append(warnings, Warning{
			Finding: f,
			Reason:  fmt.Sprintf("low confidence: %.2f", f.Confidence),
		})
	}
	return confirmed, warnings, needsHumanReview
}

// Build aggregates rule findings into a Report, applying fingerprint, dedup,
// and partitioning.
func Build(taskID string, ruleFindings []rules.Finding) *Report {
	all := FromRules(taskID, ruleFindings)
	deduped := Dedup(all)
	confirmed, warnings, needsHuman := Partition(deduped)
	sortBySeverity(confirmed)
	return &Report{
		TaskID:           taskID,
		Findings:         confirmed,
		Warnings:         warnings,
		NeedsHumanReview: needsHuman,
	}
}

// severityRank maps a severity string to an ordering rank where lower ranks
// sort first: critical=0, high=1, medium=2, low=3. Unknown severities sort
// last.
func severityRank(sev string) int {
	switch sev {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}

// sortBySeverity stable-sorts findings by severity (critical > high >
// medium > low), then by file, then by line. Stable sort preserves
// insertion order for findings with equal severity, file, and line.
func sortBySeverity(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		ri, rj := severityRank(findings[i].Severity), severityRank(findings[j].Severity)
		if ri != rj {
			return ri < rj
		}
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
}
