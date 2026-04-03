//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package messagemerger

import "trpc.group/trpc-go/trpc-agent-go/model"

func mergeConsecutiveMessages(
	messages []model.Message,
	separator string,
) []model.Message {
	if len(messages) == 0 {
		return messages
	}
	merged := make([]model.Message, 0, len(messages))
	for _, msg := range messages {
		if len(merged) == 0 {
			merged = append(merged, cloneMessage(msg))
			continue
		}
		last := merged[len(merged)-1]
		if !canMergeConsecutiveMessage(last) ||
			!canMergeConsecutiveMessage(msg) ||
			last.Role != msg.Role {
			merged = append(merged, cloneMessage(msg))
			continue
		}
		merged[len(merged)-1] = mergeMessage(last, msg, separator)
	}
	return merged
}

func canMergeConsecutiveMessage(msg model.Message) bool {
	if msg.ToolID != "" || msg.ToolName != "" {
		return false
	}
	switch msg.Role {
	case model.RoleSystem, model.RoleUser, model.RoleAssistant:
		return true
	default:
		return false
	}
}

func mergeMessage(
	dst model.Message,
	src model.Message,
	separator string,
) model.Message {
	dst.ReasoningContent = joinMessageText(
		dst.ReasoningContent,
		src.ReasoningContent,
		separator,
	)
	if len(dst.ContentParts) == 0 && len(src.ContentParts) == 0 {
		dst.Content = joinMessageText(dst.Content, src.Content, separator)
	} else {
		dst.ContentParts = mergeMessageContentParts(dst, src, separator)
		dst.Content = ""
	}
	if len(src.ToolCalls) > 0 {
		dst.ToolCalls = append(dst.ToolCalls, src.ToolCalls...)
	}
	return dst
}

func joinMessageText(first, second, separator string) string {
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	return first + separator + second
}

func cloneMessage(msg model.Message) model.Message {
	cloned := msg
	if len(msg.ContentParts) > 0 {
		cloned.ContentParts = append(
			[]model.ContentPart(nil),
			msg.ContentParts...,
		)
	}
	if len(msg.ToolCalls) > 0 {
		cloned.ToolCalls = append([]model.ToolCall(nil), msg.ToolCalls...)
	}
	return cloned
}

func mergeMessageContentParts(
	dst model.Message,
	src model.Message,
	separator string,
) []model.ContentPart {
	parts := orderedMessageContentParts(dst)
	if shouldInsertMessageSeparator(dst, src, separator) {
		parts = append(parts, textContentPart(separator))
	}
	return append(parts, orderedMessageContentParts(src)...)
}

func orderedMessageContentParts(msg model.Message) []model.ContentPart {
	parts := make([]model.ContentPart, 0, len(msg.ContentParts)+1)
	if msg.Content != "" {
		parts = append(parts, textContentPart(msg.Content))
	}
	return append(parts, msg.ContentParts...)
}

func shouldInsertMessageSeparator(
	dst model.Message,
	src model.Message,
	separator string,
) bool {
	if separator == "" {
		return false
	}
	return messageEndsWithText(dst) && messageStartsWithText(src)
}

func messageStartsWithText(msg model.Message) bool {
	if msg.Content != "" {
		return true
	}
	if len(msg.ContentParts) == 0 {
		return false
	}
	first := msg.ContentParts[0]
	return first.Type == model.ContentTypeText &&
		first.Text != nil &&
		*first.Text != ""
}

func messageEndsWithText(msg model.Message) bool {
	if len(msg.ContentParts) == 0 {
		return msg.Content != ""
	}
	last := msg.ContentParts[len(msg.ContentParts)-1]
	return last.Type == model.ContentTypeText &&
		last.Text != nil &&
		*last.Text != ""
}

func textContentPart(text string) model.ContentPart {
	return model.ContentPart{
		Type: model.ContentTypeText,
		Text: model.StringPtr(text),
	}
}
