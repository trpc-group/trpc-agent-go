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

func qualifiableOperation(operation *Operation) bool {
	return operation != nil &&
		(operation.Type == OperationAdd || operation.Type == OperationUpdate) &&
		strings.TrimSpace(operation.Memory) != ""
}

func qualifyOperationWithGroundedTopic(source string, operation *Operation) {
	if !qualifiableOperation(operation) || len(operation.Topics) < 2 {
		return
	}

	anchoredTopics := make([]string, 0, len(operation.Topics))
	for _, topic := range operation.Topics {
		if containsTopic(operation.Memory, topic) {
			anchoredTopics = append(anchoredTopics, topic)
		}
	}
	if len(anchoredTopics) == 0 {
		return
	}

	var selected string
	selectedPriority := -1
	for _, topic := range operation.Topics {
		topic = strings.TrimSpace(topic)
		if topic == "" || containsTopic(operation.Memory, topic) ||
			!containsTopic(source, topic) {
			continue
		}
		priority := groundedTopicPriority(source, topic, anchoredTopics)
		if priority <= 0 {
			continue
		}
		if selected == "" || priority > selectedPriority {
			selected = topic
			selectedPriority = priority
		}
	}
	if selected != "" {
		operation.Memory = selected + ": " +
			strings.TrimSpace(operation.Memory)
	}
}

func groundedTopicPriority(
	source, topic string,
	anchoredTopics []string,
) int {
	// Exact-case capitalization is a conservative named-entity signal and
	// outranks sentence-local generic context. Equal priorities preserve the
	// extractor's topic order.
	if !containsTopic(source, topic) {
		return 0
	}
	for _, r := range topic {
		if strings.Contains(source, topic) && unicode.IsUpper(r) {
			return 2
		}
	}
	if !containsNonASCII(topic) && topicSharesSourceSegment(
		source, topic, anchoredTopics,
	) {
		return 1
	}
	return 0
}

func topicSharesSourceSegment(
	source, topic string,
	anchoredTopics []string,
) bool {
	segments := strings.FieldsFunc(source, func(r rune) bool {
		switch r {
		case '\n', '.', '?', '!', ';', '\u3002', '\uff1f', '\uff01':
			return true
		default:
			return false
		}
	})
	for _, segment := range segments {
		if !containsTopic(segment, topic) {
			continue
		}
		for _, anchoredTopic := range anchoredTopics {
			if containsTopic(segment, anchoredTopic) {
				return true
			}
		}
	}
	return false
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
