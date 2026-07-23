//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package ranking

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/internal/assistantresult"
)

var assistantResultDirectRecallPhrases = []string{
	"assistant's",
	"earlier you",
	"did you mention",
	"did you recommend",
	"did you say",
	"did you suggest",
	"the assistant",
	"you listed",
	"you mentioned",
	"you recommended",
	"you said",
	"you suggested",
	"you told me",
	"your earlier answer",
	"your earlier recommendation",
	"your earlier response",
	"your last answer",
	"your last recommendation",
	"your last response",
	"your previous answer",
	"your previous recommendation",
	"your previous response",
}

var assistantResultConversationPhrases = []string{
	"earlier conversation",
	"follow up",
	"follow-up",
	"from our conversation",
	"in our conversation",
	"last conversation",
	"previous conversation",
	"we discussed",
	"we talked",
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
			isAssistantResult := assistantresult.Is(entry.Memory.Memory)
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
	for _, phrase := range assistantResultDirectRecallPhrases {
		if strings.Contains(query, phrase) {
			return true
		}
	}
	if !strings.Contains(query, "remind me") {
		return false
	}
	for _, phrase := range assistantResultConversationPhrases {
		if strings.Contains(query, phrase) {
			return true
		}
	}
	return false
}
