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

type assistantResultRecallVerb struct {
	base string
	past string
}

var assistantResultRecallVerbs = []assistantResultRecallVerb{
	{base: "list", past: "listed"},
	{base: "mention", past: "mentioned"},
	{base: "recommend", past: "recommended"},
	{base: "say", past: "said"},
	{base: "suggest", past: "suggested"},
	{base: "tell", past: "told"},
}

var assistantResultPastReferencePhrases = []string{
	"earlier",
	"last time",
	"previous",
	"prior",
}

var assistantResultOwners = []string{
	"your",
	"assistant's",
	"the assistant's",
}

var assistantResultResponseNouns = []string{
	"answer",
	"recommendation",
	"reply",
	"response",
}

var assistantResultCJKSubjects = []string{
	"你",
	"助手",
	"助理",
}

var assistantResultCJKPastReferences = []string{
	"上次",
	"之前",
	"此前",
	"先前",
	"刚才",
	"前面",
	"上一轮",
}

var assistantResultCJKActions = []string{
	"推荐",
	"建议",
	"提到",
	"说",
	"列出",
	"回答",
	"回复",
	"给出",
	"告诉",
	"总结",
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
	if asksForEnglishAssistantResult(query) {
		return true
	}
	if containsAssistantResultPhrase(
		query, assistantResultPastReferencePhrases,
	) && containsAssistantResultPhrase(
		query, assistantResultOwners,
	) && containsAssistantResultPhrase(
		query, assistantResultResponseNouns,
	) {
		return true
	}
	if asksForCJKAssistantResult(query) {
		return true
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

func asksForEnglishAssistantResult(query string) bool {
	for _, verb := range assistantResultRecallVerbs {
		for _, subject := range []string{"you", "the assistant"} {
			if strings.Contains(
				query, "did "+subject+" "+verb.base,
			) || strings.Contains(
				query, subject+" "+verb.past,
			) {
				return true
			}
		}
	}
	return false
}

func asksForCJKAssistantResult(query string) bool {
	if !containsAssistantResultPhrase(query, assistantResultCJKSubjects) ||
		!containsAssistantResultPhrase(query, assistantResultCJKActions) {
		return false
	}
	return containsAssistantResultPhrase(
		query, assistantResultCJKPastReferences,
	) || containsCompletedCJKAssistantResultAction(query)
}

func containsCompletedCJKAssistantResultAction(query string) bool {
	for _, action := range assistantResultCJKActions {
		if strings.Contains(query, action+"了") {
			return true
		}
	}
	return false
}

func containsAssistantResultPhrase(query string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(query, phrase) {
			return true
		}
	}
	return false
}
