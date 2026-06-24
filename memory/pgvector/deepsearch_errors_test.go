//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestServiceDeepSearchRequestValidation(t *testing.T) {
	ctx := context.Background()
	invalidKey := memory.UserKey{}
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	svc, mock := newDeepSearchMockService(t)

	require.ErrorContains(t, svc.EnsureIndex(ctx, userKey), "not enabled")
	require.Error(t, svc.IndexDocuments(ctx, deepsearch.IndexRequest{UserKey: invalidKey}))

	_, err := svc.SearchCues(ctx, deepsearch.CueSearchRequest{UserKey: invalidKey})
	require.Error(t, err)
	_, err = svc.ExpandTags(ctx, deepsearch.TagExpandRequest{UserKey: invalidKey})
	require.Error(t, err)
	_, err = svc.LoadContents(ctx, deepsearch.ContentLoadRequest{UserKey: invalidKey})
	require.Error(t, err)
	require.Error(t, svc.DeleteDocuments(ctx, deepsearch.DeleteRequest{UserKey: invalidKey}))
	_, err = svc.EdgesByTag(ctx, deepsearch.EdgesByTagRequest{UserKey: invalidKey})
	require.Error(t, err)
	_, err = svc.QueryEventContext(ctx, deepsearch.QueryEventContextRequest{UserKey: invalidKey})
	require.Error(t, err)
	_, err = svc.QueryConversationTime(ctx, deepsearch.QueryConversationTimeRequest{UserKey: invalidKey})
	require.Error(t, err)

	cues, err := svc.SearchCues(ctx, deepsearch.CueSearchRequest{
		UserKey: userKey,
		Query:   " ",
	})
	require.NoError(t, err)
	assert.Empty(t, cues.Cues)

	cues, err = svc.SearchCues(ctx, deepsearch.CueSearchRequest{
		UserKey: userKey,
		Query:   "the and",
	})
	require.NoError(t, err)
	assert.Empty(t, cues.Cues)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestServiceEnsureDeepSearchDBErrorsAndExistingSchema(t *testing.T) {
	ctx := context.Background()
	initErr := errors.New("schema unavailable")

	t.Run("table check error", func(t *testing.T) {
		svc, mock := newUninitializedDeepSearchMockService(t)
		mock.ExpectQuery("SELECT to_regclass").
			WillReturnError(initErr)

		require.ErrorIs(t, svc.ensureDeepSearchDB(ctx), initErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("existing schema", func(t *testing.T) {
		svc, mock := newUninitializedDeepSearchMockService(t)
		for i := 0; i < 3; i++ {
			mock.ExpectQuery("SELECT to_regclass").
				WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		}

		require.NoError(t, svc.ensureDeepSearchDB(ctx))
		assert.True(t, svc.deepSearchInited)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("missing table without ddl privilege", func(t *testing.T) {
		svc, mock := newUninitializedDeepSearchMockService(t)
		mock.ExpectQuery("SELECT to_regclass").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectQuery("SELECT has_schema_privilege").
			WillReturnRows(sqlmock.NewRows([]string{"has_schema_privilege"}).AddRow(false))

		require.ErrorContains(t, svc.ensureDeepSearchDB(ctx), "no DDL privilege")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("ddl privilege check error", func(t *testing.T) {
		svc, mock := newUninitializedDeepSearchMockService(t)
		mock.ExpectQuery("SELECT to_regclass").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectQuery("SELECT has_schema_privilege").
			WillReturnError(initErr)

		require.ErrorIs(t, svc.ensureDeepSearchDB(ctx), initErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("ddl execution error", func(t *testing.T) {
		svc, mock := newUninitializedDeepSearchMockService(t)
		mock.ExpectQuery("SELECT to_regclass").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectQuery("SELECT has_schema_privilege").
			WillReturnRows(sqlmock.NewRows([]string{"has_schema_privilege"}).AddRow(true))
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
			WillReturnError(initErr)

		require.ErrorIs(t, svc.ensureDeepSearchDB(ctx), initErr)
		assert.False(t, svc.deepSearchInited)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty table check result", func(t *testing.T) {
		svc, mock := newUninitializedDeepSearchMockService(t)
		mock.ExpectQuery("SELECT to_regclass").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}))

		exists, err := svc.deepSearchTableExists(ctx, svc.deepSearchTables.cues)
		require.NoError(t, err)
		assert.False(t, exists)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestServiceDeepSearchIndexValidation(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	valid := pgvectorDeepSearchDocument(userKey)
	svc, mock := newDeepSearchMockService(t)

	tests := []struct {
		name    string
		mutate  func(*deepsearch.Document)
		wantErr string
	}{
		{
			name: "missing text",
			mutate: func(document *deepsearch.Document) {
				document.Text = " "
			},
			wantErr: "text is required",
		},
		{
			name: "missing ref kind",
			mutate: func(document *deepsearch.Document) {
				document.Ref.Kind = ""
			},
			wantErr: "ref kind is required",
		},
		{
			name: "missing cues",
			mutate: func(document *deepsearch.Document) {
				document.Cues = []string{" ", "the"}
			},
			wantErr: "cues are required",
		},
		{
			name: "missing tags",
			mutate: func(document *deepsearch.Document) {
				document.Tags = []string{" ", "the"}
			},
			wantErr: "tags are required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := valid
			test.mutate(&document)
			require.ErrorContains(
				t,
				svc.indexDeepSearchDocument(ctx, userKey, document),
				test.wantErr,
			)
		})
	}

	cueID, err := svc.upsertDeepSearchCue(ctx, userKey, " ")
	require.ErrorContains(t, err, "cue text is required")
	assert.Empty(t, cueID)
	require.NoError(t, svc.upsertDeepSearchTag(ctx, userKey, "cue", "content", " "))

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestServiceDeepSearchIndexWriteErrors(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	writeErr := errors.New("write failed")

	t.Run("clear before replace", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectExec("DELETE FROM memories_cue_tag_tags").
			WillReturnError(writeErr)

		err := svc.IndexDocuments(ctx, deepsearch.IndexRequest{
			UserKey: userKey,
			Replace: true,
		})
		require.ErrorIs(t, err, writeErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("content insert", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		expectDeepSearchDocumentCleanup(mock)
		mock.ExpectExec("INSERT INTO memories_cue_tag_contents").
			WillReturnError(writeErr)

		require.ErrorIs(
			t,
			svc.indexDeepSearchDocument(ctx, userKey, pgvectorDeepSearchDocument(userKey)),
			writeErr,
		)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("cue insert", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		expectDeepSearchDocumentCleanup(mock)
		mock.ExpectExec("INSERT INTO memories_cue_tag_contents").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO memories_cue_tag_cues").
			WillReturnError(writeErr)

		require.ErrorIs(
			t,
			svc.indexDeepSearchDocument(ctx, userKey, pgvectorDeepSearchDocument(userKey)),
			writeErr,
		)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("tag insert", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		expectDeepSearchDocumentCleanup(mock)
		mock.ExpectExec("INSERT INTO memories_cue_tag_contents").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO memories_cue_tag_cues").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO memories_cue_tag_tags").
			WillReturnError(writeErr)

		require.ErrorIs(
			t,
			svc.indexDeepSearchDocument(ctx, userKey, pgvectorDeepSearchDocument(userKey)),
			writeErr,
		)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestServiceDeepSearchEnsureIndexErrors(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	now := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	entry := deepSearchMemoryEntry(userKey, now)
	readErr := errors.New("read memories failed")
	indexErr := errors.New("index failed")

	t.Run("invalid user key", func(t *testing.T) {
		svc, mock := newDeepSearchEnabledMockService(t, &staticDeepSearchModel{
			content: `{"memories":[{"id":"m1","cues":["degree"],"tags":["education"]}]}`,
		})
		require.Error(t, svc.EnsureIndex(ctx, memory.UserKey{}))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("read memories", func(t *testing.T) {
		svc, mock := newDeepSearchEnabledMockService(t, &staticDeepSearchModel{
			content: `{"memories":[{"id":"m1","cues":["degree"],"tags":["education"]}]}`,
		})
		mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content").
			WillReturnError(readErr)

		require.ErrorIs(t, svc.EnsureIndex(ctx, userKey), readErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("read fingerprints", func(t *testing.T) {
		svc, mock := newDeepSearchEnabledMockService(t, &staticDeepSearchModel{
			content: `{"memories":[{"id":"m1","cues":["degree"],"tags":["education"]}]}`,
		})
		mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content").
			WillReturnRows(deepSearchMemoryRows(entry))
		mock.ExpectQuery("SELECT source_id, metadata->>'source_fingerprint'").
			WillReturnError(readErr)

		require.ErrorIs(t, svc.EnsureIndex(ctx, userKey), readErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("build documents", func(t *testing.T) {
		svc, mock := newDeepSearchEnabledMockService(
			t,
			&errorDeepSearchModel{err: indexErr},
		)
		mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content").
			WillReturnRows(deepSearchMemoryRows(entry))
		mock.ExpectQuery("SELECT source_id, metadata->>'source_fingerprint'").
			WillReturnRows(sqlmock.NewRows([]string{"source_id", "source_fingerprint"}))

		require.ErrorIs(t, svc.EnsureIndex(ctx, userKey), indexErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("reload memories", func(t *testing.T) {
		svc, mock := newDeepSearchEnabledMockService(t, &staticDeepSearchModel{
			content: `{"memories":[{"id":"m1","cues":["degree"],"tags":["education"]}]}`,
		})
		mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content").
			WillReturnRows(deepSearchMemoryRows(entry))
		mock.ExpectQuery("SELECT source_id, metadata->>'source_fingerprint'").
			WillReturnRows(sqlmock.NewRows([]string{"source_id", "source_fingerprint"}))
		mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content").
			WillReturnError(readErr)

		require.ErrorIs(t, svc.EnsureIndex(ctx, userKey), readErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("memories changed during build", func(t *testing.T) {
		svc, mock := newDeepSearchEnabledMockService(t, &staticDeepSearchModel{
			content: `{"memories":[{"id":"m1","cues":["degree"],"tags":["education"]}]}`,
		})
		changed := *entry
		changed.UpdatedAt = changed.UpdatedAt.Add(time.Second)
		mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content").
			WillReturnRows(deepSearchMemoryRows(entry))
		mock.ExpectQuery("SELECT source_id, metadata->>'source_fingerprint'").
			WillReturnRows(sqlmock.NewRows([]string{"source_id", "source_fingerprint"}))
		mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content").
			WillReturnRows(deepSearchMemoryRows(&changed))

		require.ErrorContains(t, svc.EnsureIndex(ctx, userKey), "changed while building index")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("publish index", func(t *testing.T) {
		svc, mock := newDeepSearchEnabledMockService(t, &staticDeepSearchModel{
			content: `{"memories":[{"id":"m1","cues":["degree"],"tags":["education"]}]}`,
		})
		mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content").
			WillReturnRows(deepSearchMemoryRows(entry))
		mock.ExpectQuery("SELECT source_id, metadata->>'source_fingerprint'").
			WillReturnRows(sqlmock.NewRows([]string{"source_id", "source_fingerprint"}))
		mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content").
			WillReturnRows(deepSearchMemoryRows(entry))
		mock.ExpectExec("DELETE FROM memories_cue_tag_tags").
			WillReturnError(indexErr)

		require.ErrorIs(t, svc.EnsureIndex(ctx, userKey), indexErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestServiceIndexDeepSearchEntryErrors(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	entry := deepSearchMemoryEntry(userKey, time.Now())

	disabled, disabledMock := newDeepSearchMockService(t)
	require.NoError(t, disabled.indexDeepSearchEntry(ctx, nil))
	require.NoError(t, disabled.indexDeepSearchEntry(ctx, entry))
	require.NoError(t, disabledMock.ExpectationsWereMet())

	indexErr := errors.New("index failed")
	enabled, mock := newDeepSearchEnabledMockService(t, &staticDeepSearchModel{
		content: `{"memories":[{"id":"m1","cues":["degree"],"tags":["education"]}]}`,
	})
	mock.ExpectQuery("SELECT content_id, app_name, user_id, content_text").
		WillReturnError(indexErr)

	require.ErrorIs(t, enabled.indexDeepSearchEntry(ctx, entry), indexErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestServiceDeepSearchLoadAndScanErrors(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	loadErr := errors.New("load failed")

	t.Run("load by ids", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectQuery("content_id = ANY").
			WillReturnError(loadErr)

		_, err := svc.LoadContents(ctx, deepsearch.ContentLoadRequest{
			UserKey:    userKey,
			ContentIDs: []string{"content-1"},
		})
		require.ErrorIs(t, err, loadErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("load by ref", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectQuery("coalesce\\(source_id").
			WillReturnError(loadErr)

		_, err := svc.LoadContents(ctx, deepsearch.ContentLoadRequest{
			UserKey: userKey,
			Refs: []deepsearch.ContentRef{{
				Kind:     deepsearch.RefKindMemoryEntry,
				SourceID: "memory-1",
			}},
		})
		require.ErrorIs(t, err, loadErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("load all", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectQuery("ORDER BY updated_at DESC").
			WillReturnError(loadErr)

		_, err := svc.LoadContents(ctx, deepsearch.ContentLoadRequest{UserKey: userKey})
		require.ErrorIs(t, err, loadErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("scan columns", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectQuery("SELECT broken").
			WillReturnRows(sqlmock.NewRows([]string{"only_one"}).AddRow("value"))

		_, err := svc.scanDeepSearchContents(ctx, "SELECT broken")
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("scan metadata", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		now := time.Now()
		mock.ExpectQuery("SELECT invalid_metadata").
			WillReturnRows(sqlmock.NewRows([]string{
				"content_id", "app_name", "user_id", "content_text", "ref_kind",
				"session_id", "event_id", "turn_id", "source_id", "metadata",
				"created_at", "updated_at",
			}).AddRow(
				"content-1", userKey.AppName, userKey.UserID, "memory",
				string(deepsearch.RefKindMemoryEntry), nil, nil, nil, "memory-1",
				[]byte(`{`), now, now,
			))

		_, err := svc.scanDeepSearchContents(ctx, "SELECT invalid_metadata")
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	contents := []deepsearch.Content{}
	seen := make(map[string]struct{})
	appendDeepSearchContents(&contents, seen, []deepsearch.Content{
		{},
		{ID: "content-1"},
		{ID: "content-1"},
		{ID: "content-2"},
	}, 1)
	require.Len(t, contents, 1)
	assert.Equal(t, "content-1", contents[0].ID)
}

func TestServiceDeepSearchDeleteErrors(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	deleteErr := errors.New("delete failed")

	t.Run("clear user", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectExec("DELETE FROM memories_cue_tag_tags").
			WillReturnError(deleteErr)

		err := svc.DeleteDocuments(ctx, deepsearch.DeleteRequest{
			UserKey:  userKey,
			ClearAll: true,
		})
		require.ErrorIs(t, err, deleteErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty delete", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		require.NoError(t, svc.deleteContentDeepSearch(
			ctx,
			userKey,
			"",
			deepsearch.ContentRef{},
		))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("load ref", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectQuery("coalesce\\(source_id").
			WillReturnError(deleteErr)

		err := svc.deleteContentDeepSearch(ctx, userKey, "", deepsearch.ContentRef{
			Kind:     deepsearch.RefKindMemoryEntry,
			SourceID: "memory-1",
		})
		require.ErrorIs(t, err, deleteErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete tags", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectExec("DELETE FROM memories_cue_tag_tags").
			WillReturnError(deleteErr)

		err := svc.deleteContentDeepSearch(ctx, userKey, "content-1", deepsearch.ContentRef{})
		require.ErrorIs(t, err, deleteErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete contents", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectExec("DELETE FROM memories_cue_tag_tags").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("DELETE FROM memories_cue_tag_contents").
			WillReturnError(deleteErr)

		err := svc.deleteContentDeepSearch(ctx, userKey, "content-1", deepsearch.ContentRef{})
		require.ErrorIs(t, err, deleteErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("prune cues", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectExec("DELETE FROM memories_cue_tag_tags").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("DELETE FROM memories_cue_tag_contents").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("DELETE FROM memories_cue_tag_cues c").
			WillReturnError(deleteErr)

		err := svc.deleteContentDeepSearch(ctx, userKey, "content-1", deepsearch.ContentRef{})
		require.ErrorIs(t, err, deleteErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestServiceDeepSearchQueryErrorsAndFallback(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	queryErr := errors.New("query failed")

	t.Run("cue query", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectQuery("SELECT c.cue_id, c.cue_text").
			WillReturnError(queryErr)

		_, err := svc.SearchCues(ctx, deepsearch.CueSearchRequest{
			UserKey: userKey,
			Query:   "graduation degree",
		})
		require.ErrorIs(t, err, queryErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("cue scan and filtering", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectQuery("SELECT c.cue_id, c.cue_text").
			WillReturnRows(sqlmock.NewRows([]string{"cue_id", "cue_text"}).
				AddRow("cue-low", "vacuum cleaner").
				AddRow("cue-b", "graduation degree").
				AddRow("cue-a", "degree graduation"))

		result, err := svc.SearchCues(ctx, deepsearch.CueSearchRequest{
			UserKey:    userKey,
			Query:      "graduation degree",
			MaxResults: 1,
			MinScore:   0.5,
		})
		require.NoError(t, err)
		require.Len(t, result.Cues, 1)
		assert.Equal(t, "cue-a", result.Cues[0].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("resolve cue query", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectQuery("SELECT cue_id FROM memories_cue_tag_cues").
			WillReturnError(queryErr)

		_, err := svc.ExpandTags(ctx, deepsearch.TagExpandRequest{
			UserKey: userKey,
			Cues:    []string{"graduation degree"},
		})
		require.ErrorIs(t, err, queryErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no cue ids", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		result, err := svc.ExpandTags(ctx, deepsearch.TagExpandRequest{
			UserKey: userKey,
		})
		require.NoError(t, err)
		assert.Empty(t, result.Paths)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("tag expansion scan", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectQuery("SELECT c.cue_id, c.cue_text, t.tag_id").
			WillReturnRows(sqlmock.NewRows([]string{"broken"}).AddRow("value"))

		_, err := svc.ExpandTags(ctx, deepsearch.TagExpandRequest{
			UserKey: userKey,
			CueIDs:  []string{"cue-1"},
		})
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("edge query", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectQuery("SELECT c.cue_id, c.cue_text, t.tag_id").
			WillReturnError(queryErr)

		_, err := svc.EdgesByTag(ctx, deepsearch.EdgesByTagRequest{
			UserKey: userKey,
			Query:   "education",
		})
		require.ErrorIs(t, err, queryErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("event context fallback", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectQuery("ORDER BY updated_at DESC").
			WillReturnRows(emptyDeepSearchContentRows())
		mock.ExpectQuery("SELECT ct.content_id").
			WillReturnRows(emptyDeepSearchContentRows())

		result, err := svc.QueryEventContext(ctx, deepsearch.QueryEventContextRequest{
			UserKey: userKey,
			Query:   "graduation degree",
		})
		require.NoError(t, err)
		assert.Empty(t, result.Contents)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("content query", func(t *testing.T) {
		svc, mock := newDeepSearchMockService(t)
		mock.ExpectQuery("SELECT ct.content_id").
			WillReturnError(queryErr)

		_, err := svc.QueryPersonalInformation(ctx, deepsearch.QueryPersonalInformationRequest{
			UserKey: userKey,
			Query:   "education",
		})
		require.ErrorIs(t, err, queryErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPGVectorDeepSearchHelpers(t *testing.T) {
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	ref := normalizeDeepSearchRef(userKey, deepsearch.ContentRef{
		Kind:     deepsearch.RefKindMemoryEntry,
		SourceID: " memory-1 ",
	})
	assert.Equal(t, userKey.AppName, ref.AppName)
	assert.Equal(t, userKey.UserID, ref.UserID)
	assert.Equal(t, "memory-1", ref.SourceID)
	assert.NotEmpty(t, contentRefKey(ref))
	assert.NotEmpty(t, deepSearchContentID(userKey, ref, "", "memory text"))
	assert.Nil(t, nullEmpty(""))
	assert.Equal(t, "value", nullEmpty("value"))
	assert.Empty(t, nullableString(sql.NullString{}))
	assert.Equal(t, "value", nullableString(sql.NullString{String: "value", Valid: true}))
	assert.False(t, isNumericDeepSearchToken("20a4"))
	assert.True(t, isNumericDeepSearchToken("2024"))

	paths := limitPathsPerCue([]deepsearch.Path{
		{Cue: deepsearch.Cue{ID: "cue-1"}, Tag: deepsearch.Tag{ID: "tag-1"}},
		{Cue: deepsearch.Cue{ID: "cue-1"}, Tag: deepsearch.Tag{ID: "tag-2"}},
		{Cue: deepsearch.Cue{ID: "cue-2"}, Tag: deepsearch.Tag{ID: "tag-3"}},
	}, 1)
	require.Len(t, paths, 2)
	assert.Equal(t, "tag-1", paths[0].Tag.ID)
	assert.Equal(t, "tag-3", paths[1].Tag.ID)

	eventTime := time.Now()
	assert.Equal(t, eventTime, deepSearchContentTime(deepsearch.Content{
		Metadata: deepsearch.Metadata{EventTime: eventTime},
	}))
	assert.Equal(t, eventTime, deepSearchContentTime(deepsearch.Content{Updated: eventTime}))
}

func newDeepSearchMockService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	svc.deepSearchInited = true
	t.Cleanup(func() {
		_ = svc.Close()
	})
	return svc, mock
}

func newUninitializedDeepSearchMockService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	t.Cleanup(func() {
		_ = svc.Close()
	})
	return svc, mock
}

func newDeepSearchEnabledMockService(
	t *testing.T,
	indexModel model.Model,
) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := setupMockDB(t)
	svc := setupMockService(
		t,
		db,
		mock,
		WithSkipDBInit(true),
		WithDeepSearch(indexModel),
	)
	svc.deepSearchInited = true
	t.Cleanup(func() {
		_ = svc.Close()
	})
	return svc, mock
}

func pgvectorDeepSearchDocument(userKey memory.UserKey) deepsearch.Document {
	return deepsearch.Document{
		ID:   "memory-1",
		Text: "Alice graduated with a business degree.",
		Cues: []string{"graduation degree"},
		Tags: []string{"education"},
		Ref: deepsearch.ContentRef{
			Kind:     deepsearch.RefKindMemoryEntry,
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			SourceID: "memory-1",
		},
	}
}

func expectDeepSearchDocumentCleanup(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT content_id, app_name, user_id, content_text").
		WillReturnRows(emptyDeepSearchContentRows())
	mock.ExpectExec("DELETE FROM memories_cue_tag_tags").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM memories_cue_tag_contents").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM memories_cue_tag_cues c").
		WillReturnResult(sqlmock.NewResult(0, 0))
}

func emptyDeepSearchContentRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"content_id", "app_name", "user_id", "content_text", "ref_kind",
		"session_id", "event_id", "turn_id", "source_id", "metadata",
		"created_at", "updated_at",
	})
}
