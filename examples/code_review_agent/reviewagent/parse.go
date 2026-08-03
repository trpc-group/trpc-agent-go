//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
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

const (
	maxStandaloneModelConfidence = 0.74
	maxMissingTestConfidence     = 0.60
	maxSpeculativeConfidence     = 0.40
)

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
		category := normalizeCategory(f.Category)
		ruleID := normalizeRuleID(f.RuleID)
		if source == ModeLLM {
			category = normalizeModelCategory(f)
			ruleID = modelRuleID(category)
		}
		finding := review.Finding{
			Severity:       normalizeSeverity(f.Severity),
			Category:       category,
			File:           strings.TrimSpace(f.File),
			Line:           f.Line,
			Title:          redaction.RedactText(strings.TrimSpace(f.Title)),
			Evidence:       redaction.RedactText(strings.TrimSpace(f.Evidence)),
			Recommendation: redaction.RedactText(strings.TrimSpace(f.Recommendation)),
			Confidence:     calibratedConfidence(f, category, source),
			Source:         source,
			RuleID:         ruleID,
		}
		if category == "missing_test" {
			finding.File, finding.Line = firstAddedGoLine(files)
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

func normalizeRuleID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "LLM-GENERIC"
	}
	return s
}

// normalizeModelCategory maps provider-specific labels onto a stable taxonomy.
func normalizeModelCategory(f modelFinding) string {
	category := strings.NewReplacer("-", "_", " ", "_").Replace(
		strings.ToLower(strings.TrimSpace(f.Category)))
	switch category {
	case "hardcoded_secret", "dynamic_sql", "goroutine_lifecycle",
		"context_propagation", "resource_lifecycle", "ignored_error",
		"transaction_lifecycle", "process_termination", "missing_test",
		"compile_diagnostic":
		return category
	}
	text := strings.ToLower(strings.Join([]string{
		f.Category, f.Title, f.Evidence, f.Recommendation, f.RuleID,
	}, " "))
	switch {
	case strings.Contains(text, "missing test") || strings.Contains(text, "test coverage"):
		return "missing_test"
	case strings.Contains(text, "hard-coded") || strings.Contains(text, "hardcoded"):
		return "hardcoded_secret"
	case strings.Contains(text, "sql") &&
		(strings.Contains(text, "inject") || strings.Contains(text, "interpol")):
		return "dynamic_sql"
	case strings.Contains(text, "goroutine"):
		return "goroutine_lifecycle"
	case strings.Contains(text, "context"):
		return "context_propagation"
	case strings.Contains(text, "transaction") || strings.Contains(text, "rollback"):
		return "transaction_lifecycle"
	case strings.Contains(text, "ignored error") || strings.Contains(text, "unchecked error"):
		return "ignored_error"
	case strings.Contains(text, "panic") || strings.Contains(text, "fatal"):
		return "process_termination"
	case strings.Contains(text, "compile") || strings.Contains(text, "undefined"):
		return "compile_diagnostic"
	case strings.Contains(text, "leak") || strings.Contains(text, "ticker") ||
		strings.Contains(text, "close"):
		return "resource_lifecycle"
	default:
		return "other"
	}
}

// modelRuleID returns a stable identifier controlled by the application.
func modelRuleID(category string) string {
	return "LLM-" + strings.ToUpper(strings.ReplaceAll(category, "_", "-"))
}

// calibratedConfidence treats provider confidence as an input, not a verdict.
// Standalone model findings require human review unless corroborated later by a
// deterministic rule, while speculative API-misuse claims remain warnings.
func calibratedConfidence(f modelFinding, category, source string) float64 {
	confidence := clampConfidence(f.Confidence)
	if source != ModeLLM {
		return confidence
	}
	confidence = minConfidence(confidence, maxStandaloneModelConfidence)
	if category == "missing_test" {
		confidence = minConfidence(confidence, maxMissingTestConfidence)
	}
	text := strings.ToLower(strings.Join([]string{
		f.Title, f.Evidence, f.Recommendation,
	}, " "))
	if speculativeEnvironmentClaim(text) || speculativeTickerDrainClaim(text) ||
		strings.TrimSpace(f.Evidence) == "" {
		confidence = minConfidence(confidence, maxSpeculativeConfidence)
	}
	return confidence
}

func speculativeEnvironmentClaim(text string) bool {
	return (strings.Contains(text, "environment variable") ||
		strings.Contains(text, "os.getenv")) &&
		(strings.HasPrefix(text, "if ") || strings.Contains(text, " if ") ||
			strings.Contains(text, "could") ||
			strings.Contains(text, "may"))
}

func speculativeTickerDrainClaim(text string) bool {
	return strings.Contains(text, "ticker") && strings.Contains(text, "drain")
}

func minConfidence(value, maximum float64) float64 {
	if value > maximum {
		return maximum
	}
	return value
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

func firstAddedGoLine(files []review.ChangedFile) (string, int) {
	for _, file := range files {
		if file.Language != "go" || strings.HasSuffix(file.NewPath, "_test.go") {
			continue
		}
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind == "added" {
					return file.NewPath, line.NewLine
				}
			}
		}
	}
	return "", 0
}
