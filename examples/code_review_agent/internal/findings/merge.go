//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package findings

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const (
	maxCandidates       = 10_000
	maxCanonicalFinding = 5_000
	maxEvidencePerGroup = 32
	maxEvidenceBytes    = 64 << 10
)

// Normalize canonicalizes, deduplicates, and deterministically sorts findings
// under one authoritative task identity.
func Normalize(taskID string, diff input.Diff, candidates []Candidate) ([]review.Finding, error) {
	if !taskIDPattern.MatchString(taskID) {
		return nil, errors.New("normalize findings: invalid task id")
	}
	if len(candidates) > maxCandidates {
		return nil, errors.New("normalize findings: candidate limit exceeded")
	}
	groups := make(map[string]*findingGroup, len(candidates))
	for index, candidate := range candidates {
		if candidate.TaskID != "" && candidate.TaskID != taskID {
			return nil, errors.New("normalize findings: conflicting task id")
		}
		candidate.TaskID = taskID
		canonical, err := Canonicalize(diff, candidate)
		if err != nil {
			return nil, fmt.Errorf("normalize finding %d: %w", index, err)
		}
		group := groups[canonical.Fingerprint]
		if group == nil {
			if len(groups) >= maxCanonicalFinding {
				return nil, errors.New("normalize findings: finding limit exceeded")
			}
			group, err = newFindingGroup(canonical)
			if err != nil {
				return nil, fmt.Errorf("normalize finding %d: %w", index, err)
			}
			groups[canonical.Fingerprint] = group
			continue
		}
		if err := group.merge(canonical); err != nil {
			return nil, fmt.Errorf("normalize finding %d: %w", index, err)
		}
	}

	result := make([]review.Finding, 0, len(groups))
	for _, group := range groups {
		finding := group.finalize()
		if err := finding.Validate(); err != nil {
			return nil, fmt.Errorf("normalize merged finding: %w", err)
		}
		result = append(result, finding)
	}
	sort.Slice(result, func(left, right int) bool {
		return less(result[left], result[right])
	})
	return result, nil
}

type findingGroup struct {
	finding       review.Finding
	evidence      map[string]struct{}
	evidenceBytes int
	hasFinding    bool
	hasWarning    bool
	hasHuman      bool
}

func newFindingGroup(finding review.Finding) (*findingGroup, error) {
	group := &findingGroup{finding: finding, evidence: make(map[string]struct{})}
	group.recordDisposition(finding.Disposition)
	if err := group.addEvidence(finding.Evidence); err != nil {
		return nil, err
	}
	return group, nil
}

func (g *findingGroup) merge(other review.Finding) error {
	if g.finding.TaskID == "" {
		g.finding.TaskID = other.TaskID
	}
	if severityRank(other.Severity) > severityRank(g.finding.Severity) {
		g.finding.Severity = other.Severity
	}
	if confidenceRank(other.Confidence) > confidenceRank(g.finding.Confidence) {
		g.finding.Confidence = other.Confidence
	}
	if authoritativeBefore(other, g.finding) {
		g.finding.Source = other.Source
		g.finding.Category = other.Category
		g.finding.Title = other.Title
		g.finding.Recommendation = other.Recommendation
		g.finding.EndLine = other.EndLine
	}
	g.recordDisposition(other.Disposition)
	return g.addEvidence(other.Evidence)
}

func (g *findingGroup) finalize() review.Finding {
	evidence := make([]string, 0, len(g.evidence))
	for value := range g.evidence {
		evidence = append(evidence, value)
	}
	sort.Strings(evidence)
	g.finding.Evidence = redact.String(strings.Join(evidence, "\n\n"))
	if g.finding.Confidence == review.ConfidenceLow {
		g.finding.Disposition = review.DispositionNeedsHumanReview
	} else if g.hasFinding {
		g.finding.Disposition = review.DispositionFinding
	} else if g.hasHuman {
		g.finding.Disposition = review.DispositionNeedsHumanReview
	} else {
		g.finding.Disposition = review.DispositionWarning
	}
	return g.finding
}

func (g *findingGroup) addEvidence(value string) error {
	if _, ok := g.evidence[value]; ok {
		return nil
	}
	separator := 0
	if len(g.evidence) > 0 {
		separator = 2
	}
	if len(g.evidence) >= maxEvidencePerGroup ||
		g.evidenceBytes > maxEvidenceBytes-len(value)-separator {
		return errors.New("merge findings: evidence limit exceeded")
	}
	g.evidence[value] = struct{}{}
	g.evidenceBytes += len(value) + separator
	return nil
}

func (g *findingGroup) recordDisposition(disposition review.Disposition) {
	switch disposition {
	case review.DispositionFinding:
		g.hasFinding = true
	case review.DispositionWarning:
		g.hasWarning = true
	case review.DispositionNeedsHumanReview:
		g.hasHuman = true
	}
}

func authoritativeBefore(left, right review.Finding) bool {
	if sourceRank(left.Source) != sourceRank(right.Source) {
		return sourceRank(left.Source) < sourceRank(right.Source)
	}
	leftKey := strings.Join([]string{left.Category, left.Title, left.Recommendation}, "\x00")
	rightKey := strings.Join([]string{right.Category, right.Title, right.Recommendation}, "\x00")
	return leftKey < rightKey
}

func less(left, right review.Finding) bool {
	if left.File != right.File {
		return left.File < right.File
	}
	if left.Layer != right.Layer {
		return left.Layer < right.Layer
	}
	if left.Line != right.Line {
		return left.Line < right.Line
	}
	if left.EndLine != right.EndLine {
		return left.EndLine < right.EndLine
	}
	if left.Severity != right.Severity {
		return severityRank(left.Severity) > severityRank(right.Severity)
	}
	if left.Category != right.Category {
		return left.Category < right.Category
	}
	if left.RuleID != right.RuleID {
		return left.RuleID < right.RuleID
	}
	return left.Fingerprint < right.Fingerprint
}

func severityRank(severity review.Severity) int {
	switch severity {
	case review.SeverityCritical:
		return 5
	case review.SeverityHigh:
		return 4
	case review.SeverityMedium:
		return 3
	case review.SeverityLow:
		return 2
	case review.SeverityInfo:
		return 1
	default:
		return 0
	}
}

func confidenceRank(confidence review.Confidence) int {
	switch confidence {
	case review.ConfidenceHigh:
		return 3
	case review.ConfidenceMedium:
		return 2
	case review.ConfidenceLow:
		return 1
	default:
		return 0
	}
}

func sourceRank(source review.Source) int {
	switch source {
	case review.SourceRule:
		return 1
	case review.SourceTool:
		return 2
	case review.SourceModel:
		return 3
	default:
		return 4
	}
}
