//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewer

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

// canonicalReviewSubmission is the complete result projection accepted from
// one submit_review_results call. The Store returns committed counts after
// persisting Results, so this type has no second count representation.
type canonicalReviewSubmission struct {
	Results []store.ReviewResultRecord
}

type ruleLocationKey struct {
	file   string
	line   int
	ruleID string
}

const agentRulePrefix = "AGENT-"

// unknownLineLiteralKey includes every normalized business field. Line zero is
// not a location identity, so only literally identical records are collapsed.
type unknownLineLiteralKey struct {
	resultKind     string
	severity       string
	category       string
	file           string
	title          string
	evidence       string
	recommendation string
	confidence     float64
	source         string
	ruleID         string
}

type reviewSubmissionCandidate struct {
	result store.ReviewResultRecord
	origin string
}

type canonicalResultAccumulator struct {
	result       store.ReviewResultRecord
	firstOrigin  string
	evidence     []string
	evidenceSeen map[string]struct{}
}

func newCanonicalResultAccumulator(
	result store.ReviewResultRecord,
	origin string,
) canonicalResultAccumulator {
	return canonicalResultAccumulator{
		result:       result,
		firstOrigin:  origin,
		evidence:     []string{result.Evidence},
		evidenceSeen: map[string]struct{}{result.Evidence: {}},
	}
}

func (a *canonicalResultAccumulator) merge(incoming store.ReviewResultRecord) {
	if strongerReviewResult(incoming, a.result) {
		a.result = incoming
	}
	if _, exists := a.evidenceSeen[incoming.Evidence]; !exists {
		a.evidenceSeen[incoming.Evidence] = struct{}{}
		a.evidence = append(a.evidence, incoming.Evidence)
	}
}

func (a canonicalResultAccumulator) finalize() store.ReviewResultRecord {
	result := a.result
	result.Evidence = strings.Join(a.evidence, " Supporting evidence: ")
	return result
}

// canonicalizeReviewSubmission validates and canonicalizes one complete tool
// submission without mutating its input. The deterministic identity for an
// accurately located item is its normalized file, line, and opaque rule ID.
func canonicalizeReviewSubmission(
	results []store.ReviewResultRecord,
) (canonicalReviewSubmission, error) {
	candidates := make([]reviewSubmissionCandidate, 0, len(results))
	for index, result := range results {
		candidates = append(candidates, reviewSubmissionCandidate{
			result: result,
			origin: fmt.Sprintf("review_result[%d]", index),
		})
	}
	return canonicalizeReviewCandidates(candidates)
}

// canonicalizeReviewToolSubmission preserves the collection and index that the
// Agent supplied so a rejected submission can identify both conflicting items.
func canonicalizeReviewToolSubmission(
	input submitReviewResultsInput,
) (canonicalReviewSubmission, error) {
	candidates := make(
		[]reviewSubmissionCandidate,
		0,
		len(input.Findings)+len(input.Warnings)+len(input.NeedsHumanReview),
	)
	candidates = appendReviewCandidates(candidates, "finding", input.Findings)
	candidates = appendReviewCandidates(candidates, "warning", input.Warnings)
	candidates = appendReviewCandidates(
		candidates,
		"needs_human_review",
		input.NeedsHumanReview,
	)
	return canonicalizeReviewCandidates(candidates)
}

func canonicalizeReviewCandidates(
	candidates []reviewSubmissionCandidate,
) (canonicalReviewSubmission, error) {
	accumulators := make([]canonicalResultAccumulator, 0, len(candidates))
	located := make(map[ruleLocationKey]int)
	unknownLine := make(map[unknownLineLiteralKey]struct{})

	for _, candidate := range candidates {
		result, err := normalizeAndValidateReviewResult(
			candidate.origin,
			candidate.result,
		)
		if err != nil {
			return canonicalReviewSubmission{}, err
		}

		if result.Line == 0 {
			key := literalKeyForUnknownLine(result)
			if _, ok := unknownLine[key]; ok {
				continue
			}
			unknownLine[key] = struct{}{}
			accumulators = append(
				accumulators,
				newCanonicalResultAccumulator(result, candidate.origin),
			)
			continue
		}

		key := ruleLocationKey{
			file:   result.File,
			line:   result.Line,
			ruleID: result.RuleID,
		}
		existingIndex, ok := located[key]
		if !ok {
			located[key] = len(accumulators)
			accumulators = append(
				accumulators,
				newCanonicalResultAccumulator(result, candidate.origin),
			)
			continue
		}

		existing := &accumulators[existingIndex]
		if existing.result.Category != result.Category {
			return canonicalReviewSubmission{}, fmt.Errorf(
				"review result category conflict for %s:%d rule %q: %s uses %q, %s uses %q",
				result.File,
				result.Line,
				result.RuleID,
				existing.firstOrigin,
				existing.result.Category,
				candidate.origin,
				result.Category,
			)
		}
		if existing.result.ResultKind != result.ResultKind {
			return canonicalReviewSubmission{}, fmt.Errorf(
				"review result kind conflict for %s:%d rule %q: %s uses %q, %s uses %q",
				result.File,
				result.Line,
				result.RuleID,
				existing.firstOrigin,
				existing.result.ResultKind,
				candidate.origin,
				result.ResultKind,
			)
		}
		existing.merge(result)
	}

	submission := canonicalReviewSubmission{
		Results: make([]store.ReviewResultRecord, 0, len(accumulators)),
	}
	for _, accumulator := range accumulators {
		result := accumulator.finalize()
		submission.Results = append(submission.Results, result)
	}
	return submission, nil
}

func appendReviewCandidates(
	candidates []reviewSubmissionCandidate,
	kind string,
	inputs []reviewResultInput,
) []reviewSubmissionCandidate {
	for index, input := range inputs {
		candidates = append(candidates, reviewSubmissionCandidate{
			result: reviewResultRecord(kind, input),
			origin: fmt.Sprintf("%s[%d]", kind, index),
		})
	}
	return candidates
}

func reviewResultRecord(
	kind string,
	input reviewResultInput,
) store.ReviewResultRecord {
	return store.ReviewResultRecord{
		ResultKind:     kind,
		Severity:       input.Severity,
		Category:       input.Category,
		File:           input.File,
		Line:           input.Line,
		Title:          input.Title,
		Evidence:       input.Evidence,
		Recommendation: input.Recommendation,
		Confidence:     input.Confidence,
		Source:         input.Source,
		RuleID:         input.RuleID,
	}
}

func normalizeAndValidateReviewResult(
	origin string,
	result store.ReviewResultRecord,
) (store.ReviewResultRecord, error) {
	result.ResultKind = strings.ToLower(strings.TrimSpace(result.ResultKind))
	result.Severity = strings.ToLower(strings.TrimSpace(result.Severity))
	result.Category = strings.ToLower(strings.TrimSpace(result.Category))
	result.Source = strings.ToLower(strings.TrimSpace(result.Source))
	result.RuleID = strings.TrimSpace(result.RuleID)
	result.Title = strings.TrimSpace(result.Title)
	result.Evidence = strings.TrimSpace(result.Evidence)
	result.Recommendation = strings.TrimSpace(result.Recommendation)
	result.File = strings.TrimPrefix(
		path.Clean(strings.ReplaceAll(strings.TrimSpace(result.File), "\\", "/")),
		"./",
	)
	if result.File == "." {
		result.File = ""
	}

	if !validReviewResultKind(result.ResultKind) {
		return store.ReviewResultRecord{}, fmt.Errorf(
			"%s has unsupported result kind %q",
			origin,
			result.ResultKind,
		)
	}
	if !validReviewSeverity(result.Severity) {
		return store.ReviewResultRecord{}, fmt.Errorf(
			"%s has unsupported severity %q",
			origin,
			result.Severity,
		)
	}
	if !validReviewCategory(result.Category) {
		return store.ReviewResultRecord{}, fmt.Errorf(
			"%s has unsupported category %q",
			origin,
			result.Category,
		)
	}
	if !validReviewSource(result.Source) {
		return store.ReviewResultRecord{}, fmt.Errorf(
			"%s has unsupported source %q",
			origin,
			result.Source,
		)
	}
	if result.Confidence < 0 || result.Confidence > 1 {
		return store.ReviewResultRecord{}, fmt.Errorf(
			"%s confidence must be between 0 and 1",
			origin,
		)
	}
	if result.ResultKind == "finding" && result.Confidence < 0.80 {
		return store.ReviewResultRecord{}, fmt.Errorf(
			"%s finding confidence must be at least 0.80",
			origin,
		)
	}
	if result.ResultKind == "finding" && result.Category == "tests" {
		return store.ReviewResultRecord{}, fmt.Errorf(
			"%s is a test-coverage advisory and must be submitted as a warning",
			origin,
		)
	}
	if result.File == "" || !filepath.IsLocal(result.File) {
		return store.ReviewResultRecord{}, fmt.Errorf(
			"%s file must be a repository-relative path",
			origin,
		)
	}
	if result.Line < 0 {
		return store.ReviewResultRecord{}, fmt.Errorf(
			"%s requires a non-negative line",
			origin,
		)
	}
	if result.Title == "" ||
		result.Evidence == "" ||
		result.Recommendation == "" ||
		result.RuleID == "" {
		return store.ReviewResultRecord{}, fmt.Errorf(
			"%s requires a title, evidence, recommendation, and rule_id",
			origin,
		)
	}
	if strings.HasPrefix(result.RuleID, agentRulePrefix) {
		if result.Source != "agent" {
			return store.ReviewResultRecord{}, fmt.Errorf(
				"%s reserved agent rule_id requires source %q",
				origin,
				"agent",
			)
		}
		expected := directAgentRuleID(result.Category)
		if result.RuleID != expected {
			return store.ReviewResultRecord{}, fmt.Errorf(
				"%s source %q category %q requires reserved rule_id %q",
				origin,
				result.Source,
				result.Category,
				expected,
			)
		}
	}
	return result, nil
}

func directAgentRuleID(category string) string {
	return agentRulePrefix + strings.ToUpper(strings.ReplaceAll(category, "_", "-"))
}

func validReviewResultKind(kind string) bool {
	switch kind {
	case "finding", "warning", "needs_human_review":
		return true
	default:
		return false
	}
}

func validReviewSeverity(severity string) bool {
	switch severity {
	case "critical", "high", "medium", "low", "warning":
		return true
	default:
		return false
	}
}

func validReviewCategory(category string) bool {
	switch category {
	case "correctness",
		"security",
		"sensitive_info",
		"concurrency",
		"resource_lifecycle",
		"error_handling",
		"tests",
		"database_lifecycle":
		return true
	default:
		return false
	}
}

func validReviewSource(source string) bool {
	switch source {
	case "agent", "skill", "static_rule", "go_test", "go_vet", "staticcheck", "sandbox":
		return true
	default:
		return false
	}
}

func literalKeyForUnknownLine(result store.ReviewResultRecord) unknownLineLiteralKey {
	return unknownLineLiteralKey{
		resultKind:     result.ResultKind,
		severity:       result.Severity,
		category:       result.Category,
		file:           result.File,
		title:          result.Title,
		evidence:       result.Evidence,
		recommendation: result.Recommendation,
		confidence:     result.Confidence,
		source:         result.Source,
		ruleID:         result.RuleID,
	}
}

func strongerReviewResult(candidate, current store.ReviewResultRecord) bool {
	candidateSeverity := reviewSeverityRank(candidate.Severity)
	currentSeverity := reviewSeverityRank(current.Severity)
	if candidateSeverity != currentSeverity {
		return candidateSeverity > currentSeverity
	}
	return candidate.Confidence > current.Confidence
}

func reviewSeverityRank(severity string) int {
	switch severity {
	case "critical":
		return 5
	case "high":
		return 4
	case "medium":
		return 3
	case "low":
		return 2
	case "warning":
		return 1
	default:
		return 0
	}
}

// validateReviewResults remains as the validation-only seam used by focused
// tests. Production callers use canonicalizeReviewSubmission and persist its
// returned projection.
func validateReviewResults(results []store.ReviewResultRecord) error {
	_, err := canonicalizeReviewSubmission(results)
	return err
}
