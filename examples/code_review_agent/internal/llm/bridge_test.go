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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	agentmodel "trpc.group/trpc-go/trpc-agent-go/model"
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

func TestOfficialProviderBoundsStreamingResponse(t *testing.T) {
	model := &streamingModel{chunks: make([]string, 66), canceled: make(chan struct{})}
	for i := range model.chunks {
		model.chunks[i] = strings.Repeat("x", 1024)
	}
	provider := OfficialProvider{Model: model}

	_, err := provider.Review(context.Background(), Input{})
	if !errors.Is(err, ErrOfficialModelResponseTooLarge) {
		t.Fatalf("Review error = %v, want bounded-response error", err)
	}
	if strings.Contains(err.Error(), model.chunks[0]) {
		t.Fatalf("bounded-response error retained model payload: %v", err)
	}
	select {
	case <-model.canceled:
	case <-time.After(time.Second):
		t.Fatal("expected provider to cancel oversized model stream")
	}

	result, summary := RunReview(context.Background(), "task-oversized-model", provider, Audit{}, review.Result{}, []byte("diff --git a/main.go b/main.go\n+++ b/main.go\n@@ -0,0 +1 @@\n+package main\n"), review.InputMetadata{})
	if summary.ExceptionCount != 1 || !hasRuleID(result.Warnings, "model-provider-failed") {
		t.Fatalf("oversized provider result was not recorded as model exception: result=%+v summary=%+v", result, summary)
	}
	for _, item := range result.Warnings {
		if strings.Contains(item.Evidence, model.chunks[0]) {
			t.Fatalf("model exception retained oversized payload: %+v", item)
		}
	}
}

type streamingModel struct {
	chunks   []string
	canceled chan struct{}
}

func (m *streamingModel) GenerateContent(ctx context.Context, _ *agentmodel.Request) (<-chan *agentmodel.Response, error) {
	responses := make(chan *agentmodel.Response)
	go func() {
		defer close(responses)
		for _, chunk := range m.chunks {
			select {
			case responses <- &agentmodel.Response{Choices: []agentmodel.Choice{{Delta: agentmodel.Message{Content: chunk}}}}:
			case <-ctx.Done():
				select {
				case <-m.canceled:
				default:
					close(m.canceled)
				}
				return
			}
		}
	}()
	return responses, nil
}

func (*streamingModel) Info() agentmodel.Info {
	return agentmodel.Info{Name: "streaming-test-model"}
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

func TestSanitizeInputRedactsAndBoundsMetadata(t *testing.T) {
	const secret = "sk-metadata-redaction-1234567890"
	input := Input{InputMetadata: review.InputMetadata{
		ChangedGoFiles:   []string{"\"config-" + secret + ".go\""},
		PackageNames:     []string{"pkg-" + secret},
		ModulePath:       "example.com/" + secret,
		BaseRef:          "base-" + secret,
		HeadRef:          "head-" + secret,
		TouchedTestFiles: []string{"test-" + secret + ".go"},
	}}
	encoded, err := json.Marshal(SanitizeInput(input))
	if err != nil {
		t.Fatalf("marshal sanitized input: %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("input metadata leaked secret: %s", encoded)
	}
	if !strings.Contains(string(encoded), "[REDACTED]") {
		t.Fatalf("input metadata missing redaction marker: %s", encoded)
	}

	// The generic HTTP provider must serialize only the sanitized request body.
	var httpRequestBody string
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read request body: %v", readErr)
		}
		httpRequestBody = string(body)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"findings":[]}`)), Header: make(http.Header)}, nil
	})}
	provider, err := NewHTTPProvider(HTTPConfig{Enabled: true, Endpoint: "https://provider.example.test/review", Client: client})
	if err != nil {
		t.Fatalf("NewHTTPProvider: %v", err)
	}
	if _, err := provider.Review(context.Background(), input); err != nil {
		t.Fatalf("generic HTTP review: %v", err)
	}
	if strings.Contains(httpRequestBody, secret) || !strings.Contains(httpRequestBody, "[REDACTED]") {
		t.Fatalf("generic HTTP request did not sanitize metadata: %s", httpRequestBody)
	}

	// Official model requests use the same sanitized JSON payload in the user message.
	officialRequest := InputRequest(input)
	if len(officialRequest.Messages) < 2 || strings.Contains(officialRequest.Messages[1].Content, secret) || !strings.Contains(officialRequest.Messages[1].Content, "[REDACTED]") {
		t.Fatalf("official model request did not sanitize metadata: %+v", officialRequest.Messages)
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

func TestNormalizeFindingPreservesRepositoryPathsStartingWithSidePrefixes(t *testing.T) {
	for _, path := range []string{"a/handler.go", "b/handler.go"} {
		finding := NormalizeFinding(review.Finding{File: "b/" + path, Line: 1})
		if finding.File != path {
			t.Errorf("normalized file = %q, want %q", finding.File, path)
		}
	}
}

func TestSanitizeFindingRedactsEveryProviderControlledString(t *testing.T) {
	const secret = "sk-provider-all-fields-1234567890"
	finding := SanitizeFinding(review.Finding{
		Severity: secret, Category: secret, File: secret, Title: secret,
		Evidence: secret, Recommendation: secret, Confidence: secret,
		RuleID: secret, Status: secret,
	})
	for name, value := range map[string]string{
		"severity": finding.Severity, "category": finding.Category, "file": finding.File,
		"title": finding.Title, "evidence": finding.Evidence, "recommendation": finding.Recommendation,
		"confidence": finding.Confidence, "rule_id": finding.RuleID, "status": finding.Status,
	} {
		if strings.Contains(value, secret) {
			t.Fatalf("%s leaked provider secret: %+v", name, finding)
		}
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
