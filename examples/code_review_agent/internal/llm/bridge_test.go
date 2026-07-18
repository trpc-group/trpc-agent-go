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

func hasRuleID(findings []review.Finding, ruleID string) bool {
	for _, finding := range findings {
		if finding.RuleID == ruleID {
			return true
		}
	}
	return false
}
