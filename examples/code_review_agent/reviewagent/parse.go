//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package reviewagent

import (
	"encoding/json"
	"errors"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/redaction"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

// downgradedConfidence routes unverifiable model claims to the
// needs-human-review bucket instead of high-confidence findings.
const downgradedConfidence = 0.5

// ParsedReview is the validated payload extracted from a model reply.
type ParsedReview struct {
	Summary  string
	Findings []review.Finding
}

type modelReply struct {
	Summary  string         `json:"summary"`
	Findings []modelFinding `json:"findings"`
}

type modelFinding struct {
	Severity       string  `json:"severity"`
	Category       string  `json:"category"`
	File           string  `json:"file"`
	Line           int     `json:"line"`
	Title          string  `json:"title"`
	Evidence       string  `json:"evidence"`
	Recommendation string  `json:"recommendation"`
	Confidence     float64 `json:"confidence"`
	RuleID         string  `json:"rule_id"`
}

// ParseModelReview validates model output against the changed files so
// hallucinated files or lines are downgraded instead of trusted.
func ParseModelReview(content string, files []review.ChangedFile, source string) (ParsedReview, error) {
	payload := extractJSON(content)
	if payload == "" {
		return ParsedReview{}, errors.New("model reply did not contain a JSON object")
	}
	var reply modelReply
	if err := json.Unmarshal([]byte(payload), &reply); err != nil {
		return ParsedReview{}, err
	}
	out := ParsedReview{Summary: redaction.RedactText(strings.TrimSpace(reply.Summary))}
	for _, f := range reply.Findings {
		if strings.TrimSpace(f.Title) == "" {
			continue
		}
		finding := review.Finding{
			Severity:       normalizeSeverity(f.Severity),
			Category:       normalizeCategory(f.Category),
			File:           strings.TrimSpace(f.File),
			Line:           f.Line,
			Title:          redaction.RedactText(strings.TrimSpace(f.Title)),
			Evidence:       redaction.RedactText(strings.TrimSpace(f.Evidence)),
			Recommendation: redaction.RedactText(strings.TrimSpace(f.Recommendation)),
			Confidence:     clampConfidence(f.Confidence),
			Source:         source,
			RuleID:         normalizeRuleID(f.RuleID),
		}
		if !locationInDiff(finding.File, finding.Line, files) {
			// The model referenced a file or line that is not part of the
			// diff; keep the observation but force a human-review pass.
			if finding.Confidence > downgradedConfidence {
				finding.Confidence = downgradedConfidence
			}
		}
		out.Findings = append(out.Findings, finding)
	}
	return out, nil
}

// extractJSON tolerates markdown fences and prose around the JSON object.
func extractJSON(content string) string {
	content = strings.TrimSpace(content)
	if fenced := strings.Index(content, "```"); fenced >= 0 {
		rest := content[fenced+3:]
		rest = strings.TrimPrefix(rest, "json")
		if end := strings.Index(rest, "```"); end >= 0 {
			content = rest[:end]
		} else {
			content = rest
		}
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return ""
	}
	return content[start : end+1]
}

// normalizeSeverity maps model output onto the known severity levels.
func normalizeSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case review.SeverityCritical:
		return review.SeverityCritical
	case review.SeverityHigh:
		return review.SeverityHigh
	case review.SeverityMedium:
		return review.SeverityMedium
	default:
		return review.SeverityLow
	}
}

// normalizeCategory lower-cases the category, defaulting to model_review.
func normalizeCategory(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "model_review"
	}
	return s
}

// normalizeRuleID trims the rule ID, defaulting to LLM-GENERIC.
func normalizeRuleID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "LLM-GENERIC"
	}
	return s
}

// clampConfidence bounds a confidence value to the [0, 1] range.
func clampConfidence(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// locationInDiff reports whether file and line exist in the reviewed diff.
func locationInDiff(file string, line int, files []review.ChangedFile) bool {
	for _, f := range files {
		if f.NewPath != file {
			continue
		}
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if l.Kind == "added" && l.NewLine == line {
					return true
				}
			}
		}
	}
	return false
}
