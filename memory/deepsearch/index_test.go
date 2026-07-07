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

func TestAttachedEntryErrorsAndEmptyIndex(t *testing.T) {
	_, err := MarshalAttachedEntry(nil, nil)
	require.Error(t, err)

	entry := testEntry("m1", "User prefers espresso.", []string{"coffee"})
	raw, err := MarshalAttachedEntry(entry, nil)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "deepsearch_index")

	decodedEntry, decodedIndex, err := UnmarshalAttachedEntry(raw)
	require.NoError(t, err)
	require.Equal(t, entry.ID, decodedEntry.ID)
	require.Nil(t, decodedIndex)

	_, _, err = UnmarshalAttachedEntry([]byte(`null`))
	require.Error(t, err)
	_, _, err = UnmarshalAttachedEntry([]byte(`{`))
	require.Error(t, err)
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
	entry.UpdatedAt = index.Content.Updated

	index.Version = IndexVersion + 1
	require.False(t, IsCurrent(entry, index))
	require.False(t, RowsCurrent([]EntryRow{{Entry: entry, Index: index}}))
	require.True(t, RowsCurrent(nil))
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

func TestCueSearchFilteringAndOrdering(t *testing.T) {
	rows := []EntryRow{
		indexedRow("m1", "User bought espresso beans.", []string{"coffee"}, []string{"espresso beans"}, []string{"coffee"}),
		indexedRow("m2", "User visited Berlin last summer.", []string{"travel"}, []string{"Berlin trip"}, []string{"travel"}),
		{Entry: testEntry("stale", "Stale row", nil)},
	}

	result := SearchCues(rows, CueSearchRequest{
		Query:      "ESPRESSO, coffee",
		MaxResults: 1,
		MinScore:   0.5,
	})
	require.Equal(t, "ESPRESSO, coffee", result.Query)
	require.Len(t, result.Cues, 1)
	require.Equal(t, "espresso beans", result.Cues[0].Text)

	empty := SearchCues(rows, CueSearchRequest{Query: "x"})
	require.Empty(t, empty.Cues)
}

func TestExpandTagsFiltersAndLimits(t *testing.T) {
	row := indexedRow(
		"m1",
		"User prefers espresso after lunch.",
		[]string{"coffee"},
		[]string{"espresso preference", "lunch drink"},
		[]string{"coffee", "espresso", "routine"},
	)
	rows := []EntryRow{row}

	result := ExpandTags(rows, TagExpandRequest{
		Cues:           []string{" ESPRESSO preference "},
		MaxTagsPerCue:  2,
		MaxContents:    1,
		IncludeContent: true,
	})
	require.Len(t, result.Tags, 1)
	require.Len(t, result.Paths, 1)
	require.Equal(t, "coffee", result.Tags[0].Text)
	require.NotNil(t, result.Paths[0].Content)

	byID := ExpandTags(rows, TagExpandRequest{
		CueIDs:        []string{cueID("m1", 1)},
		MaxTagsPerCue: 1,
	})
	require.Len(t, byID.Tags, 1)
	require.Equal(t, cueID("m1", 1), byID.Tags[0].CueID)

	none := ExpandTags(rows, TagExpandRequest{
		Cues:         []string{"missing"},
		MinPathScore: 2,
	})
	require.Empty(t, none.Tags)
	require.Empty(t, none.Paths)
}

func TestLoadContentsByIDRefAllAndLimit(t *testing.T) {
	older := indexedRow("m1", "Older memory.", nil, []string{"old"}, []string{"old"})
	newer := indexedRow("m2", "Newer memory.", nil, []string{"new"}, []string{"new"})
	newer.Index.Content.Updated = newer.Index.Content.Updated.Add(time.Hour)
	rows := []EntryRow{older, newer, {Entry: testEntry("missing-index", "ignored", nil)}}

	all := LoadContents(rows, ContentLoadRequest{MaxResults: 1})
	require.Len(t, all.Contents, 1)
	require.Equal(t, "m2", all.Contents[0].ID)

	byID := LoadContents(rows, ContentLoadRequest{ContentIDs: []string{"m1"}})
	require.Len(t, byID.Contents, 1)
	require.Equal(t, "Older memory.", byID.Contents[0].Text)

	byRef := LoadContents(rows, ContentLoadRequest{Refs: []ContentRef{newer.Index.Content.Ref}})
	require.Len(t, byRef.Contents, 1)
	require.Equal(t, "m2", byRef.Contents[0].ID)

	none := LoadContents(rows, ContentLoadRequest{
		Refs: []ContentRef{{Kind: RefKindMemoryEntry, SourceID: "unknown"}},
	})
	require.Empty(t, none.Contents)
}

func TestIndexTextAndScoringHelpers(t *testing.T) {
	require.Empty(t, IndexText(nil))
	row := indexedRow("m1", "User likes espresso.", nil, []string{"espresso"}, []string{"coffee"})
	text := IndexText(row.Index)
	require.Contains(t, text, "User likes espresso.")
	require.Contains(t, text, "espresso")
	require.Contains(t, text, "coffee")
	require.Equal(t, 0.0, textScore("coffee", ""))
	require.Equal(t, 0.0, textScore("coffee", "x"))
	require.Greater(t, textScore("coffee espresso", "coffee"), 0.0)
	require.Equal(t, []string{"coffee"}, terms("a coffee coffee"))
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

func indexedRow(
	id string,
	text string,
	topics []string,
	cues []string,
	tags []string,
) EntryRow {
	entry := testEntry(id, text, topics)
	document, err := DocumentsFromEntries([]*memory.Entry{entry})
	if err != nil {
		panic(err)
	}
	document[0].Cues = cues
	document[0].Tags = tags
	return EntryRow{
		Entry: entry,
		Index: NewIndex(document[0], entry.UpdatedAt),
	}
}
