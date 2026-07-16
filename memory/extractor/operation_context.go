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

const maxGroundedExampleContextBytes = 240

func qualifyOperationsWithGroundedTopics(
	source string,
	operations []*Operation,
) {
	preserveGroundedExampleRelations(source, operations)
	for _, operation := range operations {
		qualifyOperationWithGroundedTopic(source, operation)
	}
}

func preserveGroundedExampleRelations(source string, operations []*Operation) {
	for _, contextOperation := range operations {
		if !qualifiableOperation(contextOperation) ||
			len(contextOperation.Memory) > maxGroundedExampleContextBytes {
			continue
		}
		var entities []string
		for _, detail := range operations {
			if detail == contextOperation || !qualifiableOperation(detail) {
				continue
			}
			entity := groundedExampleEntity(
				source, contextOperation, detail,
			)
			if entity == "" || hasTopic(entities, entity) {
				continue
			}
			entities = append(entities, entity)
		}
		if len(entities) == 0 {
			continue
		}
		contextOperation.Memory = appendGroundedExamples(
			contextOperation.Memory, entities,
		)
		contextOperation.Topics = append(contextOperation.Topics, entities...)
	}
}

func appendGroundedExamples(context string, entities []string) string {
	context = strings.TrimRight(
		strings.TrimSpace(context), ".!?;。！？；",
	)
	if usesCJKPunctuation(context) {
		return context + "（包括" + strings.Join(entities, "、") + "）。"
	}
	return context + " (including " + strings.Join(entities, ", ") + ")."
}

func usesCJKPunctuation(value string) bool {
	for _, r := range value {
		if unicode.In(
			r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul,
		) {
			return true
		}
	}
	return false
}

func qualifiableOperation(operation *Operation) bool {
	return operation != nil &&
		(operation.Type == OperationAdd || operation.Type == OperationUpdate) &&
		strings.TrimSpace(operation.Memory) != ""
}

func groundedExampleEntity(
	source string,
	contextOperation *Operation,
	detail *Operation,
) string {
	for _, category := range contextOperation.Topics {
		category = strings.TrimSpace(category)
		if category == "" || !containsTopic(contextOperation.Memory, category) ||
			!hasTopic(detail.Topics, category) {
			continue
		}
		for _, entity := range detail.Topics {
			entity = strings.TrimSpace(entity)
			if entity == "" || entity == category ||
				!containsTopic(detail.Memory, entity) ||
				containsTopic(contextOperation.Memory, entity) ||
				hasTopic(contextOperation.Topics, entity) {
				continue
			}
			if sourceLinksExample(source, category, entity) {
				return entity
			}
		}
	}
	return ""
}

func hasTopic(topics []string, target string) bool {
	for _, topic := range topics {
		if strings.EqualFold(strings.TrimSpace(topic), target) {
			return true
		}
	}
	return false
}

func sourceLinksExample(source, category, entity string) bool {
	source = strings.ToLower(source)
	category = strings.ToLower(strings.TrimSpace(category))
	entity = strings.ToLower(strings.TrimSpace(entity))
	if source == "" || category == "" || entity == "" {
		return false
	}
	for _, categoryIndex := range topicIndexes(source, category) {
		for _, entityIndex := range topicIndexes(source, entity) {
			if sourceSpanLinksExample(
				source, category, categoryIndex, entity, entityIndex,
			) {
				return true
			}
		}
	}
	return false
}

func topicIndexes(source, topic string) []int {
	var indexes []int
	for offset := 0; offset < len(source); {
		index := strings.Index(source[offset:], topic)
		if index < 0 {
			break
		}
		index += offset
		indexes = append(indexes, index)
		offset = index + len(topic)
	}
	return indexes
}

func sourceSpanLinksExample(
	source, category string,
	categoryIndex int,
	entity string,
	entityIndex int,
) bool {
	if categoryIndex == entityIndex {
		return false
	}
	left, right := categoryIndex+len(category), entityIndex
	if entityIndex < categoryIndex {
		left, right = entityIndex+len(entity), categoryIndex
	}
	if left > right || right-left > maxGroundedExampleContextBytes {
		return false
	}
	between := source[left:right]
	if strings.ContainsAny(between, ".!?;\n。！？；") {
		return false
	}
	for _, cue := range []string{
		" like ", " such as ", " including ", " includes ",
		" include ", " for example ", " one of ",
		"比如", "例如", "包括", "像",
	} {
		if strings.Contains(" "+between+" ", cue) {
			return true
		}
	}
	return false
}

func qualifyOperationWithGroundedTopic(source string, operation *Operation) {
	if !qualifiableOperation(operation) || len(operation.Topics) < 2 {
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
