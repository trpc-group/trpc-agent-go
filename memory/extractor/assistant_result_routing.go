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
)

const assistantResultMemoryPrefix = "Assistant result: "

func qualifyAssistantResultOperations(operations []*Operation) {
	for _, operation := range operations {
		if operation == nil || operation.Type != OperationAdd {
			continue
		}
		memoryText := strings.TrimSpace(operation.Memory)
		if memoryText == "" {
			continue
		}
		operation.Memory = assistantResultMemoryPrefix +
			stripAssistantResultPrefixes(memoryText)
	}
}

func stripAssistantResultPrefixes(memoryText string) string {
	marker := strings.ToLower(strings.TrimSpace(assistantResultMemoryPrefix))
	parts := make([]string, 0, 2)
	for {
		index := strings.Index(strings.ToLower(memoryText), marker)
		if index < 0 {
			parts = append(parts, strings.TrimSpace(memoryText))
			break
		}
		parts = append(parts, strings.TrimSpace(memoryText[:index]))
		memoryText = memoryText[index+len(marker):]
	}
	return strings.Join(removeEmptyStrings(parts), " ")
}

func removeEmptyStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func routeAssistantResultOperations(
	primary, results []*Operation,
) ([]*Operation, []*Operation) {
	results = uniqueExtractionOperations(nil, results)
	if len(primary) == 0 || len(results) == 0 {
		return primary, results
	}
	filtered := make([]*Operation, 0, len(primary))
	for _, operation := range primary {
		match := assistantResultOperationIndex(operation, results)
		if match < 0 {
			filtered = append(filtered, operation)
			continue
		}
		results[match] = mergeAssistantResultOperationMetadata(
			operation, results[match],
		)
	}
	return filtered, results
}

func assistantResultOperationIndex(
	primary *Operation,
	results []*Operation,
) int {
	if primary == nil || !explicitAssistantSubject(primary.Memory) {
		return -1
	}
	for i, result := range results {
		if sameExtractionOperation(primary, result) ||
			likelySameAssistantResult(primary, result) {
			return i
		}
	}
	return -1
}

func explicitAssistantSubject(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range []string{
		"assistant ", "assistant's ", "assistant-", "assistant result:",
		"the assistant ",
		"ai assistant ", "助手", "助理",
	} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func likelySameAssistantResult(left, right *Operation) bool {
	if left == nil || right == nil ||
		left.Type != OperationAdd || right.Type != OperationAdd ||
		left.MemoryKind != right.MemoryKind ||
		!sameOptionalTime(left.EventTime, right.EventTime) ||
		!equalFoldedStringSets(left.Participants, right.Participants) ||
		!strings.EqualFold(
			strings.TrimSpace(left.Location),
			strings.TrimSpace(right.Location),
		) {
		return false
	}
	leftTokens := operationTokenSet(left.Memory)
	rightTokens := operationTokenSet(right.Memory)
	minCount, maxCount := len(leftTokens), len(rightTokens)
	if minCount > maxCount {
		minCount, maxCount = maxCount, minCount
	}
	if minCount < 6 {
		return false
	}
	shared := 0
	for token := range leftTokens {
		if _, ok := rightTokens[token]; ok {
			shared++
		}
	}
	return shared >= 6 && shared*100 >= minCount*80 &&
		shared*100 >= maxCount*80
}

func mergeAssistantResultOperationMetadata(
	primary, result *Operation,
) *Operation {
	if primary == nil || result == nil {
		return result
	}
	merged := *result
	merged.Topics = append([]string(nil), result.Topics...)
	seen := make(map[string]struct{}, len(merged.Topics))
	for _, topic := range merged.Topics {
		seen[strings.ToLower(strings.TrimSpace(topic))] = struct{}{}
	}
	for _, topic := range primary.Topics {
		key := strings.ToLower(strings.TrimSpace(topic))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged.Topics = append(merged.Topics, topic)
	}
	return &merged
}

func operationTokenSet(text string) map[string]struct{} {
	tokens := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	result := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if token != "" {
			result[token] = struct{}{}
		}
	}
	return result
}
