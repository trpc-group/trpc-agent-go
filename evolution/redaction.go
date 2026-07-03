//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evolution

import (
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const reviewerRedactedValue = "[REDACTED]"

var (
	reviewerSensitiveNamePattern = regexp.MustCompile(
		`(?i)\b[A-Z0-9_]*(TOKEN|SECRET|PASSWORD|PASSWD|API_KEY|ACCESS_KEY|PRIVATE_KEY)\b[A-Z0-9_]*`,
	)
	reviewerAssignmentPattern = regexp.MustCompile(
		`(?im)\b([A-Za-z_][A-Za-z0-9_]*)(\s*=\s*)(\"[^\"]*\"|'[^']*'|[^\s,;]+)`,
	)
	reviewerColonPattern = regexp.MustCompile(
		`(?im)(["']?)([A-Za-z_][A-Za-z0-9_]*)(["']?\s*:\s*)(\"[^\"]*\"|'[^']*'|[^,\s}\]]+)`,
	)
	reviewerSensitiveFlagPattern = regexp.MustCompile(
		`(?i)(--(?:api-key|token|secret|password)\s+)(\"[^\"]*\"|'[^']*'|[^\s]+)`,
	)
	reviewerAuthorizationHeaderPattern = regexp.MustCompile(
		`(?i)(authorization\s*:\s*bearer\s+)([^\s,;]+)`,
	)
	reviewerAuthorizationFieldPattern = regexp.MustCompile(
		`(?im)(["']?authorization["']?\s*:\s*)(\"[^\"]*\"|'[^']*'|[^,\s}\]]+)`,
	)
	reviewerBearerTokenPattern = regexp.MustCompile(
		`(?i)(\bbearer\s+)([A-Za-z0-9._~+/-]{12,}=*)`,
	)
	reviewerOpenAIKeyPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`)
	reviewerJWTPattern       = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
)

func sanitizeReviewInput(in *ReviewInput) *ReviewInput {
	if in == nil {
		return nil
	}
	out := *in
	out.Messages = sanitizeModelMessages(in.Messages)
	out.Transcript = sanitizeReviewMessages(in.Transcript)
	out.ExistingSkills = sanitizeExistingSkills(in.ExistingSkills)
	out.Outcome = sanitizeOutcome(in.Outcome)
	return &out
}

func sanitizeModelMessages(in []model.Message) []model.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]model.Message, 0, len(in))
	for _, msg := range in {
		cp := msg
		cp.Content = redactSensitiveText(msg.Content)
		cp.ReasoningContent = redactSensitiveText(msg.ReasoningContent)
		if len(msg.ContentParts) > 0 {
			cp.ContentParts = make([]model.ContentPart, len(msg.ContentParts))
			for i, part := range msg.ContentParts {
				partCopy := part
				if part.Text != nil {
					text := redactSensitiveText(*part.Text)
					partCopy.Text = &text
				}
				cp.ContentParts[i] = partCopy
			}
		}
		if len(msg.ToolCalls) > 0 {
			cp.ToolCalls = make([]model.ToolCall, len(msg.ToolCalls))
			for i, call := range msg.ToolCalls {
				callCopy := call
				if len(call.Function.Arguments) > 0 {
					callCopy.Function.Arguments = []byte(
						redactSensitiveText(string(call.Function.Arguments)),
					)
				}
				cp.ToolCalls[i] = callCopy
			}
		}
		out = append(out, cp)
	}
	return out
}

func sanitizeReviewMessages(in []ReviewMessage) []ReviewMessage {
	if len(in) == 0 {
		return nil
	}
	out := make([]ReviewMessage, 0, len(in))
	for _, msg := range in {
		cp := msg
		cp.Content = redactSensitiveText(msg.Content)
		if len(msg.ToolCalls) > 0 {
			cp.ToolCalls = make([]ReviewToolCall, len(msg.ToolCalls))
			for i, call := range msg.ToolCalls {
				callCopy := call
				callCopy.Arguments = redactSensitiveText(call.Arguments)
				cp.ToolCalls[i] = callCopy
			}
		}
		out = append(out, cp)
	}
	return out
}

func sanitizeExistingSkills(in []ExistingSkill) []ExistingSkill {
	if len(in) == 0 {
		return nil
	}
	out := make([]ExistingSkill, 0, len(in))
	for _, skill := range in {
		cp := skill
		cp.Description = redactSensitiveText(skill.Description)
		cp.BodyExcerpt = redactSensitiveText(skill.BodyExcerpt)
		out = append(out, cp)
	}
	return out
}

func sanitizeOutcome(in *Outcome) *Outcome {
	if in == nil {
		return nil
	}
	out := *in
	out.Notes = redactSensitiveText(in.Notes)
	return &out
}

func redactSensitiveText(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	redacted := reviewerAuthorizationHeaderPattern.ReplaceAllString(
		text,
		`${1}`+reviewerRedactedValue,
	)
	redacted = reviewerAuthorizationFieldPattern.ReplaceAllStringFunc(
		redacted,
		redactAuthorizationFieldMatch,
	)
	redacted = reviewerBearerTokenPattern.ReplaceAllString(
		redacted,
		`${1}`+reviewerRedactedValue,
	)
	redacted = reviewerAssignmentPattern.ReplaceAllStringFunc(
		redacted,
		redactAssignmentMatch,
	)
	redacted = reviewerColonPattern.ReplaceAllStringFunc(
		redacted,
		redactColonMatch,
	)
	redacted = reviewerSensitiveFlagPattern.ReplaceAllString(
		redacted,
		`${1}`+reviewerRedactedValue,
	)
	redacted = reviewerOpenAIKeyPattern.ReplaceAllString(
		redacted,
		reviewerRedactedValue,
	)
	redacted = reviewerJWTPattern.ReplaceAllString(
		redacted,
		reviewerRedactedValue,
	)
	return redacted
}

func redactAuthorizationFieldMatch(match string) string {
	parts := reviewerAuthorizationFieldPattern.FindStringSubmatch(match)
	if len(parts) != 3 {
		return match
	}
	return parts[1] + redactedStructuredValue(parts[2])
}

func redactAssignmentMatch(match string) string {
	parts := reviewerAssignmentPattern.FindStringSubmatch(match)
	if len(parts) != 4 || !isReviewerSensitiveName(parts[1]) {
		return match
	}
	return parts[1] + parts[2] + redactedStructuredValue(parts[3])
}

func redactColonMatch(match string) string {
	parts := reviewerColonPattern.FindStringSubmatch(match)
	if len(parts) != 5 || !isReviewerSensitiveName(parts[2]) {
		return match
	}
	return parts[1] + parts[2] + parts[3] + redactedStructuredValue(parts[4])
}

func redactedStructuredValue(raw string) string {
	trimmedRight := strings.TrimRight(raw, " \t")
	suffix := raw[len(trimmedRight):]
	body := trimmedRight
	trailing := ""

	if strings.HasSuffix(body, ",") {
		body = strings.TrimSuffix(body, ",")
		trailing = ","
	}

	switch {
	case hasWrappedQuotes(body, '"'):
		return `"` + reviewerRedactedValue + `"` + trailing + suffix
	case hasWrappedQuotes(body, '\''):
		return `'` + reviewerRedactedValue + `'` + trailing + suffix
	default:
		return reviewerRedactedValue + trailing + suffix
	}
}

func hasWrappedQuotes(value string, quote byte) bool {
	if len(value) < 2 {
		return false
	}
	return value[0] == quote && value[len(value)-1] == quote
}

func isReviewerSensitiveName(name string) bool {
	return reviewerSensitiveNamePattern.MatchString(name)
}
