//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package ranking

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestHybridCandidateLimit(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 90, HybridCandidateLimit(
		"Which sports events did I watch in January?", 30,
	))
	assert.Equal(t, 30, HybridCandidateLimit(
		"What did you recommend?", 30,
	))
	assert.Equal(t, 30, HybridCandidateLimit(
		"Where am I planning to stay for my birthday trip?", 30,
	))
	assert.Equal(t, 90, HybridCandidateLimit(
		"What is the order from earliest to latest?", 30,
	))
	assert.Equal(t, 30, HybridCandidateLimit("When?", 30))
	assert.Equal(t, 0, HybridCandidateLimit("ordinary query", 0))
	assert.Equal(t, -1, HybridCandidateLimit("ordinary query", -1))

	maxInt := int(^uint(0) >> 1)
	assert.Equal(t, maxInt, HybridCandidateLimit("ordinary query", maxInt))
}

func TestMergeHybridBackfillsTemporalSequenceTail(t *testing.T) {
	t.Parallel()

	eventTime := time.Date(2023, time.March, 4, 0, 0, 0, 0, time.UTC)
	entry := func(id, text string, kind memory.Kind) *memory.Entry {
		return &memory.Entry{
			ID: id,
			Memory: &memory.Memory{
				Memory:    text,
				Kind:      kind,
				EventTime: &eventTime,
			},
		}
	}
	results := MergeHybrid(
		"What is the order of the six museums I visited from earliest to latest?",
		[]*memory.Entry{
			entry("base-1", "Visited the Science Museum.", memory.KindEpisode),
			entry("base-2", "Visited the Art Museum.", memory.KindEpisode),
			entry("tail-fact", "Interested in museums.", memory.KindFact),
			entry(
				"tail-count",
				"Reserved six seats at the theater.",
				memory.KindEpisode,
			),
			entry(
				"tail-episode",
				"Met a curator at the Museum of Contemporary Art.",
				memory.KindEpisode,
			),
			entry(
				"tail-shorter-distractor",
				"Museum lecture.",
				memory.KindEpisode,
			),
		},
		nil,
		0,
		2,
	)

	require.Len(t, results, 2)
	assert.Equal(t, []string{"base-1", "tail-episode"}, []string{
		results[0].ID,
		results[1].ID,
	})
}

func TestMergeHybridRejectsDuplicateCalendarOnlyTailMatch(t *testing.T) {
	t.Parallel()

	eventTime := time.Date(2023, time.April, 1, 0, 0, 0, 0, time.UTC)
	entry := func(id, text string, at *time.Time) *memory.Entry {
		return &memory.Entry{
			ID: id,
			Memory: &memory.Memory{
				Memory:    text,
				Kind:      memory.KindEpisode,
				EventTime: at,
			},
		}
	}
	results := MergeHybrid(
		"Which events happened in April?",
		[]*memory.Entry{
			entry("base-1", "Attended a workshop.", nil),
			entry("base-2", "Attended a lecture.", nil),
			entry("tail", "Celebrated a birthday in April.", &eventTime),
		},
		nil,
		0,
		2,
	)

	require.Len(t, results, 2)
	assert.Equal(t, []string{"base-1", "base-2"}, []string{
		results[0].ID,
		results[1].ID,
	})
}

func TestMergeHybridBackfillsFocusedEventTail(t *testing.T) {
	t.Parallel()

	eventTime := time.Date(2023, time.January, 14, 0, 0, 0, 0, time.UTC)
	entry := func(id, text string, kind memory.Kind, at *time.Time) *memory.Entry {
		return &memory.Entry{
			ID: id,
			Memory: &memory.Memory{
				Memory:    text,
				Kind:      kind,
				EventTime: at,
			},
		}
	}
	tailAnswer := entry(
		"tail-answer",
		"Watched the College Football National Championship.",
		memory.KindEpisode,
		&eventTime,
	)
	results := MergeHybrid(
		"What is the order of the sports events I watched in January?",
		[]*memory.Entry{
			entry("base-1", "Watched an NFL game.", memory.KindEpisode, nil),
			entry("base-2", "Attended an NBA game.", memory.KindEpisode, nil),
			entry("base-3", "Other memory.", memory.KindFact, nil),
			entry(
				"tail-fact",
				"Interested in fintech and e-commerce events.",
				memory.KindFact,
				&eventTime,
			),
			tailAnswer,
		},
		nil,
		0,
		3,
	)

	require.Len(t, results, 3)
	assert.Equal(t, []string{"base-1", "base-2", "tail-answer"}, []string{
		results[0].ID,
		results[1].ID,
		results[2].ID,
	})
	assert.Less(t, results[2].Score, results[1].Score)
	assert.NotSame(t, tailAnswer, results[2])
	assert.Zero(t, tailAnswer.Score)
}

func TestMergeHybridDoesNotBackfillAssistantResultQuery(t *testing.T) {
	t.Parallel()

	entry := func(id, text string) *memory.Entry {
		return &memory.Entry{
			ID:     id,
			Memory: &memory.Memory{Memory: text},
		}
	}
	results := MergeHybrid(
		"Which evening train did you recommend?",
		[]*memory.Entry{
			entry("base-1", "Assistant result: Recommended Express 7."),
			entry("base-2", "User asked about evening trains."),
			entry("tail", "Assistant result: Recommended an evening train."),
		},
		nil,
		0,
		2,
	)

	require.Len(t, results, 2)
	assert.Equal(t, []string{"base-1", "base-2"}, []string{
		results[0].ID,
		results[1].ID,
	})
}

func TestMergeHybridFusesBackendRankings(t *testing.T) {
	t.Parallel()

	entry := func(id string) *memory.Entry {
		return &memory.Entry{
			ID:     id,
			Memory: &memory.Memory{Memory: id},
		}
	}
	results := MergeHybrid(
		"query",
		[]*memory.Entry{entry("mem-1"), entry("mem-2")},
		[]*memory.Entry{entry("mem-2"), entry("mem-3")},
		0,
		2,
	)

	require.Len(t, results, 2)
	assert.Equal(t, "mem-2", results[0].ID)
	assert.Greater(t, results[0].Score, results[1].Score)
}

func TestMergeHybridUsesFocusedRanking(t *testing.T) {
	t.Parallel()

	resources := &memory.Entry{
		ID: "resources",
		Memory: &memory.Memory{
			Memory: "Front-end and back-end resources include Code Academy.",
		},
	}
	languages := &memory.Entry{
		ID: "languages",
		Memory: &memory.Memory{
			Memory: "Front-end uses JavaScript. Back-end languages include Go and Python.",
		},
	}
	results := MergeHybrid(
		"Which back-end languages did you recommend?",
		[]*memory.Entry{resources, languages},
		nil,
		0,
		2,
	)

	require.Len(t, results, 2)
	assert.Equal(t, "languages", results[0].ID)
}

func TestMergeHybridDoesNotFocusAssistantResultForUserRecall(t *testing.T) {
	t.Parallel()

	assistant := &memory.Entry{
		ID: "assistant",
		Memory: &memory.Memory{
			Memory: "Assistant result: Suggested the evening train.",
		},
	}
	user := &memory.Entry{
		ID: "user",
		Memory: &memory.Memory{
			Memory: "User chose Express 7 for the evening departure.",
		},
	}

	results := MergeHybrid(
		"Which evening train did I choose?",
		[]*memory.Entry{assistant, user},
		nil,
		0,
		2,
	)

	require.Len(t, results, 2)
	assert.Equal(t, "user", results[0].ID)
}
