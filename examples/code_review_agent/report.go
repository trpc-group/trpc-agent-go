//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

const findingConfidenceThreshold = 0.80

var severityRanks = map[string]int{
	"critical": 4,
	"high":     3,
	"medium":   2,
	"low":      1,
}

type reviewFinding struct {
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

type redactionResult struct {
	Text  string
	Count int
	Types []string
}

type finalizedFindings struct {
	Findings          []reviewFinding
	Warnings          []reviewFinding
	SuppressedMatches int
	Redactions        int
	NeedsHumanReview  bool
	SeverityCounts    map[string]int
	FindingRuleIDs    []string
	WarningRuleIDs    []string
}

type redactionRule struct {
	pattern      *regexp.Regexp
	redactedType string
	classify     func(string) string
}

var redactionRules = []redactionRule{
	{
		pattern:      regexp.MustCompile(`(?is)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
		redactedType: "private-key",
	},
	{
		pattern: regexp.MustCompile(`(?i)\b[A-Za-z0-9_]*(?:api[_-]?key|token|password|passwd|secret|private[_-]?key)[A-Za-z0-9_]*\s*(?::=|[:=])\s*["']?[^"'\s,;]{6,}["']?`),
		classify: func(match string) string {
			lower := strings.ToLower(match)
			switch {
			case strings.Contains(match, "AKIA"), strings.Contains(match, "ASIA"):
				return "aws-key"
			case strings.Contains(match, "ghp_"), strings.Contains(match, "gho_"),
				strings.Contains(match, "ghu_"), strings.Contains(match, "ghs_"),
				strings.Contains(match, "ghr_"):
				return "github-token"
			case strings.Contains(match, "sk-"):
				return "openai-token"
			case strings.Contains(lower, "password"), strings.Contains(lower, "passwd"):
				return "password"
			case strings.Contains(lower, "api"):
				return "api-key"
			case strings.Contains(lower, "token"):
				return "token"
			case strings.Contains(lower, "private"):
				return "private-key"
			default:
				return "secret"
			}
		},
	},
	{
		pattern:      regexp.MustCompile(`(?i)\bAuthorization\s*[:=]\s*(?:Bearer|Basic|Token)?\s*[A-Za-z0-9._~+/=-]{8,}`),
		redactedType: "authorization",
	},
	{
		pattern:      regexp.MustCompile(`(?i)\bX-API-Key\s*[:=]\s*["']?[^"'\s,;]{8,}["']?`),
		redactedType: "api-key",
	},
	{
		pattern:      regexp.MustCompile(`(?i)\b(?:Set-Cookie|Cookie)\s*[:=]\s*[^\r\n]+`),
		redactedType: "cookie",
	},
	{
		pattern:      regexp.MustCompile(`(?i)\b(?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis)://[^\s"']+`),
		redactedType: "connection-string",
	},
	{
		pattern:      regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^/\s"'@]+:[^/\s"'@]+@[^\s"']+`),
		redactedType: "url-userinfo",
	},
	{
		pattern:      regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
		redactedType: "aws-key",
	},
	{
		pattern:      regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{20,}\b`),
		redactedType: "github-token",
	},
	{
		pattern:      regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`),
		redactedType: "openai-token",
	},
	{
		pattern:      regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{16,}\b`),
		redactedType: "bearer-token",
	},
	{
		pattern: regexp.MustCompile(`(?i)\b[A-Za-z0-9_]*(?:api[_-]?key|token|password|passwd|secret|private[_-]?key)[A-Za-z0-9_]*\s*(?::=|[:=])\s*["']?[^"'\s,;]{6,}["']?`),
		classify: func(match string) string {
			lower := strings.ToLower(match)
			switch {
			case strings.Contains(lower, "password"), strings.Contains(lower, "passwd"):
				return "password"
			case strings.Contains(lower, "api"):
				return "api-key"
			case strings.Contains(lower, "token"):
				return "token"
			case strings.Contains(lower, "private"):
				return "private-key"
			default:
				return "secret"
			}
		},
	},
}

func finalizeRuleMatches(matches []ruleMatch) finalizedFindings {
	findings, findingSuppressed := dedupeRuleMatches(routeRuleMatches(matches, true))
	warnings, warningSuppressed := dedupeRuleMatches(routeRuleMatches(matches, false))

	result := finalizedFindings{
		SuppressedMatches: findingSuppressed + warningSuppressed,
		SeverityCounts:    map[string]int{},
	}

	result.Findings, result.Redactions = sanitizeMatches(findings)
	result.Warnings, result.Redactions = appendSanitizedMatches(result.Warnings, warnings, result.Redactions)
	sortReviewFindings(result.Findings)
	sortReviewFindings(result.Warnings)

	result.NeedsHumanReview = len(result.Warnings) > 0
	result.FindingRuleIDs = uniqueRuleIDs(result.Findings)
	result.WarningRuleIDs = uniqueRuleIDs(result.Warnings)
	for _, finding := range result.Findings {
		result.SeverityCounts[finding.Severity]++
	}
	for _, warning := range result.Warnings {
		result.SeverityCounts[warning.Severity]++
	}
	return result
}

func routeRuleMatches(matches []ruleMatch, wantFindings bool) []ruleMatch {
	routed := make([]ruleMatch, 0, len(matches))
	for _, match := range matches {
		normalized := match
		normalized.Confidence = normalizeConfidence(match.Confidence)
		isFinding := normalized.Confidence >= findingConfidenceThreshold
		if isFinding == wantFindings {
			routed = append(routed, normalized)
		}
	}
	return routed
}

func normalizeConfidence(confidence float64) float64 {
	switch {
	case math.IsNaN(confidence), confidence < 0:
		return 0
	case confidence > 1:
		return 1
	default:
		return confidence
	}
}

func dedupeRuleMatches(matches []ruleMatch) ([]ruleMatch, int) {
	byKey := map[string]ruleMatch{}
	suppressed := 0
	for _, match := range matches {
		key := dedupeKey(match)
		existing, ok := byKey[key]
		if !ok {
			byKey[key] = match
			continue
		}
		suppressed++
		if isBetterRuleMatch(match, existing) {
			byKey[key] = match
		}
	}

	deduped := make([]ruleMatch, 0, len(byKey))
	for _, match := range byKey {
		deduped = append(deduped, match)
	}
	sort.Slice(deduped, func(i, j int) bool {
		return compareRuleMatches(deduped[i], deduped[j]) < 0
	})
	return deduped, suppressed
}

func dedupeKey(match ruleMatch) string {
	return fmt.Sprintf("%s\x00%d\x00%s", match.File, match.Line, match.Category)
}

func isBetterRuleMatch(candidate ruleMatch, existing ruleMatch) bool {
	if candidate.Confidence != existing.Confidence {
		return candidate.Confidence > existing.Confidence
	}
	candidateSeverity := severityRank(candidate.Severity)
	existingSeverity := severityRank(existing.Severity)
	if candidateSeverity != existingSeverity {
		return candidateSeverity > existingSeverity
	}
	return candidate.RuleID < existing.RuleID
}

func compareRuleMatches(left ruleMatch, right ruleMatch) int {
	if left.File != right.File {
		return strings.Compare(left.File, right.File)
	}
	if left.Line != right.Line {
		if left.Line < right.Line {
			return -1
		}
		return 1
	}
	if severityRank(left.Severity) != severityRank(right.Severity) {
		if severityRank(left.Severity) > severityRank(right.Severity) {
			return -1
		}
		return 1
	}
	if left.Category != right.Category {
		return strings.Compare(left.Category, right.Category)
	}
	if left.RuleID != right.RuleID {
		return strings.Compare(left.RuleID, right.RuleID)
	}
	return strings.Compare(left.Title, right.Title)
}

func sanitizeMatches(matches []ruleMatch) ([]reviewFinding, int) {
	return appendSanitizedMatches(nil, matches, 0)
}

func appendSanitizedMatches(existing []reviewFinding, matches []ruleMatch, redactions int) ([]reviewFinding, int) {
	for _, match := range matches {
		finding, count := sanitizeRuleMatch(match)
		existing = append(existing, finding)
		redactions += count
	}
	return existing, redactions
}

func sanitizeRuleMatch(match ruleMatch) (reviewFinding, int) {
	var redactions int
	file, count := redactField(match.File)
	redactions += count
	title, count := redactField(match.Title)
	redactions += count
	evidence, count := redactField(match.Evidence)
	redactions += count
	recommendation, count := redactField(match.Recommendation)
	redactions += count
	source, count := redactField(match.Source)
	redactions += count
	ruleID, count := redactField(match.RuleID)
	redactions += count
	category, count := redactField(match.Category)
	redactions += count
	severity, count := redactField(match.Severity)
	redactions += count

	return reviewFinding{
		Severity:       severity,
		Category:       category,
		File:           file,
		Line:           match.Line,
		Title:          title,
		Evidence:       evidence,
		Recommendation: recommendation,
		Confidence:     normalizeConfidence(match.Confidence),
		Source:         source,
		RuleID:         ruleID,
	}, redactions
}

func redactField(value string) (string, int) {
	redacted := redactText(value)
	return redacted.Text, redacted.Count
}

func sortReviewFindings(findings []reviewFinding) {
	sort.Slice(findings, func(i, j int) bool {
		left := findings[i]
		right := findings[j]
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if severityRank(left.Severity) != severityRank(right.Severity) {
			return severityRank(left.Severity) > severityRank(right.Severity)
		}
		if left.Category != right.Category {
			return left.Category < right.Category
		}
		if left.RuleID != right.RuleID {
			return left.RuleID < right.RuleID
		}
		return left.Title < right.Title
	})
}

func severityRank(severity string) int {
	return severityRanks[strings.ToLower(severity)]
}

func uniqueRuleIDs(findings []reviewFinding) []string {
	seen := map[string]bool{}
	for _, finding := range findings {
		if finding.RuleID == "" {
			continue
		}
		seen[finding.RuleID] = true
	}
	ruleIDs := make([]string, 0, len(seen))
	for id := range seen {
		ruleIDs = append(ruleIDs, id)
	}
	sort.Strings(ruleIDs)
	return ruleIDs
}

func redactParseWarningMessages(warnings []parseWarning) ([]string, int) {
	if len(warnings) == 0 {
		return nil, 0
	}
	messages := make([]string, 0, len(warnings))
	redactions := 0
	for _, warning := range warnings {
		message := warning.Message
		if warning.File != "" {
			message = fmt.Sprintf("%s:%d: %s", warning.File, warning.Line, warning.Message)
		} else if warning.Line > 0 {
			message = fmt.Sprintf("line %d: %s", warning.Line, warning.Message)
		}
		redacted := redactText(message)
		messages = append(messages, redacted.Text)
		redactions += redacted.Count
	}
	sort.Strings(messages)
	return messages, redactions
}

func redactText(text string) redactionResult {
	typeSet := map[string]bool{}
	redactedText := text
	count := 0
	for _, rule := range redactionRules {
		redactedText = rule.pattern.ReplaceAllStringFunc(redactedText, func(match string) string {
			if strings.Contains(match, "<redacted:") {
				return match
			}
			redactedType := rule.redactedType
			if rule.classify != nil {
				redactedType = rule.classify(match)
			}
			count++
			typeSet[redactedType] = true
			return "<redacted:" + redactedType + ">"
		})
	}

	types := make([]string, 0, len(typeSet))
	for redactedType := range typeSet {
		types = append(types, redactedType)
	}
	sort.Strings(types)
	return redactionResult{
		Text:  redactedText,
		Count: count,
		Types: types,
	}
}
