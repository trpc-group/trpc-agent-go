//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"strings"
)

// AttributeFailure classifies a failed run using explicit signals, trace state, and conservative
// keyword inference. The precedence favors actionable infrastructure causes over model blame.
func AttributeFailure(input AttributionInput) AttributionResult {
	if input.Passed {
		return attribution(FailureCategoryUnknown, 1, "the case passed; no failure attribution is needed", nil)
	}

	traceEvidence := failedTraceEvidence(input.Trace)
	text := strings.ToLower(strings.Join([]string{input.Error, input.Output, traceEvidence.text}, " "))

	if input.EnvironmentFailure || traceEvidence.environment || containsAny(text,
		"connection refused", "connection reset", "dns", "rate limit", "429", "service unavailable",
		"gateway timeout", "deadline exceeded", "network", "out of memory") {
		return attribution(FailureCategoryEnvironment, confidence(input.EnvironmentFailure),
			"runtime or external-service evidence indicates an environment failure", traceEvidence.items)
	}
	if input.ToolFailure || traceEvidence.tool {
		return attribution(FailureCategoryAgentTool, confidence(input.ToolFailure),
			"a failed agent or tool trace step prevented successful completion", traceEvidence.items)
	}
	if input.FormatFailure || containsAny(text,
		"invalid json", "json parse", "unmarshal", "schema validation", "invalid format",
		"malformed output", "missing required field", "expected json structure",
		"expected json", "json structure and values") {
		return attribution(FailureCategoryFormat, confidence(input.FormatFailure),
			"the response violated an explicit output-format or schema requirement", nonEmptyEvidence(input.Error))
	}
	if input.KnowledgeMissing || containsAny(text,
		"missing context", "knowledge not found", "retrieval returned no", "no relevant document",
		"insufficient context", "unknown fact") {
		return attribution(FailureCategoryKnowledge, confidence(input.KnowledgeMissing),
			"required knowledge was absent from the model context or retrieval result", nonEmptyEvidence(input.Error))
	}
	if input.PromptMismatch || containsAny(text,
		"ambiguous instruction", "conflicting instruction", "prompt missing", "instruction not followed",
		"ignored instruction", "underspecified prompt", "requested routing", "requested refusal",
		"requested routing, refusal, or safety behavior") {
		return attribution(FailureCategoryPrompt, confidence(input.PromptMismatch),
			"the prompt was ambiguous, incomplete, conflicting, or not followed", nonEmptyEvidence(input.Error))
	}
	if input.ModelFailure || containsAny(text,
		"model refusal", "refused to answer", "hallucination", "fabricated", "incorrect reasoning",
		"model returned empty", "model response differed from the expected answer") {
		return attribution(FailureCategoryModel, confidence(input.ModelFailure),
			"model behavior failed despite no stronger prompt, tool, knowledge, or environment signal", nonEmptyEvidence(input.Error))
	}
	return attribution(FailureCategoryUnknown, 0.25,
		"the available trace and error evidence is insufficient for a reliable attribution", nonEmptyEvidence(input.Error))
}

type traceAttributionEvidence struct {
	environment bool
	tool        bool
	text        string
	items       []string
}

func failedTraceEvidence(trace []TraceStep) traceAttributionEvidence {
	var result traceAttributionEvidence
	var texts []string
	for _, step := range trace {
		text := strings.TrimSpace(strings.Join([]string{step.Kind, step.Name, step.Status, step.Detail}, " "))
		texts = append(texts, text)
		if !isFailureStatus(step.Status) {
			continue
		}
		result.items = append(result.items, fmt.Sprintf("%s step %q ended with status %q: %s",
			step.Kind, step.Name, step.Status, step.Detail))
		switch strings.ToLower(strings.TrimSpace(step.Kind)) {
		case "environment", "runtime", "network", "dependency":
			result.environment = true
		case "tool", "orchestration":
			result.tool = true
		}
	}
	result.text = strings.Join(texts, " ")
	return result
}

func isFailureStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "error", "failed", "failure", "timeout", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func confidence(explicit bool) float64 {
	if explicit {
		return 0.95
	}
	return 0.75
}

func attribution(category FailureCategory, confidence float64, explanation string, evidence []string) AttributionResult {
	return AttributionResult{
		Category: category, Confidence: confidence, Explanation: explanation, Evidence: evidence,
	}
}

func nonEmptyEvidence(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}
