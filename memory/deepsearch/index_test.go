//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package deepsearch

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestAttachedEntryKeepsDeepSearchFieldsHidden(t *testing.T) {
	entry := testEntry("m1", "User prefers espresso.", []string{"coffee"})
	documents, err := DocumentsFromEntries([]*memory.Entry{entry})
	require.NoError(t, err)

	index := NewIndex(Document{
		ID:       documents[0].ID,
		Text:     documents[0].Text,
		Ref:      documents[0].Ref,
		Metadata: documents[0].Metadata,
		Cues:     []string{"espresso preference"},
		Tags:     []string{"coffee", "preference"},
		Created:  documents[0].Created,
		Updated:  documents[0].Updated,
	}, time.Now())

	raw, err := MarshalAttachedEntry(entry, index)
	require.NoError(t, err)
	require.Contains(t, string(raw), "deepsearch_index")

	var publicEntry memory.Entry
	require.NoError(t, json.Unmarshal(raw, &publicEntry))
	encodedPublic, err := json.Marshal(publicEntry)
	require.NoError(t, err)
	require.NotContains(t, string(encodedPublic), "deepsearch_index")

	decodedEntry, decodedIndex, err := UnmarshalAttachedEntry(raw)
	require.NoError(t, err)
	require.Equal(t, entry.ID, decodedEntry.ID)
	require.Equal(t, index.SourceFingerprint, decodedIndex.SourceFingerprint)
	require.True(t, IsCurrent(decodedEntry, decodedIndex))
}

func TestRowsCurrentDetectsStaleFingerprint(t *testing.T) {
	entry := testEntry("m1", "User prefers tea.", []string{"tea"})
	document, err := DocumentsFromEntries([]*memory.Entry{entry})
	require.NoError(t, err)

	index := NewIndex(Document{
		ID:       document[0].ID,
		Text:     document[0].Text,
		Ref:      document[0].Ref,
		Metadata: document[0].Metadata,
		Cues:     []string{"tea preference"},
		Tags:     []string{"tea"},
		Created:  document[0].Created,
		Updated:  document[0].Updated,
	}, time.Now())
	require.True(t, RowsCurrent([]EntryRow{{Entry: entry, Index: index}}))

	entry.UpdatedAt = entry.UpdatedAt.Add(time.Minute)
	require.False(t, RowsCurrent([]EntryRow{{Entry: entry, Index: index}}))
}

func TestCueTagAndContentQueries(t *testing.T) {
	now := time.Now().UTC()
	entry := testEntry("m1", "User's emergency contact is Alex.", []string{"personal"})
	index := &Index{
		Version: IndexVersion,
		Content: Content{
			ID:   "m1",
			Text: entry.Memory.Memory,
			Ref: ContentRef{
				Kind:     RefKindMemoryEntry,
				AppName:  entry.AppName,
				UserID:   entry.UserID,
				SourceID: entry.ID,
			},
			Updated: now,
		},
		Cues:              []string{"emergency contact"},
		Tags:              []string{"Alex", "personal"},
		SourceFingerprint: SourceFingerprint(entry),
		IndexedAt:         now,
	}
	rows := []EntryRow{{Entry: entry, Index: index}}

	cues := SearchCues(rows, CueSearchRequest{Query: "contact Alex"})
	require.Len(t, cues.Cues, 1)
	require.Equal(t, "emergency contact", cues.Cues[0].Text)

	tags := ExpandTags(rows, TagExpandRequest{
		CueIDs:         []string{cues.Cues[0].ID},
		IncludeContent: true,
	})
	require.Len(t, tags.Tags, 2)
	require.NotNil(t, tags.Paths[0].Content)

	contents := LoadContents(rows, ContentLoadRequest{
		ContentIDs: []string{"m1"},
	})
	require.Len(t, contents.Contents, 1)
	require.Equal(t, "User's emergency contact is Alex.", contents.Contents[0].Text)
}

func testEntry(id, text string, topics []string) *memory.Entry {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	return &memory.Entry{
		ID:      id,
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      text,
			Topics:      topics,
			Kind:        memory.KindFact,
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}
