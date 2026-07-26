//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llm

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestDecodeLLMOutputAcceptsPlainJSON(t *testing.T) {
	output, err := DecodeOutput(`{"findings":[{"rule_id":"plain-json","confidence":"high"}]}`)
	if err != nil {
		t.Fatalf("decode plain JSON: %v", err)
	}
	if !hasRuleID(output.Findings, "plain-json") {
		t.Fatalf("expected plain JSON finding, got %+v", output.Findings)
	}
}

func TestDecodeLLMOutputAcceptsFencedJSON(t *testing.T) {
	output, err := DecodeOutput("```json\n{\"findings\":[{\"rule_id\":\"fenced-json\",\"confidence\":\"low\"}]}\n```")
	if err != nil {
		t.Fatalf("decode fenced JSON: %v", err)
	}
	if !hasRuleID(output.Findings, "fenced-json") {
		t.Fatalf("expected fenced JSON finding, got %+v", output.Findings)
	}
}

func TestDecodeLLMOutputExtractsJSONFromText(t *testing.T) {
	output, err := DecodeOutput("Review result:\n{\"findings\":[{\"rule_id\":\"embedded-json\",\"confidence\":\"medium\"}]}\nDone.")
	if err != nil {
		t.Fatalf("decode embedded JSON: %v", err)
	}
	if !hasRuleID(output.Findings, "embedded-json") {
		t.Fatalf("expected embedded JSON finding, got %+v", output.Findings)
	}
}

func TestDecodeLLMOutputEmptyContent(t *testing.T) {
	output, err := DecodeOutput("  ")
	if err != nil {
		t.Fatalf("decode empty content: %v", err)
	}
	if len(output.Findings) != 0 {
		t.Fatalf("expected empty output, got %+v", output)
	}
}

func TestDecodeLLMOutputRedactsInvalidJSONError(t *testing.T) {
	_, err := DecodeOutput(`{"findings":[{"evidence":"sk-invalidjson-1234567890abcdef"}`)
	if err == nil {
		t.Fatal("expected decode error")
	}
	if strings.Contains(err.Error(), "sk-invalidjson-1234567890abcdef") {
		t.Fatalf("decode error leaked secret: %v", err)
	}
}

func TestModelReviewSystemPromptDefinesStrictContract(t *testing.T) {
	req := InputRequest(Input{})
	if len(req.Messages) == 0 {
		t.Fatal("expected system prompt")
	}
	prompt := req.Messages[0].Content
	for _, want := range []string{
		"only return a JSON object",
		"do not return markdown",
		`"findings"`,
		"severity",
		"confidence",
		"high, medium, or low",
		"do not duplicate existing_findings",
		"Only report incremental semantic value",
		"cross-file",
		"business logic",
		"Return an empty findings array",
		"Do not output secrets",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestRunReviewNeverSendsMultilinePEMToProvider(t *testing.T) {
	const payload = "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC-multiline-test-payload"
	diff := []byte("diff --git a/config.go b/config.go\n" +
		"+++ b/config.go\n" +
		"@@ -0,0 +1,4 @@\n" +
		"+private_key=-----BEGIN PRIVATE KEY-----\n" +
		"+" + payload + "\n" +
		"+-----END PRIVATE KEY-----\n")
	called := false
	provider := ProviderFunc(func(_ context.Context, input Input) (Output, error) {
		called = true
		for _, secret := range []string{"-----BEGIN PRIVATE KEY-----", payload, "-----END PRIVATE KEY-----"} {
			if strings.Contains(input.DiffSummary, secret) {
				t.Fatalf("provider input leaked %q: %s", secret, input.DiffSummary)
			}
		}
		if !strings.Contains(input.DiffSummary, "[REDACTED_PRIVATE_KEY]") {
			t.Fatalf("provider input missing PEM redaction marker: %s", input.DiffSummary)
		}
		return Output{}, nil
	})

	_, _ = RunReview(context.Background(), "task-1", provider, Audit{}, review.Result{}, diff, review.InputMetadata{})
	if !called {
		t.Fatal("expected model provider to receive a review request")
	}
}

func TestRunReviewAndFindingSanitizationRedactShortDeclarationSecrets(t *testing.T) {
	const secret = "llm-live-short-declaration"
	diff := []byte("diff --git a/config.go b/config.go\n" +
		"+++ b/config.go\n" +
		"@@ -0,0 +1 @@\n" +
		"+apiKey := \"" + secret + "\"\n")
	called := false
	provider := ProviderFunc(func(_ context.Context, input Input) (Output, error) {
		called = true
		if strings.Contains(input.DiffSummary, secret) {
			t.Fatalf("provider input leaked short-declaration secret: %s", input.DiffSummary)
		}
		if !strings.Contains(input.DiffSummary, "apiKey=[REDACTED]") {
			t.Fatalf("provider input missing short-declaration redaction marker: %s", input.DiffSummary)
		}
		return Output{}, nil
	})

	_, _ = RunReview(context.Background(), "task-1", provider, Audit{}, review.Result{}, diff, review.InputMetadata{})
	if !called {
		t.Fatal("expected model provider to receive a review request")
	}

	finding := SanitizeFinding(review.Finding{Evidence: "apiKey := \"" + secret + "\""})
	if strings.Contains(finding.Evidence, secret) {
		t.Fatalf("report evidence leaked short-declaration secret: %s", finding.Evidence)
	}
}

func TestRunReviewKeepsOnlyProviderFindingsOnAddedLines(t *testing.T) {
	const secret = "sk-provider-linecheck-1234567890"
	diff := []byte("diff --git a/main.go b/main.go\n" +
		"--- a/main.go\n" +
		"+++ b/main.go\n" +
		"@@ -1,2 +1,5 @@\n" +
		" package main\n" +
		" \n" +
		"+func run() {\n" +
		"+\tprintln(\"ok\")\n" +
		"+}\n")
	provider := ProviderFunc(func(_ context.Context, input Input) (Output, error) {
		_ = input
		return Output{Findings: []review.Finding{
			{
				Severity:       "medium",
				Category:       "logic",
				File:           "b/main.go",
				Line:           4,
				Title:          "Valid semantic risk " + secret,
				Evidence:       "provider saw " + secret,
				Recommendation: "review " + secret,
				Confidence:     "high",
				Source:         "model",
				RuleID:         "valid-model-finding",
			},
			{
				Severity:       "medium",
				Category:       "logic",
				File:           "main.go",
				Line:           99,
				Title:          "Off-diff line",
				Evidence:       "should be dropped",
				Recommendation: "drop it",
				Confidence:     "high",
				Source:         "model",
				RuleID:         "off-diff-line",
			},
			{
				Severity:       "medium",
				Category:       "logic",
				File:           "other.go",
				Line:           4,
				Title:          "Wrong file",
				Evidence:       "should be dropped",
				Recommendation: "drop it",
				Confidence:     "high",
				Source:         "model",
				RuleID:         "wrong-file",
			},
		}}, nil
	})

	result, summary := RunReview(context.Background(), "task-1", provider, Audit{}, review.Result{}, diff, review.InputMetadata{})
	if len(result.Findings) != 1 {
		t.Fatalf("expected exactly one validated model finding, got %+v", result.Findings)
	}
	if finding := result.Findings[0]; finding.RuleID != "valid-model-finding" {
		t.Fatalf("expected valid finding to survive validation, got %+v", finding)
	} else {
		for _, field := range []string{finding.Title, finding.Evidence, finding.Recommendation} {
			if strings.Contains(field, secret) {
				t.Fatalf("provider-controlled result field leaked secret after sanitization: %+v", finding)
			}
		}
	}
	if summary.FindingCount != 1 {
		t.Fatalf("expected summary to count only validated provider findings, got %+v", summary)
	}
	if !hasRuleID(result.Warnings, "model-invalid-anchor") {
		t.Fatalf("expected invalid provider anchors to produce an audit warning, got %+v", result.Warnings)
	}
}

func TestNormalizeFindingSanitizesAndBoundsProviderStrings(t *testing.T) {
	const secret = "sk-provider-bounds-1234567890"
	finding := NormalizeFinding(review.Finding{
		Severity:       "  medium  ",
		Category:       strings.Repeat("category-", 20) + secret,
		File:           " b/secret.go ",
		Title:          strings.Repeat("title-", 60) + secret,
		Evidence:       "evidence " + secret + strings.Repeat("x", 3000),
		Recommendation: "recommendation " + secret + strings.Repeat("y", 1000),
		Confidence:     " high ",
		Source:         "custom-provider",
		RuleID:         "rule-" + secret + strings.Repeat("z", 200),
		Status:         " finding ",
	})

	if finding.File != "secret.go" {
		t.Fatalf("expected normalized file path, got %+v", finding)
	}
	for name, value := range map[string]string{
		"category":       finding.Category,
		"title":          finding.Title,
		"evidence":       finding.Evidence,
		"recommendation": finding.Recommendation,
		"rule_id":        finding.RuleID,
	} {
		if strings.Contains(value, secret) {
			t.Fatalf("%s leaked provider secret after normalization: %+v", name, finding)
		}
	}
	if len([]rune(finding.Category)) > maxFindingCategoryLen ||
		len([]rune(finding.Title)) > maxFindingTitleLen ||
		len([]rune(finding.Evidence)) > maxFindingEvidenceLen ||
		len([]rune(finding.Recommendation)) > maxFindingRecommendationLen ||
		len([]rune(finding.RuleID)) > maxFindingRuleIDLen {
		t.Fatalf("expected normalized finding strings to be bounded, got lengths category=%d title=%d evidence=%d recommendation=%d rule=%d",
			len([]rune(finding.Category)),
			len([]rune(finding.Title)),
			len([]rune(finding.Evidence)),
			len([]rune(finding.Recommendation)),
			len([]rune(finding.RuleID)),
		)
	}
	if finding.Severity != "medium" || finding.Confidence != "high" || finding.Status != "finding" {
		t.Fatalf("expected normalized enums, got %+v", finding)
	}
	invalid := NormalizeFinding(review.Finding{Severity: "critical-secret", Confidence: "certain", Status: "published"})
	if invalid.Severity != "low" || invalid.Confidence != "low" || invalid.Status != "finding" {
		t.Fatalf("invalid provider enums must fall back safely, got %+v", invalid)
	}
}

func hasRuleID(findings []review.Finding, ruleID string) bool {
	for _, finding := range findings {
		if finding.RuleID == ruleID {
			return true
		}
	}
	return false
}
