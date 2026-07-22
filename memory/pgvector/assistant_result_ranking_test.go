//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestRankResultsByAssistantResultIntent(t *testing.T) {
	assistantOne := assistantResultEntry("assistant-1", "Recommended Go and Python.")
	assistantTwo := assistantResultEntry("assistant-2", "Listed Ruby and PHP.")
	userOne := &memory.Entry{
		ID: "user-1", Memory: &memory.Memory{Memory: "User knows Java."},
	}
	userTwo := &memory.Entry{
		ID: "user-2", Memory: &memory.Memory{Memory: "User studies Rust."},
	}
	vector := []*memory.Entry{assistantOne, userOne, assistantTwo}
	keyword := []*memory.Entry{userTwo, assistantOne}

	actual := rankResultsByAssistantResultIntent(
		"How many languages do I currently know?", vector, keyword,
	)
	require.Len(t, actual, 2)
	assert.Equal(t, []string{"user-1", "user-2"}, assistantRankingEntryIDs(actual))

	actual = rankResultsByAssistantResultIntent(
		"Remind me what you recommended in our previous conversation.",
		vector,
		keyword,
	)
	require.Len(t, actual, 2)
	assert.Equal(t, []string{"assistant-1", "assistant-2"}, assistantRankingEntryIDs(actual))
}

func TestRankResultsByAssistantResultIntentRequiresMixedProvenance(t *testing.T) {
	assistant := assistantResultEntry("assistant", "Recommended Go.")
	user := &memory.Entry{
		ID: "user", Memory: &memory.Memory{Memory: "User knows Java."},
	}

	assert.Nil(t, rankResultsByAssistantResultIntent(
		"What did you recommend?", []*memory.Entry{assistant},
	))
	assert.Nil(t, rankResultsByAssistantResultIntent(
		"What do I know?", []*memory.Entry{user},
	))
	assert.Nil(t, rankResultsByAssistantResultIntent(
		"What do I know?", []*memory.Entry{nil, {ID: "empty"}},
	))
}

func TestAsksForAssistantResult(t *testing.T) {
	for _, query := range []string{
		"What did you recommend?",
		"Can you remind me what you listed?",
		"Following up on our previous conversation, can you remind me who was mentioned?",
		"What did the assistant say?",
		"What was your previous answer?",
	} {
		assert.True(t, asksForAssistantResult(query), query)
	}
	for _, query := range []string{
		"Can you recommend a restaurant?",
		"Can you remind me when I bought my iPad?",
		"Can you remind me what was listed?",
		"How much will I save?",
		"I want to follow up on my doctor's visit.",
		"What did I buy?",
		"What did I mention in our previous conversation?",
		"What is your recommendation?",
	} {
		assert.False(t, asksForAssistantResult(query), query)
	}
}

func TestAssistantResultIntentContributesToHybridRanking(t *testing.T) {
	assistant := assistantResultEntry("assistant", "Estimated cost is $40.")
	user := &memory.Entry{
		ID: "user", Memory: &memory.Memory{Memory: "Taxi cost is $60."},
	}
	vector := []*memory.Entry{assistant, user}
	provenance := rankResultsByAssistantResultIntent(
		"How much will I save?", vector,
	)

	actual := mergeHybridResults(
		vector, nil, nil, provenance, defaultRRFK, 2,
	)

	require.Len(t, actual, 2)
	assert.Equal(t, "user", actual[0].ID)
	assert.Greater(t, actual[0].Score, actual[1].Score)
}

func assistantResultEntry(id, content string) *memory.Entry {
	return &memory.Entry{
		ID: id,
		Memory: &memory.Memory{
			Memory: "Assistant result: " + content,
		},
	}
}

func assistantRankingEntryIDs(entries []*memory.Entry) []string {
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.ID)
	}
	return result
}
