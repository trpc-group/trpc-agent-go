//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"strings"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func qualifyOperationsWithGroundedTopics(
	source string,
	operations []*Operation,
) {
	for _, operation := range operations {
		qualifyOperationWithGroundedTopic(source, operation)
	}
}

func qualifyOperationWithGroundedTopic(source string, operation *Operation) {
	if operation == nil ||
		(operation.Type != OperationAdd && operation.Type != OperationUpdate) ||
		strings.TrimSpace(operation.Memory) == "" || len(operation.Topics) < 2 {
		return
	}

	anchored := false
	for _, topic := range operation.Topics {
		if containsTopic(operation.Memory, topic) {
			anchored = true
			break
		}
	}
	if !anchored {
		return
	}

	for _, topic := range operation.Topics {
		topic = strings.TrimSpace(topic)
		if topic == "" || containsTopic(operation.Memory, topic) ||
			!containsTopic(source, topic) {
			continue
		}
		operation.Memory = topic + ": " + strings.TrimSpace(operation.Memory)
		return
	}
}

func conversationSourceText(messages []model.Message) string {
	var source strings.Builder
	for _, message := range messages {
		if (message.Role != model.RoleUser &&
			message.Role != model.RoleAssistant) ||
			message.ToolID != "" || len(message.ToolCalls) > 0 {
			continue
		}
		appendSourceText(&source, message.Content)
		for _, part := range message.ContentParts {
			if part.Type == model.ContentTypeText && part.Text != nil {
				appendSourceText(&source, *part.Text)
			}
		}
	}
	return source.String()
}

func appendSourceText(source *strings.Builder, text string) {
	if text = strings.TrimSpace(text); text != "" {
		source.WriteString(text)
		source.WriteByte('\n')
	}
}

func containsTopic(text, topic string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	topic = strings.ToLower(strings.TrimSpace(topic))
	if text == "" || topic == "" {
		return false
	}
	if containsNonASCII(topic) {
		return strings.Contains(text, topic)
	}
	text = normalizeTopicText(text)
	topic = normalizeTopicText(topic)
	return topic != "" && strings.Contains(" "+text+" ", " "+topic+" ")
}

func normalizeTopicText(value string) string {
	var normalized strings.Builder
	spacePending := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if spacePending && normalized.Len() > 0 {
				normalized.WriteByte(' ')
			}
			normalized.WriteRune(unicode.ToLower(r))
			spacePending = false
			continue
		}
		spacePending = normalized.Len() > 0
	}
	return normalized.String()
}

func containsNonASCII(value string) bool {
	for _, r := range value {
		if r > unicode.MaxASCII {
			return true
		}
	}
	return false
}
