//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package findings validates and canonicalizes findings from all producers.
package findings

import (
	"errors"
	"fmt"
	"regexp"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

var (
	ruleIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*/v[1-9][0-9]*$`)
	anchorPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]{0,127}$`)
	taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

// Candidate is an untrusted pre-canonical finding produced by a rule, tool, or model.
type Candidate struct {
	SchemaVersion  string
	TaskID         string
	Severity       review.Severity
	Category       string
	Layer          review.ChangeLayer
	File           string
	Line           int
	EndLine        int
	SemanticAnchor string
	Title          string
	Evidence       string
	Recommendation string
	Confidence     review.Confidence
	Source         review.Source
	RuleID         string
	Disposition    review.Disposition
}

// Canonicalize redacts, locates, fingerprints, and validates one finding.
func Canonicalize(diff input.Diff, candidate Candidate) (review.Finding, error) {
	finding := review.Finding{
		SchemaVersion:  candidate.SchemaVersion,
		TaskID:         candidate.TaskID,
		Severity:       candidate.Severity,
		Category:       candidate.Category,
		Layer:          candidate.Layer,
		File:           candidate.File,
		Line:           candidate.Line,
		EndLine:        candidate.EndLine,
		SemanticAnchor: candidate.SemanticAnchor,
		Title:          candidate.Title,
		Evidence:       candidate.Evidence,
		Recommendation: candidate.Recommendation,
		Confidence:     candidate.Confidence,
		Source:         candidate.Source,
		RuleID:         candidate.RuleID,
		Disposition:    candidate.Disposition,
	}
	if !ruleIDPattern.MatchString(finding.RuleID) {
		return review.Finding{}, errors.New("canonicalize finding: invalid versioned rule id")
	}
	if !anchorPattern.MatchString(finding.SemanticAnchor) {
		return review.Finding{}, errors.New("canonicalize finding: invalid semantic anchor")
	}
	if finding.TaskID != "" && !taskIDPattern.MatchString(finding.TaskID) {
		return review.Finding{}, errors.New("canonicalize finding: invalid task id")
	}
	if redact.String(finding.TaskID) != finding.TaskID || redact.String(finding.File) != finding.File ||
		redact.String(finding.RuleID) != finding.RuleID || redact.String(finding.SemanticAnchor) != finding.SemanticAnchor {
		return review.Finding{}, errors.New("canonicalize finding: secret-bearing identity field")
	}
	locations := addedLocations(diff)
	layer, err := resolveLayer(locations, finding)
	if err != nil {
		return review.Finding{}, err
	}
	finding.Layer = layer
	fileLines := locations[locationKey{layer: layer, file: finding.File}]
	if _, ok := fileLines[finding.Line]; !ok {
		return review.Finding{}, fmt.Errorf("canonicalize finding: line %d is not an added line", finding.Line)
	}
	if finding.EndLine != 0 {
		if finding.EndLine < finding.Line {
			return review.Finding{}, errors.New("canonicalize finding: end line precedes start line")
		}
		for line := finding.Line; line <= finding.EndLine; line++ {
			if _, ok := fileLines[line]; !ok {
				return review.Finding{}, fmt.Errorf(
					"canonicalize finding: range contains non-added line %d",
					line,
				)
			}
		}
	}

	finding.Category = redact.String(finding.Category)
	finding.Title = redact.String(finding.Title)
	finding.Evidence = redact.String(finding.Evidence)
	finding.Recommendation = redact.String(finding.Recommendation)
	if finding.Confidence == review.ConfidenceLow {
		finding.Disposition = review.DispositionNeedsHumanReview
	} else if finding.Disposition == "" {
		finding.Disposition = review.DispositionFinding
	}
	finding.Fingerprint = Fingerprint(finding)
	if err := finding.Validate(); err != nil {
		return review.Finding{}, fmt.Errorf("canonicalize finding: %w", err)
	}
	return finding, nil
}

type locationKey struct {
	layer review.ChangeLayer
	file  string
}

func addedLocations(diff input.Diff) map[locationKey]map[int]struct{} {
	locations := make(map[locationKey]map[int]struct{}, len(diff.Files))
	for _, file := range diff.Files {
		if file.NewPath == "" || file.Binary {
			continue
		}
		layer := file.Layer
		if layer == "" {
			layer = input.DiffLayerUnified
		}
		key := locationKey{layer: layer, file: file.NewPath}
		lines := locations[key]
		if lines == nil {
			lines = make(map[int]struct{})
			locations[key] = lines
		}
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind == input.LineAdded && line.NewNumber != nil {
					lines[*line.NewNumber] = struct{}{}
				}
			}
		}
	}
	return locations
}

func resolveLayer(
	locations map[locationKey]map[int]struct{},
	finding review.Finding,
) (review.ChangeLayer, error) {
	if finding.Layer != "" {
		if _, ok := locations[locationKey{layer: finding.Layer, file: finding.File}]; !ok {
			return "", errors.New("canonicalize finding: layer and file are not changed")
		}
		return finding.Layer, nil
	}
	var matched review.ChangeLayer
	for key, lines := range locations {
		if key.file != finding.File {
			continue
		}
		if _, ok := lines[finding.Line]; !ok {
			continue
		}
		if matched != "" && matched != key.layer {
			return "", errors.New("canonicalize finding: ambiguous change layer")
		}
		matched = key.layer
	}
	if matched == "" {
		return "", errors.New("canonicalize finding: file is not changed")
	}
	return matched, nil
}
