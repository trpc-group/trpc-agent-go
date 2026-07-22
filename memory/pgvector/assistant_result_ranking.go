//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const assistantResultPrefix = "assistant result:"

var assistantResultRecallPhrases = []string{
	"assistant",
	"did you mention",
	"did you recommend",
	"did you say",
	"did you suggest",
	"earlier conversation",
	"earlier you",
	"follow up",
	"follow-up",
	"last conversation",
	"in our conversation",
	"from our conversation",
	"previous conversation",
	"remind me",
	"we discussed",
	"we talked",
	"you listed",
	"you mentioned",
	"you recommended",
	"you said",
	"you suggested",
	"you told me",
	"your answer",
	"your recommendation",
	"your response",
}

func rankResultsByAssistantResultIntent(
	query string,
	rankings ...[]*memory.Entry,
) []*memory.Entry {
	wantAssistantResult := asksForAssistantResult(query)
	seen := make(map[string]struct{})
	preferred := make([]*memory.Entry, 0)
	otherFound := false
	for _, ranking := range rankings {
		for _, entry := range ranking {
			if entry == nil || entry.Memory == nil {
				continue
			}
			if _, ok := seen[entry.ID]; ok {
				continue
			}
			seen[entry.ID] = struct{}{}
			isAssistantResult := strings.HasPrefix(
				strings.ToLower(strings.TrimSpace(entry.Memory.Memory)),
				assistantResultPrefix,
			)
			if isAssistantResult == wantAssistantResult {
				preferred = append(preferred, entry)
			} else {
				otherFound = true
			}
		}
	}
	if len(preferred) == 0 || !otherFound {
		return nil
	}
	return preferred
}

func asksForAssistantResult(query string) bool {
	query = strings.ToLower(strings.Join(strings.Fields(query), " "))
	for _, phrase := range assistantResultRecallPhrases {
		if strings.Contains(query, phrase) {
			return true
		}
	}
	return false
}
