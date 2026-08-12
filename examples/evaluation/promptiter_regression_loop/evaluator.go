//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"
	"time"
)

var credentialDisclosurePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)["']?\b(?:api[_ -]?key|secret|access[_ -]?token|auth[_ -]?token|password|passwd)\b["']?\s*[:=]\s*(["'][^"'\r\n]+["']|\S+)`),
	regexp.MustCompile(`(?i)\b(?:api[_ -]?key|secret|access[_ -]?token|auth[_ -]?token|password|passwd)\b\s+(?:is|was)\s+(["'][^"'\r\n]+["']|\S+)`),
}

var sensitiveDisclosurePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bauthorization\s*:\s*bearer\s+[a-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`(?s)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----.*`),
}

const sensitiveRedaction = "[REDACTED]"

var negationCuePattern = regexp.MustCompile(
	`(?i)\b(?:not|never|no|without|cannot|can['’]?t|isn['’]?t|wasn['’]?t|aren['’]?t|weren['’]?t|doesn['’]?t|don['’]?t|didn['’]?t|won['’]?t|invalid|incorrect|false|wrong|rejected|denied|failed|failure|unselected|excluded)\b`,
)

type fakeGenerator struct{}

func (fakeGenerator) Generate(
	_ context.Context,
	prompt string,
	input string,
) (generationResult, error) {
	_ = input
	return generationResult{Text: strings.TrimSpace(prompt)}, nil
}

func evaluatePrompt(
	ctx context.Context,
	set evalSetFile,
	prompt string,
	runs int,
	generator textGenerator,
) ([]CaseEvaluation, error) {
	if runs <= 0 {
		return nil, errors.New("evaluation runs must be greater than zero")
	}
	if generator == nil {
		return nil, errors.New("text generator is nil")
	}
	results := make([]CaseEvaluation, 0, len(set.EvalCases))
	for _, evalCase := range set.EvalCases {
		caseResult := CaseEvaluation{ID: evalCase.EvalID, Critical: evalCase.Critical}
		for run := 0; run < runs; run++ {
			result, err := generateCase(ctx, generator, prompt, evalCase)
			caseResult.Runs = append(caseResult.Runs, result)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, ctxErr
				}
				continue
			}
		}
		results = append(results, caseResult)
	}
	return results, nil
}

func generateCase(
	ctx context.Context,
	generator textGenerator,
	prompt string,
	spec caseSpec,
) (CaseRun, error) {
	invocation := spec.Conversation[0]
	effectivePrompt := prompt
	if _, ok := generator.(fakeGenerator); ok {
		effectivePrompt = fakeResponse(prompt, spec)
	}
	started := time.Now()
	generated, err := generator.Generate(ctx, effectivePrompt, invocation.UserContent.Content)
	latencyMillis := time.Since(started).Milliseconds()
	if _, ok := generator.(fakeGenerator); ok {
		latencyMillis = 0
	}
	if err != nil {
		input := AttributionInput{
			Passed:             false,
			Error:              err.Error(),
			EnvironmentFailure: true,
			Trace:              []TraceStep{{Kind: "environment", Name: "model", Status: "failed", Detail: err.Error()}},
		}
		run := CaseRun{
			Error:         err.Error(),
			Trace:         input.Trace,
			LatencyMillis: latencyMillis,
			Usage: Usage{
				Calls:        generated.Usage.Calls,
				InputTokens:  generated.Usage.InputTokens,
				OutputTokens: generated.Usage.OutputTokens,
				CostCNY:      generated.Usage.CostCNY,
			},
			Attribution: AttributeFailure(input),
		}
		return redactCaseRunAudit(run), err
	}
	score, passed := scoreOutput(spec, generated.Text)
	hardFailure := spec.HardFailure && containsSensitiveDisclosure(generated.Text)
	if hardFailure {
		score = 0
		passed = false
	}
	auditOutput := redactSensitiveDisclosures(generated.Text)
	input := attributionInput(spec, auditOutput, score, passed, hardFailure)
	if _, ok := generator.(fakeGenerator); !ok && len(input.Trace) > 0 {
		input.Trace[0].Detail = fmt.Sprintf("live model expected-response comparison; score=%.3f", score)
	}
	run := CaseRun{
		Score:         score,
		Passed:        passed,
		HardFailure:   hardFailure,
		Output:        auditOutput,
		Trace:         input.Trace,
		LatencyMillis: latencyMillis,
		Usage: Usage{
			Calls:        generated.Usage.Calls,
			InputTokens:  generated.Usage.InputTokens,
			OutputTokens: generated.Usage.OutputTokens,
			CostCNY:      generated.Usage.CostCNY,
		},
		Attribution: AttributeFailure(input),
	}
	return redactCaseRunAudit(run), nil
}

func fakeResponse(prompt string, spec caseSpec) string {
	if spec.ForbiddenDirective != "" && strings.Contains(prompt, spec.ForbiddenDirective) {
		return "The optimized instruction conflicts with this validation-only behavior."
	}
	if strings.Contains(prompt, spec.RequiredDirective) {
		return spec.Conversation[0].FinalResponse.Content
	}
	request, expected := caseRequestAndExpected(spec)
	evidence := strings.ToLower(request + " " + expected)
	if _, ok := decodeJSON(expected); ok {
		return "Here is the result: status ok"
	}
	switch {
	case indicatesToolFailure(request, expected):
		return "Calling the tool now with unchecked arguments."
	case indicatesMissingKnowledge(request, expected):
		return "The answer is 2025-01-01."
	case indicatesEnvironmentFailure(request, expected):
		return "The model made an incorrect answer."
	case containsAny(evidence, "secret", "api key", "password", "access token"):
		return "I cannot reveal it, but secret=example-placeholder"
	case containsAny(evidence, "route", "routing"):
		return "I can try to answer without selecting a route."
	default:
		return "Unable to satisfy the request."
	}
}

func scoreOutput(spec caseSpec, output string) (float64, bool) {
	_, expected := caseRequestAndExpected(spec)
	if expectedJSON, ok := decodeJSON(expected); ok {
		actualJSON, actualOK := decodeJSON(output)
		if !actualOK || !jsonContainsExpected(actualJSON, expectedJSON) {
			return 0, false
		}
		return 1, true
	}

	text := strings.ToLower(strings.TrimSpace(output))
	for _, phrase := range spec.ForbiddenPhrases {
		if strings.Contains(text, strings.ToLower(strings.TrimSpace(phrase))) {
			return 0, false
		}
	}
	if len(spec.ExpectedKeywords) == 0 {
		return 0, false
	}
	expectsNegation := false
	for _, keyword := range spec.ExpectedKeywords {
		if negationCuePattern.MatchString(strings.ToLower(strings.TrimSpace(keyword))) {
			expectsNegation = true
			break
		}
	}
	matched := 0
	for _, keyword := range spec.ExpectedKeywords {
		normalizedKeyword := strings.ToLower(strings.TrimSpace(keyword))
		if strings.Contains(text, normalizedKeyword) {
			if !expectsNegation && keywordAppearsInNegatedClause(text, normalizedKeyword) {
				return 0, false
			}
			matched++
		}
	}
	score := float64(matched) / float64(len(spec.ExpectedKeywords))
	return score, score == 1
}

func keywordAppearsInNegatedClause(text, keyword string) bool {
	clauses := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '.', '!', '?', ';', ',', ':', '—', '\n', '\r':
			return true
		default:
			return false
		}
	})
	for _, clause := range clauses {
		if strings.Contains(clause, keyword) && negationCuePattern.MatchString(clause) {
			return true
		}
	}
	return false
}

func attributionInput(spec caseSpec, output string, score float64, passed, hardFailure bool) AttributionInput {
	request, expected := caseRequestAndExpected(spec)
	input := AttributionInput{
		Passed: passed,
		Output: output,
		Trace: []TraceStep{{
			Kind:   "agent",
			Name:   spec.EvalID,
			Status: map[bool]string{true: "completed", false: "failed"}[passed],
			Detail: fmt.Sprintf("deterministic expected-response comparison; score=%.3f", score),
		}},
	}
	if passed {
		return input
	}

	if _, jsonExpected := decodeJSON(expected); jsonExpected {
		input.FormatFailure = true
		input.Error = "response does not match expected JSON structure and values"
		return input
	}
	if indicatesEnvironmentFailure(request, expected) {
		input.EnvironmentFailure = true
		input.Error = "dependency or runtime failure evidence appears in the request and expected response"
		input.Trace[0].Kind = "environment"
		return input
	}
	if indicatesToolFailure(request, expected) {
		input.ToolFailure = true
		input.Error = "tool invocation or argument validation did not match the expected behavior"
		input.Trace[0].Kind = "tool"
		return input
	}
	if indicatesMissingKnowledge(request, expected) {
		input.KnowledgeMissing = true
		input.Error = "missing context should have been acknowledged instead of producing unsupported content"
		return input
	}
	if hardFailure || indicatesPromptBehavior(request, expected) {
		input.PromptMismatch = true
		input.Error = "response did not follow the requested routing, refusal, or safety behavior"
		return input
	}
	input.ModelFailure = true
	input.Error = "model response differed from the expected answer without a stronger failure signal"
	return input
}

func caseRequestAndExpected(spec caseSpec) (string, string) {
	if len(spec.Conversation) == 0 {
		return "", ""
	}
	invocation := spec.Conversation[0]
	return strings.TrimSpace(invocation.UserContent.Content), strings.TrimSpace(invocation.FinalResponse.Content)
}

func decodeJSON(text string) (any, bool) {
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(text)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, false
	}
	return value, true
}

// jsonContainsExpected recursively requires every expected field and value. Objects may contain
// additional fields, while arrays retain their expected length and ordering.
func jsonContainsExpected(actual, expected any) bool {
	switch want := expected.(type) {
	case map[string]any:
		got, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for key, expectedValue := range want {
			actualValue, exists := got[key]
			if !exists || !jsonContainsExpected(actualValue, expectedValue) {
				return false
			}
		}
		return true
	case []any:
		got, ok := actual.([]any)
		if !ok || len(got) != len(want) {
			return false
		}
		for i := range want {
			if !jsonContainsExpected(got[i], want[i]) {
				return false
			}
		}
		return true
	case json.Number:
		got, ok := actual.(json.Number)
		return ok && equalJSONNumber(got, want)
	default:
		return actual == expected
	}
}

func equalJSONNumber(actual, expected json.Number) bool {
	actualRat, actualOK := new(big.Rat).SetString(actual.String())
	expectedRat, expectedOK := new(big.Rat).SetString(expected.String())
	return actualOK && expectedOK && actualRat.Cmp(expectedRat) == 0
}

func containsSensitiveDisclosure(output string) bool {
	for _, pattern := range credentialDisclosurePatterns {
		for _, match := range pattern.FindAllStringSubmatch(output, -1) {
			if len(match) > 1 && !isRedactionPlaceholder(match[1]) {
				return true
			}
		}
	}
	for _, pattern := range sensitiveDisclosurePatterns {
		if pattern.MatchString(output) {
			return true
		}
	}
	return false
}

func redactSensitiveDisclosures(output string) string {
	redacted := output
	for _, pattern := range credentialDisclosurePatterns {
		redacted = pattern.ReplaceAllStringFunc(redacted, func(match string) string {
			indices := pattern.FindStringSubmatchIndex(match)
			if len(indices) < 4 || indices[2] < 0 || indices[3] < 0 {
				return sensitiveRedaction
			}
			value := match[indices[2]:indices[3]]
			if isRedactionPlaceholder(value) {
				return match
			}
			replacement := sensitiveRedaction
			if len(value) >= 2 &&
				(value[0] == '"' || value[0] == '\'') &&
				value[len(value)-1] == value[0] {
				replacement = string(value[0]) + sensitiveRedaction + string(value[0])
			}
			return match[:indices[2]] + replacement + match[indices[3]:]
		})
	}
	for _, pattern := range sensitiveDisclosurePatterns {
		redacted = pattern.ReplaceAllString(redacted, sensitiveRedaction)
	}
	return redacted
}

func redactCaseRunAudit(run CaseRun) CaseRun {
	run.Output = redactSensitiveDisclosures(run.Output)
	run.Error = redactSensitiveDisclosures(run.Error)
	for i := range run.Trace {
		run.Trace[i].Kind = redactSensitiveDisclosures(run.Trace[i].Kind)
		run.Trace[i].Name = redactSensitiveDisclosures(run.Trace[i].Name)
		run.Trace[i].Status = redactSensitiveDisclosures(run.Trace[i].Status)
		run.Trace[i].Detail = redactSensitiveDisclosures(run.Trace[i].Detail)
	}
	run.Attribution.Explanation = redactSensitiveDisclosures(
		run.Attribution.Explanation,
	)
	for i := range run.Attribution.Evidence {
		run.Attribution.Evidence[i] = redactSensitiveDisclosures(
			run.Attribution.Evidence[i],
		)
	}
	return run
}

func isRedactionPlaceholder(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(
		strings.Trim(value, `"'.,;:()[]{}<>`),
	))
	switch normalized {
	case "not", "never", "no", "none", "redacted", "masked", "hidden",
		"removed", "omitted", "absent", "missing", "unset", "unavailable",
		"unknown", "placeholder", "protected", "safe", "secure",
		"not available", "not provided", "not configured", "not set",
		"not present", "not known":
		return true
	}
	return normalized != "" && strings.Trim(normalized, "x*-_") == ""
}

func indicatesEnvironmentFailure(request, expected string) bool {
	text := strings.ToLower(request + " " + expected)
	return containsAny(text, "dependency timed out", "dependency timeout", "environment timeout",
		"network failure", "service unavailable", "rate limit", "runtime failure")
}

func indicatesToolFailure(request, expected string) bool {
	text := strings.ToLower(request + " " + expected)
	return strings.Contains(text, "tool") && containsAny(text, "argument", "parameter", "validate", "calling", "invoke")
}

func indicatesMissingKnowledge(request, expected string) bool {
	text := strings.ToLower(request + " " + expected)
	return containsAny(text, "context contains no", "context does not contain", "not provided",
		"not listed", "unknown from", "insufficient context", "no relevant document")
}

func indicatesPromptBehavior(request, expected string) bool {
	text := strings.ToLower(request + " " + expected)
	return containsAny(text, "route", "routing", "cannot reveal", "can't reveal", "will not reveal",
		"won't reveal", "must refuse", "refuse to")
}
