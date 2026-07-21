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
	"trpc.group/trpc-go/trpc-agent-go/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const reviewerRedactedValue = redact.Value

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
	return redact.SensitiveText(text)
}

func redactedStructuredValue(raw string) string {
	return redact.StructuredValue(raw)
}

func hasWrappedQuotes(value string, quote byte) bool {
	return redact.HasWrappedQuotes(value, quote)
}

func isReviewerSensitiveName(name string) bool {
	return redact.IsSensitiveName(name)
}
