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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestRankResultsByTemporalEventCoverage(t *testing.T) {
	t.Parallel()
	jan15 := time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC)
	feb10 := time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	entry := func(
		id string,
		eventTime time.Time,
		createdAt time.Time,
		location string,
		text string,
	) *memory.Entry {
		return &memory.Entry{
			ID:        id,
			CreatedAt: createdAt,
			Memory: &memory.Memory{
				Memory:    text,
				Kind:      memory.KindEpisode,
				EventTime: &eventTime,
				Location:  location,
			},
		}
	}
	science := entry("science", jan15, created,
		"Science Museum", "Visited the Science Museum.")
	contemporary := entry("contemporary", jan15, created.Add(time.Second),
		"Museum of Contemporary Art", "Attended a lecture at the museum.")
	metropolitan := entry("metropolitan", feb10, created.Add(2*time.Second),
		"Metropolitan Museum of Art", "Visited an Egyptian exhibition.")
	plan := entry("plan", feb10, created.Add(3*time.Second), "",
		"Planning to visit the Modern Art Museum.")
	restaurant := entry("restaurant", jan15, created.Add(4*time.Second),
		"River Cafe", "Had dinner at the River Cafe.")
	fact := entry("fact", jan15, created.Add(5*time.Second),
		"History Museum", "Interested in the History Museum.")
	fact.Memory.Kind = memory.KindFact
	results := []*memory.Entry{
		metropolitan, plan, restaurant, contemporary, fact, science,
	}

	got := rankResultsByTemporalEventCoverage(
		"What is the order of the museums I visited from earliest to latest?",
		results,
	)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"science", "contemporary", "metropolitan"},
		temporalEntryIDs(got))

	got = rankResultsByTemporalEventCoverage(
		"List the museums from latest to earliest.", results,
	)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"metropolitan", "contemporary", "science"},
		temporalEntryIDs(got))

	got = rankResultsByTemporalEventCoverage(
		"Show the museum timeline in reverse chronological order.", results,
	)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"metropolitan", "contemporary", "science"},
		temporalEntryIDs(got))

	assert.Nil(t, rankResultsByTemporalEventCoverage(
		"Which museum did I visit?", results,
	))
	assert.Nil(t, rankResultsByTemporalEventCoverage(
		"What did I order at the museum cafe?", results,
	))
	assert.Nil(t, rankResultsByTemporalEventCoverage(
		"Which museum did I visit latest?", results,
	))

	got = rankResultsByTemporalEventCoverage(
		"List the museum visit sequence.", results,
	)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"science", "contemporary", "metropolitan"},
		temporalEntryIDs(got))
}

func TestBackfillTemporalEventTail(t *testing.T) {
	t.Parallel()
	eventTime := time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC)
	entry := func(id string, location string) *memory.Entry {
		return &memory.Entry{
			ID: id,
			Memory: &memory.Memory{
				Memory:    id,
				Kind:      memory.KindEpisode,
				EventTime: &eventTime,
				Location:  location,
			},
		}
	}
	base := []*memory.Entry{
		entry("a", "Science Museum"),
		entry("b", "Metropolitan Museum of Art"),
		entry("c", "Museum of History"),
		entry("d", "Museum of Contemporary Art"),
	}
	diverse := []*memory.Entry{
		entry("a", "Science Museum"),
		entry("d", "Museum of Contemporary Art"),
		entry("e", "Natural History Museum"),
	}

	results := backfillTemporalEventTail(base, diverse, 3, 1)
	assert.Equal(t, []string{"a", "b", "d"}, temporalEntryIDs(results))
	assert.Equal(t, []string{"a", "b", "c", "d"}, temporalEntryIDs(base))
	assert.Same(t, base[3], results[2])
	assert.Equal(t, []string{"a", "b", "c"}, temporalEntryIDs(
		backfillTemporalEventTail(
			base,
			[]*memory.Entry{
				entry("a", "Science Museum"),
				entry("b", "Metropolitan Museum of Art"),
				entry("e", "Natural History Museum"),
			},
			3,
			1,
		),
	))
	assert.Equal(t, []string{"a", "b"}, temporalEntryIDs(
		backfillTemporalEventTail(base[:2], diverse, 3, 1),
	))

	duplicateEvent := entry("duplicate", "Science Museum")
	duplicateBase := append(append([]*memory.Entry(nil), base[:3]...),
		duplicateEvent)
	assert.Equal(t, []string{"a", "b", "c"}, temporalEntryIDs(
		backfillTemporalEventTail(
			duplicateBase,
			[]*memory.Entry{duplicateEvent},
			3,
			1,
		),
	))
}

func temporalEntryIDs(entries []*memory.Entry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}
