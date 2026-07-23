//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package eval provides deterministic fixture-level acceptance metrics.
package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
)

// ExpectedFixture maps a public fixture to the rule ids it must trigger.
type ExpectedFixture struct {
	Name            string
	HighRiskRuleIDs []string
	ExpectClean     bool
	ExpectSecret    bool
}

// Result captures deterministic evaluation metrics for a fixture set.
type Result struct {
	FixtureCount          int
	ExpectedHighRiskCount int
	DetectedHighRiskCount int
	CleanSampleCount      int
	CleanFindingCount     int
	SecretSampleCount     int
	RedactedSampleCount   int
	HighRiskRecall        float64
	FalsePositiveRate     float64
	RedactionRecall       float64
}

// PublicExpectations covers the checked-in acceptance fixtures.
func PublicExpectations() []ExpectedFixture {
	return []ExpectedFixture{
		{Name: "clean.diff", ExpectClean: true},
		{Name: "security_secret.diff", HighRiskRuleIDs: []string{"security.secret_leak"}, ExpectSecret: true},
		{Name: "goroutine_context_leak.diff", HighRiskRuleIDs: []string{"concurrency.goroutine_context_leak"}},
		{Name: "resource_not_closed.diff", HighRiskRuleIDs: []string{"resource.close_missing"}},
		{Name: "db_lifecycle.diff", HighRiskRuleIDs: []string{"db.lifecycle"}},
		{Name: "missing_tests.diff"},
		{Name: "duplicate_findings.diff"},
		{Name: "secret_redaction.diff", HighRiskRuleIDs: []string{"security.secret_leak"}, ExpectSecret: true},
		{Name: "sandbox_failure.diff"},
	}
}

// EvaluatePublicFixtures evaluates deterministic rule coverage over fixtures.
func EvaluatePublicFixtures(ctx context.Context, fixtureDir string, expectations []ExpectedFixture) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(expectations) == 0 {
		expectations = PublicExpectations()
	}
	result := Result{FixtureCount: len(expectations)}
	for _, expectation := range expectations {
		raw, err := os.ReadFile(filepath.Join(fixtureDir, expectation.Name))
		if err != nil {
			return Result{}, fmt.Errorf("read fixture %s: %w", expectation.Name, err)
		}
		files, err := diffparse.Parse(string(raw))
		if err != nil {
			return Result{}, fmt.Errorf("parse fixture %s: %w", expectation.Name, err)
		}
		findings := rules.Evaluate(files)
		foundRules := ruleSet(findings)
		expectedRules := stringSet(expectation.HighRiskRuleIDs)
		result.ExpectedHighRiskCount += len(expectedRules)
		for ruleID := range expectedRules {
			if foundRules[ruleID] {
				result.DetectedHighRiskCount++
			}
		}
		if expectation.ExpectClean {
			result.CleanSampleCount++
			result.CleanFindingCount += highConfidenceFindingCount(findings)
		}
		if expectation.ExpectSecret || redact.ContainsSecret(string(raw)) {
			result.SecretSampleCount++
			if allFindingsRedacted(findings) {
				result.RedactedSampleCount++
			}
		}
	}
	if result.ExpectedHighRiskCount > 0 {
		result.HighRiskRecall = float64(result.DetectedHighRiskCount) / float64(result.ExpectedHighRiskCount)
	}
	if result.CleanSampleCount > 0 {
		result.FalsePositiveRate = float64(result.CleanFindingCount) / float64(result.CleanSampleCount)
	}
	if result.SecretSampleCount > 0 {
		result.RedactionRecall = float64(result.RedactedSampleCount) / float64(result.SecretSampleCount)
	}
	return result, nil
}

func ruleSet(findings []review.Finding) map[string]bool {
	out := map[string]bool{}
	for _, finding := range findings {
		out[finding.RuleID] = true
	}
	return out
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}

func allFindingsRedacted(findings []review.Finding) bool {
	for _, finding := range findings {
		if redact.ContainsSecret(finding.Evidence) || redact.ContainsSecret(finding.Recommendation) {
			return false
		}
	}
	return true
}

func highConfidenceFindingCount(findings []review.Finding) int {
	count := 0
	for _, finding := range findings {
		if finding.Status == review.FindingStatusFinding {
			count++
		}
	}
	return count
}
