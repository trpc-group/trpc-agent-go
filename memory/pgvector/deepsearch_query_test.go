//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
)

func TestService_DeepSearchContentQueries(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()
	svc.deepSearchInited = true

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	eventTime := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		call func() (*deepsearch.QueryResult, error)
	}{
		{
			name: "conversation time",
			call: func() (*deepsearch.QueryResult, error) {
				return svc.QueryConversationTime(ctx, deepsearch.QueryConversationTimeRequest{
					UserKey:    userKey,
					Query:      "Kyoto",
					TimeAfter:  eventTime.Add(-time.Hour),
					TimeBefore: eventTime.Add(time.Hour),
				})
			},
		},
		{
			name: "event keywords",
			call: func() (*deepsearch.QueryResult, error) {
				return svc.QueryEventKeywords(ctx, deepsearch.QueryEventKeywordsRequest{
					UserKey:  userKey,
					Query:    "Kyoto",
					Keywords: []string{"Alice"},
				})
			},
		},
		{
			name: "personal information",
			call: func() (*deepsearch.QueryResult, error) {
				return svc.QueryPersonalInformation(ctx, deepsearch.QueryPersonalInformationRequest{
					UserKey: userKey,
					Query:   "Kyoto",
					Aspects: []string{"travel"},
				})
			},
		},
		{
			name: "personal aspect",
			call: func() (*deepsearch.QueryResult, error) {
				return svc.QueryPersonalAspect(ctx, deepsearch.QueryPersonalAspectRequest{
					UserKey: userKey,
					Aspect:  "travel",
				})
			},
		},
		{
			name: "topic events",
			call: func() (*deepsearch.QueryResult, error) {
				return svc.QueryTopicEvents(ctx, deepsearch.QueryTopicEventsRequest{
					UserKey: userKey,
					Topic:   "travel",
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mock.ExpectQuery("SELECT ct.content_id").
				WillReturnRows(deepSearchContentRows(userKey, eventTime))

			result, err := test.call()
			require.NoError(t, err)
			require.Len(t, result.Contents, 1)
			assert.Equal(t, "content-1", result.Contents[0].ID)
		})
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_DeepSearchEnsureIndexCurrent(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(
		t,
		db,
		mock,
		WithSkipDBInit(true),
		WithDeepSearch(&errorDeepSearchModel{err: assert.AnError}),
	)
	defer svc.Close()
	svc.deepSearchInited = true

	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	now := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	entry := deepSearchMemoryEntry(userKey, now)
	mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content").
		WillReturnRows(deepSearchMemoryRows(entry))
	mock.ExpectQuery("SELECT source_id, metadata->>'source_fingerprint'").
		WillReturnRows(sqlmock.NewRows([]string{"source_id", "source_fingerprint"}).
			AddRow(entry.ID, deepsearch.SourceFingerprint(entry)))

	require.NoError(t, svc.EnsureIndex(context.Background(), userKey))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_DeepSearchIndexHelpers(t *testing.T) {
	now := time.Now()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	entry := deepSearchMemoryEntry(userKey, now)
	cloned := *entry
	clonedMemory := *entry.Memory
	cloned.Memory = &clonedMemory

	assert.True(t, sameEntryFingerprints(
		[]*memory.Entry{entry},
		[]*memory.Entry{&cloned},
	))
	assert.False(t, sameEntryFingerprints(
		[]*memory.Entry{entry},
		nil,
	))
	assert.False(t, sameEntryFingerprints(
		[]*memory.Entry{nil},
		[]*memory.Entry{entry},
	))
	cloned.UpdatedAt = cloned.UpdatedAt.Add(time.Second)
	assert.False(t, sameEntryFingerprints(
		[]*memory.Entry{entry},
		[]*memory.Entry{&cloned},
	))

	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()
	svc.deepSearchInited = true

	require.ErrorContains(t, svc.EnsureIndex(context.Background(), userKey), "not enabled")
	require.NoError(t, svc.IndexDocuments(context.Background(), deepsearch.IndexRequest{
		UserKey: userKey,
	}))
	result, err := svc.SearchCues(context.Background(), deepsearch.CueSearchRequest{
		UserKey: userKey,
		Query:   " ",
	})
	require.NoError(t, err)
	assert.Empty(t, result.Cues)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_DeepSearchIndexCurrentDetectsStaleEntries(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	entry := deepSearchMemoryEntry(userKey, time.Now())

	mock.ExpectQuery("SELECT source_id, metadata->>'source_fingerprint'").
		WillReturnRows(sqlmock.NewRows([]string{"source_id", "source_fingerprint"}))
	current, err := svc.deepSearchIndexCurrent(
		context.Background(),
		userKey,
		[]*memory.Entry{entry},
	)
	require.NoError(t, err)
	assert.False(t, current)

	mock.ExpectQuery("SELECT source_id, metadata->>'source_fingerprint'").
		WillReturnRows(sqlmock.NewRows([]string{"source_id", "source_fingerprint"}).
			AddRow(entry.ID, "stale"))
	current, err = svc.deepSearchIndexCurrent(
		context.Background(),
		userKey,
		[]*memory.Entry{entry},
	)
	require.NoError(t, err)
	assert.False(t, current)

	mock.ExpectQuery("SELECT source_id, metadata->>'source_fingerprint'").
		WillReturnRows(sqlmock.NewRows([]string{"source_id", "source_fingerprint"}))
	current, err = svc.deepSearchIndexCurrent(context.Background(), userKey, nil)
	require.NoError(t, err)
	assert.True(t, current)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_EnsureDeepSearchDBInitializesSchema(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	mock.ExpectQuery("SELECT to_regclass").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery("SELECT has_schema_privilege").
		WillReturnRows(sqlmock.NewRows([]string{"has_schema_privilege"}).AddRow(true))
	for i := 0; i < 3; i++ {
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
	for i := 0; i < 5; i++ {
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	require.NoError(t, svc.ensureDeepSearchDB(context.Background()))
	assert.True(t, svc.deepSearchInited)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_DeepSearchEnsureIndexRebuildsStaleIndex(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(
		t,
		db,
		mock,
		WithSkipDBInit(true),
		WithDeepSearch(&staticDeepSearchModel{
			content: `{"memories":[{"id":"m1","cues":["kyoto"],"tags":["travel"]}]}`,
		}),
	)
	defer svc.Close()
	svc.deepSearchInited = true

	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	now := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	entry := deepSearchMemoryEntry(userKey, now)
	mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content").
		WillReturnRows(deepSearchMemoryRows(entry))
	mock.ExpectQuery("SELECT source_id, metadata->>'source_fingerprint'").
		WillReturnRows(sqlmock.NewRows([]string{"source_id", "source_fingerprint"}))
	mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content").
		WillReturnRows(deepSearchMemoryRows(entry))

	for i := 0; i < 3; i++ {
		mock.ExpectExec("DELETE FROM memories_cue_tag_").
			WithArgs(userKey.AppName, userKey.UserID).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectQuery("SELECT content_id, app_name, user_id, content_text").
		WillReturnRows(sqlmock.NewRows([]string{
			"content_id", "app_name", "user_id", "content_text", "ref_kind",
			"session_id", "event_id", "turn_id", "source_id", "metadata",
			"created_at", "updated_at",
		}))
	mock.ExpectExec("DELETE FROM memories_cue_tag_tags").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM memories_cue_tag_contents").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM memories_cue_tag_cues c").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO memories_cue_tag_contents").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO memories_cue_tag_cues").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO memories_cue_tag_tags").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, svc.EnsureIndex(context.Background(), userKey))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_DeepSearchEdgesAndExpansion(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()
	svc.deepSearchInited = true

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	mock.ExpectQuery("SELECT c.cue_id, c.cue_text, t.tag_id").
		WillReturnRows(sqlmock.NewRows([]string{
			"cue_id", "cue_text", "tag_id", "tag_text", "content_id", "weight",
		}).AddRow("cue-1", "kyoto", "tag-1", "travel", "content-1", 1.0))

	edges, err := svc.EdgesByTag(ctx, deepsearch.EdgesByTagRequest{
		UserKey: userKey,
		Tags:    []string{"travel"},
		Query:   "Kyoto",
	})
	require.NoError(t, err)
	require.Len(t, edges.Paths, 1)
	assert.Equal(t, "tag-1", edges.Paths[0].Tag.ID)

	mock.ExpectQuery("SELECT c.cue_id, c.cue_text, t.tag_id").
		WillReturnRows(sqlmock.NewRows([]string{
			"cue_id", "cue_text", "tag_id", "tag_text", "content_id", "weight",
		}).AddRow("cue-1", "kyoto", "tag-1", "travel", "content-1", 1.0))
	expanded, err := svc.ExpandTags(ctx, deepsearch.TagExpandRequest{
		UserKey:       userKey,
		CueIDs:        []string{"cue-1"},
		MaxTagsPerCue: 1,
		MaxContents:   2,
	})
	require.NoError(t, err)
	require.Len(t, expanded.Paths, 1)
	assert.Equal(t, "travel", expanded.Tags[0].Text)

	mock.ExpectQuery("SELECT cue_id FROM memories_cue_tag_cues").
		WillReturnRows(sqlmock.NewRows([]string{"cue_id"}).AddRow("cue-1"))
	mock.ExpectQuery("SELECT c.cue_id, c.cue_text, t.tag_id").
		WillReturnRows(sqlmock.NewRows([]string{
			"cue_id", "cue_text", "tag_id", "tag_text", "content_id", "weight",
		}).AddRow("cue-1", "kyoto", "tag-1", "travel", "content-1", 1.0))
	expanded, err = svc.ExpandTags(ctx, deepsearch.TagExpandRequest{
		UserKey: userKey,
		Cues:    []string{"Kyoto"},
	})
	require.NoError(t, err)
	require.Len(t, expanded.Paths, 1)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_DeepSearchLoadAndDeleteDocuments(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()
	svc.deepSearchInited = true

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	eventTime := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery("content_id = ANY").
		WillReturnRows(deepSearchContentRows(userKey, eventTime))
	loaded, err := svc.LoadContents(ctx, deepsearch.ContentLoadRequest{
		UserKey:    userKey,
		ContentIDs: []string{"content-1"},
	})
	require.NoError(t, err)
	require.Len(t, loaded.Contents, 1)

	mock.ExpectQuery("ORDER BY updated_at DESC").
		WillReturnRows(deepSearchContentRows(userKey, eventTime))
	loaded, err = svc.LoadContents(ctx, deepsearch.ContentLoadRequest{UserKey: userKey})
	require.NoError(t, err)
	require.Len(t, loaded.Contents, 1)

	for i := 0; i < 3; i++ {
		mock.ExpectExec("DELETE FROM memories_cue_tag_").
			WithArgs(userKey.AppName, userKey.UserID).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	require.NoError(t, svc.DeleteDocuments(ctx, deepsearch.DeleteRequest{
		UserKey:  userKey,
		ClearAll: true,
	}))

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_DeepSearchEventContext(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()
	svc.deepSearchInited = true

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	eventTime := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT DISTINCT content_id").
		WillReturnRows(sqlmock.NewRows([]string{"content_id"}).
			AddRow("content-2").
			AddRow("content-1"))
	mock.ExpectQuery("content_id = ANY").
		WillReturnRows(deepSearchContentRows(userKey, eventTime))

	result, err := svc.QueryEventContext(ctx, deepsearch.QueryEventContextRequest{
		UserKey:    userKey,
		Query:      "Kyoto",
		ContentIDs: []string{" content-1 ", "content-1", ""},
	})
	require.NoError(t, err)
	require.Len(t, result.Contents, 1)
	assert.Equal(t, "content-1", result.Contents[0].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeepSearchQueryHelpers(t *testing.T) {
	svc := &Service{
		deepSearchTables: buildDeepSearchTables("", "memories"),
	}
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	eventTime := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	query, args := svc.buildDeepSearchContentQuery(userKey, deepSearchContentFilter{
		query:        "Kyoto",
		keywords:     []string{"Alice"},
		tags:         []string{"travel"},
		topics:       []string{"travel"},
		participants: []string{"Alice"},
		aspect:       "preference",
		kind:         memory.KindEpisode,
		timeAfter:    eventTime.Add(-time.Hour),
		timeBefore:   eventTime.Add(time.Hour),
	}, 5)
	assert.Contains(t, query, "metadata->>'kind'")
	assert.Contains(t, query, "event_time")
	assert.Contains(t, query, "jsonb_array_elements_text")
	assert.Contains(t, query, "EXISTS (SELECT 1 FROM memories_cue_tag_tags")
	assert.Contains(t, query, "LIMIT 5")
	assert.Len(t, args, 10)

	assert.Contains(t, svc.buildEdgesByTagQuery(true, true, 5), "JOIN memories_cue_tag_contents")
	assert.NotContains(t, svc.buildEdgesByTagQuery(false, false, 5), "JOIN memories_cue_tag_contents")
	assert.Contains(t, deepSearchTextClause("$1"), "LIKE ANY")
	assert.Contains(t, jsonTextArrayClause("topics", "$2"), "topics")
	assert.Equal(t, []string{"%kyoto%", "%alice%"}, likeTerms([]string{" Kyoto ", "", "Alice"}))
	assert.Equal(t, defaultDeepSearchLimit, normalizeDeepSearchLimit(0))
	assert.Equal(t, 3, normalizeDeepSearchLimit(3))

	contents := []deepsearch.Content{
		{
			ID:      "older",
			Text:    "Alice planned a Kyoto trip",
			Created: eventTime.Add(-time.Hour),
			Metadata: deepsearch.Metadata{
				Topics:       []string{"travel"},
				Participants: []string{"Alice"},
				Location:     "Kyoto",
			},
		},
		{
			ID:      "newer",
			Text:    "Unrelated note",
			Updated: eventTime,
		},
	}
	filtered := filterAndRankDeepSearchContents(contents, deepSearchContentFilter{
		query:        "Kyoto",
		keywords:     []string{"Alice"},
		topics:       []string{"travel"},
		participants: []string{"Alice"},
		aspect:       "Kyoto",
	}, 1)
	require.Len(t, filtered, 1)
	assert.Equal(t, "older", filtered[0].ID)
	assert.Greater(t, filtered[0].Score, 0.0)

	assert.True(t, deepSearchContentTextMatches(filtered[0], "Kyoto", nil))
	assert.False(t, deepSearchContentTextMatches(contents[1], "Kyoto", nil))
	assert.True(t, deepSearchContentTextMatches(contents[1], "", nil))
	assert.Equal(t, eventTime.Add(-time.Hour), deepSearchContentTime(filtered[0]))

	ranked := []deepsearch.Content{
		{ID: "b", Text: "Kyoto", Created: eventTime},
		{ID: "a", Text: "Kyoto", Created: eventTime.Add(-time.Hour)},
	}
	rankDeepSearchContents(ranked, "Kyoto", []string{"trip"})
	assert.Equal(t, "a", ranked[0].ID)

	paths := []deepsearch.Path{
		{Tag: deepsearch.Tag{Text: "z"}, Score: 1},
		{Tag: deepsearch.Tag{Text: "a"}, Score: 1},
		{Tag: deepsearch.Tag{Text: "top"}, Score: 2},
	}
	sortDeepSearchPaths(paths)
	assert.Equal(t, "top", paths[0].Tag.Text)
	assert.Equal(t, "a", paths[1].Tag.Text)
}

func deepSearchContentRows(
	userKey memory.UserKey,
	eventTime time.Time,
) *sqlmock.Rows {
	metadata := []byte(`{
		"kind":"episode",
		"event_time":"2024-03-01T12:00:00Z",
		"topics":["travel"],
		"participants":["Alice"],
		"location":"Kyoto"
	}`)
	return sqlmock.NewRows([]string{
		"content_id", "app_name", "user_id", "content_text", "ref_kind",
		"session_id", "event_id", "turn_id", "source_id", "metadata",
		"created_at", "updated_at",
	}).AddRow(
		"content-1",
		userKey.AppName,
		userKey.UserID,
		"Alice planned a Kyoto trip",
		string(deepsearch.RefKindMemoryEntry),
		nil,
		nil,
		nil,
		"memory-1",
		metadata,
		eventTime,
		eventTime,
	)
}

func deepSearchMemoryEntry(userKey memory.UserKey, now time.Time) *memory.Entry {
	return &memory.Entry{
		ID:        "memory-1",
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		CreatedAt: now,
		UpdatedAt: now,
		Memory: &memory.Memory{
			Memory:      "Alice planned a Kyoto trip",
			Topics:      []string{"travel"},
			Kind:        memory.KindEpisode,
			LastUpdated: &now,
		},
	}
}

func deepSearchMemoryRows(entry *memory.Entry) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"memory_id", "app_name", "user_id", "memory_content", "topics",
		"memory_kind", "event_time", "participants", "location",
		"created_at", "updated_at",
	}).AddRow(
		entry.ID,
		entry.AppName,
		entry.UserID,
		entry.Memory.Memory,
		pq.Array([]string{"travel"}),
		string(memory.KindEpisode),
		nil,
		pq.Array([]string{}),
		nil,
		entry.CreatedAt,
		entry.UpdatedAt,
	)
}

func TestScanDeepSearchPathWithContent(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	eventTime := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT path").
		WillReturnRows(sqlmock.NewRows([]string{
			"cue_id", "cue_text", "tag_id", "tag_text", "content_id", "weight",
			"app_name", "user_id", "content_text", "ref_kind", "session_id",
			"event_id", "turn_id", "source_id", "metadata", "created_at", "updated_at",
		}).AddRow(
			"cue-1", "kyoto", "tag-1", "travel", "content-1", 1.0,
			"app", "user", "Kyoto trip", string(deepsearch.RefKindMemoryEntry),
			nil, nil, nil, "memory-1", []byte(`{"topics":["travel"]}`),
			eventTime, eventTime,
		))

	rows, err := db.Query("SELECT path")
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	path, err := scanDeepSearchPath(rows, true)
	require.NoError(t, err)
	require.NotNil(t, path.Content)
	assert.Equal(t, "content-1", path.Content.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResolveDeepSearchContentIDs_QueryError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()
	svc.deepSearchInited = true
	mock.ExpectQuery("SELECT DISTINCT content_id").
		WillReturnError(sql.ErrConnDone)

	_, err := svc.resolveDeepSearchContentIDs(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"},
		[]string{"content-1"},
		1,
	)
	require.ErrorIs(t, err, sql.ErrConnDone)
}
