//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// --- addOrgProjectQuery ---

func TestAddOrgProjectQuery(t *testing.T) {
	t.Run("nil values map is a no-op", func(t *testing.T) {
		assert.NotPanics(t, func() {
			addOrgProjectQuery(nil, serviceOpts{orgID: "o", projectID: "p"})
		})
	})
	t.Run("adds both when set", func(t *testing.T) {
		q := url.Values{}
		addOrgProjectQuery(q, serviceOpts{orgID: "o", projectID: "p"})
		assert.Equal(t, "o", q.Get("org_id"))
		assert.Equal(t, "p", q.Get("project_id"))
	})
	t.Run("skips empty values", func(t *testing.T) {
		q := url.Values{}
		addOrgProjectQuery(q, serviceOpts{})
		assert.Empty(t, q)
	})
}

// --- addOrgProjectFilter ---

func TestAddOrgProjectFilter(t *testing.T) {
	t.Run("nil filter is a no-op", func(t *testing.T) {
		assert.NotPanics(t, func() {
			addOrgProjectFilter(nil, serviceOpts{orgID: "o"})
		})
	})
	t.Run("missing AND key is a no-op", func(t *testing.T) {
		f := map[string]any{"OR": []any{}}
		addOrgProjectFilter(f, serviceOpts{orgID: "o"})
		assert.NotContains(t, f, "AND")
	})
	t.Run("AND with wrong type is a no-op", func(t *testing.T) {
		f := map[string]any{"AND": "not-a-slice"}
		addOrgProjectFilter(f, serviceOpts{orgID: "o", projectID: "p"})
		assert.Equal(t, "not-a-slice", f["AND"])
	})
	t.Run("appends entries when set", func(t *testing.T) {
		f := map[string]any{"AND": []any{map[string]any{"user_id": "u"}}}
		addOrgProjectFilter(f, serviceOpts{orgID: "o", projectID: "p"})
		andList, ok := f["AND"].([]any)
		require.True(t, ok)
		assert.Len(t, andList, 3)
	})
}

// --- parseMem0Time / parseMem0Times ---

func TestParseMem0Time(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"empty", "", false},
		{"whitespace", "   ", false},
		{"invalid", "not-a-time", false},
		{"rfc3339", "2024-05-07T12:34:56Z", true},
		{"rfc3339nano", "2024-05-07T12:34:56.789Z", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseMem0Time(tc.in)
			assert.Equal(t, tc.ok, ok)
		})
	}
}

func TestParseMem0Times(t *testing.T) {
	t.Run("nil record returns zero values", func(t *testing.T) {
		got := parseMem0Times(nil)
		assert.True(t, got.CreatedAt.IsZero())
		assert.True(t, got.UpdatedAt.IsZero())
	})
	t.Run("missing values stay zero", func(t *testing.T) {
		got := parseMem0Times(&memoryRecord{})
		assert.True(t, got.CreatedAt.IsZero())
		assert.True(t, got.UpdatedAt.IsZero())
	})
	t.Run("valid values are parsed", func(t *testing.T) {
		got := parseMem0Times(&memoryRecord{
			CreatedAt: "2024-01-01T00:00:00Z",
			UpdatedAt: "2024-02-02T00:00:00Z",
		})
		assert.False(t, got.CreatedAt.IsZero())
		assert.False(t, got.UpdatedAt.IsZero())
		assert.True(t, got.UpdatedAt.After(got.CreatedAt))
	})
}

// --- toEntry ---

func TestToEntry(t *testing.T) {
	t.Run("nil record returns nil", func(t *testing.T) {
		assert.Nil(t, toEntry("a", "u", nil))
	})
	t.Run("empty ID returns nil", func(t *testing.T) {
		assert.Nil(t, toEntry("a", "u", &memoryRecord{Memory: "m"}))
	})
	t.Run("empty memory returns nil", func(t *testing.T) {
		assert.Nil(t, toEntry("a", "u", &memoryRecord{ID: "id"}))
	})
	t.Run("populates fields from metadata", func(t *testing.T) {
		rec := &memoryRecord{
			ID:        "id-1",
			Memory:    "content",
			CreatedAt: "2024-01-01T00:00:00Z",
			UpdatedAt: "2024-02-01T00:00:00Z",
			Metadata: map[string]any{
				metadataKeyTRPCTopics:       []any{"a", "b"},
				metadataKeyTRPCKind:         string(memory.KindEpisode),
				metadataKeyTRPCEventTime:    "2024-01-15T00:00:00Z",
				metadataKeyTRPCParticipants: []any{"alice"},
				metadataKeyTRPCLocation:     "tokyo",
			},
		}
		e := toEntry("app", "user", rec)
		require.NotNil(t, e)
		assert.Equal(t, "id-1", e.ID)
		assert.Equal(t, "app", e.AppName)
		assert.Equal(t, "user", e.UserID)
		assert.Equal(t, memory.KindEpisode, e.Memory.Kind)
		assert.Equal(t, []string{"a", "b"}, e.Memory.Topics)
		assert.NotNil(t, e.Memory.EventTime)
		assert.Equal(t, []string{"alice"}, e.Memory.Participants)
		assert.Equal(t, "tokyo", e.Memory.Location)
		assert.NotNil(t, e.Memory.LastUpdated)
	})
}

// --- readTopicsFromMetadata ---

func TestReadTopicsFromMetadata(t *testing.T) {
	t.Run("nil meta returns nil", func(t *testing.T) {
		assert.Nil(t, readTopicsFromMetadata(nil))
	})
	t.Run("missing key returns nil", func(t *testing.T) {
		assert.Nil(t, readTopicsFromMetadata(map[string]any{}))
	})
	t.Run("nil value returns nil", func(t *testing.T) {
		assert.Nil(t, readTopicsFromMetadata(map[string]any{metadataKeyTRPCTopics: nil}))
	})
	t.Run("array of strings", func(t *testing.T) {
		got := readTopicsFromMetadata(map[string]any{
			metadataKeyTRPCTopics: []any{"a", "", "  b  ", 123},
		})
		// whitespace-only string and non-string values are dropped; "  b  "
		// is kept verbatim (no trim applied).
		assert.Equal(t, []string{"a", "  b  "}, got)
	})
	t.Run("single string", func(t *testing.T) {
		got := readTopicsFromMetadata(map[string]any{metadataKeyTRPCTopics: "only"})
		assert.Equal(t, []string{"only"}, got)
	})
	t.Run("blank string returns nil", func(t *testing.T) {
		assert.Nil(t, readTopicsFromMetadata(map[string]any{metadataKeyTRPCTopics: "   "}))
	})
	t.Run("unknown type returns nil", func(t *testing.T) {
		assert.Nil(t, readTopicsFromMetadata(map[string]any{metadataKeyTRPCTopics: 42}))
	})
}

// --- readKindFromMetadata ---

func TestReadKindFromMetadata(t *testing.T) {
	assert.Equal(t, memory.Kind(""), readKindFromMetadata(nil))
	assert.Equal(t, memory.KindFact, readKindFromMetadata(map[string]any{metadataKeyTRPCKind: "  fact  "}))
	assert.Equal(t, memory.Kind(""), readKindFromMetadata(map[string]any{metadataKeyTRPCKind: 42}))
}

// --- readEventTimeFromMetadata ---

func TestReadEventTimeFromMetadata(t *testing.T) {
	assert.Nil(t, readEventTimeFromMetadata(nil))
	assert.Nil(t, readEventTimeFromMetadata(map[string]any{metadataKeyTRPCEventTime: "garbage"}))
	assert.NotNil(t, readEventTimeFromMetadata(map[string]any{metadataKeyTRPCEventTime: "2024-01-15T00:00:00Z"}))
}

// --- readParticipantsFromMetadata ---

func TestReadParticipantsFromMetadata(t *testing.T) {
	t.Run("nil meta", func(t *testing.T) {
		assert.Nil(t, readParticipantsFromMetadata(nil))
	})
	t.Run("missing key", func(t *testing.T) {
		assert.Nil(t, readParticipantsFromMetadata(map[string]any{}))
	})
	t.Run("[]any", func(t *testing.T) {
		got := readParticipantsFromMetadata(map[string]any{
			metadataKeyTRPCParticipants: []any{"  alice ", "", "bob", 1},
		})
		assert.Equal(t, []string{"alice", "bob"}, got)
	})
	t.Run("[]string", func(t *testing.T) {
		got := readParticipantsFromMetadata(map[string]any{
			metadataKeyTRPCParticipants: []string{"alice", " ", "bob"},
		})
		assert.Equal(t, []string{"alice", "bob"}, got)
	})
	t.Run("all blanks", func(t *testing.T) {
		assert.Nil(t, readParticipantsFromMetadata(map[string]any{
			metadataKeyTRPCParticipants: []any{"", "  "},
		}))
	})
	t.Run("unknown type", func(t *testing.T) {
		assert.Nil(t, readParticipantsFromMetadata(map[string]any{
			metadataKeyTRPCParticipants: 42,
		}))
	})
}

// --- readLocationFromMetadata ---

func TestReadLocationFromMetadata(t *testing.T) {
	assert.Empty(t, readLocationFromMetadata(nil))
	assert.Equal(t, "tokyo", readLocationFromMetadata(map[string]any{metadataKeyTRPCLocation: "  tokyo  "}))
	assert.Empty(t, readLocationFromMetadata(map[string]any{metadataKeyTRPCLocation: 42}))
}

// --- messageText ---

func TestMessageText(t *testing.T) {
	t.Run("content only", func(t *testing.T) {
		assert.Equal(t, "hello", messageText(model.Message{Content: "  hello  "}))
	})
	t.Run("empty content and no parts", func(t *testing.T) {
		assert.Empty(t, messageText(model.Message{}))
	})
	t.Run("content parts", func(t *testing.T) {
		s1, s2, empty := "a", "b", "  "
		msg := model.Message{
			ContentParts: []model.ContentPart{
				{Type: model.ContentTypeText, Text: &s1},
				{Type: model.ContentTypeImage}, // ignored
				{Type: model.ContentTypeText, Text: nil},
				{Type: model.ContentTypeText, Text: &empty},
				{Type: model.ContentTypeText, Text: &s2},
			},
		}
		assert.Equal(t, "a\nb", messageText(msg))
	})
}

// --- matchesSearchFilters ---

func TestMatchesSearchFilters(t *testing.T) {
	before := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	after := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	eventIn := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	t.Run("nil entry", func(t *testing.T) {
		assert.False(t, matchesSearchFilters(nil, memory.SearchOptions{}))
	})
	t.Run("nil memory", func(t *testing.T) {
		assert.False(t, matchesSearchFilters(&memory.Entry{}, memory.SearchOptions{}))
	})
	t.Run("strict kind mismatch filters out", func(t *testing.T) {
		e := &memory.Entry{Memory: &memory.Memory{Kind: memory.KindFact}}
		assert.False(t, matchesSearchFilters(e, memory.SearchOptions{Kind: memory.KindEpisode}))
	})
	t.Run("kind fallback allows mismatched kind", func(t *testing.T) {
		e := &memory.Entry{Memory: &memory.Memory{Kind: memory.KindFact}}
		opts := memory.SearchOptions{Kind: memory.KindEpisode, KindFallback: true}
		assert.True(t, matchesSearchFilters(e, opts))
	})
	t.Run("time bounds without event time filters out", func(t *testing.T) {
		e := &memory.Entry{Memory: &memory.Memory{}}
		assert.False(t, matchesSearchFilters(e, memory.SearchOptions{TimeAfter: &before}))
	})
	t.Run("event time before TimeAfter filters out", func(t *testing.T) {
		e := &memory.Entry{Memory: &memory.Memory{EventTime: &before}}
		cutoff := after
		assert.False(t, matchesSearchFilters(e, memory.SearchOptions{TimeAfter: &cutoff}))
	})
	t.Run("event time after TimeBefore filters out", func(t *testing.T) {
		e := &memory.Entry{Memory: &memory.Memory{EventTime: &after}}
		cutoff := before
		assert.False(t, matchesSearchFilters(e, memory.SearchOptions{TimeBefore: &cutoff}))
	})
	t.Run("within both bounds passes", func(t *testing.T) {
		e := &memory.Entry{Memory: &memory.Memory{EventTime: &eventIn}}
		assert.True(t, matchesSearchFilters(e, memory.SearchOptions{TimeAfter: &before, TimeBefore: &after}))
	})
	t.Run("similarity threshold", func(t *testing.T) {
		e := &memory.Entry{Memory: &memory.Memory{}, Score: 0.2}
		assert.False(t, matchesSearchFilters(e, memory.SearchOptions{SimilarityThreshold: 0.5}))
		assert.True(t, matchesSearchFilters(e, memory.SearchOptions{SimilarityThreshold: 0.1}))
	})
}

// --- sortSearchResults ---

func TestSortSearchResults(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)

	t.Run("score descending", func(t *testing.T) {
		a := &memory.Entry{ID: "a", Score: 0.1, Memory: &memory.Memory{}}
		b := &memory.Entry{ID: "b", Score: 0.9, Memory: &memory.Memory{}}
		c := &memory.Entry{ID: "c", Score: 0.5, Memory: &memory.Memory{}}
		results := []*memory.Entry{a, b, c}
		sortSearchResults(results, memory.SearchOptions{})
		assert.Equal(t, []string{"b", "c", "a"}, orderedIDs(results))
	})
	t.Run("kind fallback prioritizes matching kind", func(t *testing.T) {
		a := &memory.Entry{ID: "a", Score: 0.9, Memory: &memory.Memory{Kind: memory.KindFact}}
		b := &memory.Entry{ID: "b", Score: 0.9, Memory: &memory.Memory{Kind: memory.KindEpisode}}
		results := []*memory.Entry{a, b}
		opts := memory.SearchOptions{Kind: memory.KindEpisode, KindFallback: true}
		sortSearchResults(results, opts)
		assert.Equal(t, "b", results[0].ID)
	})
	t.Run("order by event time when scores equal", func(t *testing.T) {
		a := &memory.Entry{ID: "a", Score: 1.0, Memory: &memory.Memory{EventTime: &t3}}
		b := &memory.Entry{ID: "b", Score: 1.0, Memory: &memory.Memory{EventTime: &t1}}
		c := &memory.Entry{ID: "c", Score: 1.0, Memory: &memory.Memory{EventTime: &t2}}
		results := []*memory.Entry{a, b, c}
		sortSearchResults(results, memory.SearchOptions{OrderByEventTime: true})
		assert.Equal(t, []string{"b", "c", "a"}, orderedIDs(results))
	})
	t.Run("order by event time: nil event times sort after non-nil", func(t *testing.T) {
		withTime := &memory.Entry{ID: "withT", Score: 1.0, Memory: &memory.Memory{EventTime: &t1}}
		noTime := &memory.Entry{ID: "noT", Score: 1.0, Memory: &memory.Memory{}}
		results := []*memory.Entry{noTime, withTime}
		sortSearchResults(results, memory.SearchOptions{OrderByEventTime: true})
		assert.Equal(t, "withT", results[0].ID)
	})
	t.Run("fallback to UpdatedAt/CreatedAt when tied", func(t *testing.T) {
		a := &memory.Entry{ID: "a", Score: 1.0, Memory: &memory.Memory{}, UpdatedAt: t1, CreatedAt: t1}
		b := &memory.Entry{ID: "b", Score: 1.0, Memory: &memory.Memory{}, UpdatedAt: t1, CreatedAt: t2}
		c := &memory.Entry{ID: "c", Score: 1.0, Memory: &memory.Memory{}, UpdatedAt: t2, CreatedAt: t1}
		results := []*memory.Entry{a, b, c}
		sortSearchResults(results, memory.SearchOptions{})
		assert.Equal(t, []string{"c", "b", "a"}, orderedIDs(results))
	})
}

// orderedIDs returns the IDs of entries in the slice's current order.
// (Unlike the previous `ids` helper, it does NOT sort — callers rely on the
// order produced by sortSearchResults.)
func orderedIDs(entries []*memory.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ID
	}
	return out
}

// sortedIDs is the alphabetically-sorted variant used by tests that only care
// about set membership.
func sortedIDs(entries []*memory.Entry) []string {
	out := orderedIDs(entries)
	sort.Strings(out)
	return out
}

// --- searchCandidateLimit ---

func TestSearchCandidateLimit(t *testing.T) {
	t.Run("default when no max", func(t *testing.T) {
		assert.Equal(t, defaultSearchTopK, searchCandidateLimit(memory.SearchOptions{}, 0))
	})
	t.Run("max above default", func(t *testing.T) {
		assert.Equal(t, defaultSearchTopK+10, searchCandidateLimit(memory.SearchOptions{}, defaultSearchTopK+10))
	})
	t.Run("filter widens candidate pool to at least page size", func(t *testing.T) {
		opts := memory.SearchOptions{Kind: memory.KindFact}
		assert.GreaterOrEqual(t, searchCandidateLimit(opts, 5), defaultListPageSize)
	})
	t.Run("filter expands to 3x max when large", func(t *testing.T) {
		opts := memory.SearchOptions{Kind: memory.KindFact}
		assert.GreaterOrEqual(t, searchCandidateLimit(opts, 200), 600)
	})
}

// --- isInvalidPageError ---

func TestIsInvalidPageError(t *testing.T) {
	t.Run("nil err", func(t *testing.T) {
		assert.False(t, isInvalidPageError(nil))
	})
	t.Run("non apiError", func(t *testing.T) {
		assert.False(t, isInvalidPageError(errors.New("boom")))
	})
	t.Run("404 with invalid page body", func(t *testing.T) {
		err := &apiError{StatusCode: http.StatusNotFound, Body: "Invalid page."}
		assert.True(t, isInvalidPageError(err))
	})
	t.Run("500 not invalid page", func(t *testing.T) {
		err := &apiError{StatusCode: 500, Body: "invalid page"}
		assert.False(t, isInvalidPageError(err))
	})
	t.Run("404 without invalid page text", func(t *testing.T) {
		err := &apiError{StatusCode: http.StatusNotFound, Body: "not found"}
		assert.False(t, isInvalidPageError(err))
	})
}

// --- cloneMetadata ---

func TestCloneMetadata_NilAndEmpty(t *testing.T) {
	assert.Nil(t, cloneMetadata(nil))
	assert.Nil(t, cloneMetadata(map[string]any{}))
}

func TestCloneMetadata_DeepCloneIsolatesNestedValues(t *testing.T) {
	nestedMap := map[string]any{"leaf": "original"}
	nestedSlice := []any{"a", "b"}
	original := map[string]any{
		"string":      "hello",
		"number":      float64(42),
		"nested_map":  nestedMap,
		"nested_list": nestedSlice,
	}

	cloned := cloneMetadata(original)
	require.NotNil(t, cloned)
	assert.NotSame(t, &original, &cloned, "outer map should be a distinct instance")

	// Mutating the caller's nested map must not affect the clone.
	nestedMap["leaf"] = "mutated"
	clonedNested, ok := cloned["nested_map"].(map[string]any)
	require.True(t, ok, "nested_map type in clone: %T", cloned["nested_map"])
	assert.Equal(t, "original", clonedNested["leaf"], "nested map must be isolated")

	// Mutating the caller's nested slice must not affect the clone.
	nestedSlice[0] = "mutated"
	clonedSlice, ok := cloned["nested_list"].([]any)
	require.True(t, ok, "nested_list type in clone: %T", cloned["nested_list"])
	assert.Equal(t, "a", clonedSlice[0], "nested slice must be isolated")

	assert.Equal(t, "hello", cloned["string"])
	assert.Equal(t, float64(42), cloned["number"])
}

func TestCloneMetadata_NonSerializableInputReturnsNil(t *testing.T) {
	meta := map[string]any{"ch": make(chan int)}
	assert.Nil(t, cloneMetadata(meta))
}

// Reference `sortedIDs` from elsewhere in the test suite if desired.
var _ = sortedIDs
