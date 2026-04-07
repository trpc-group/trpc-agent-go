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
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	pgvec "github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestToFloat32(t *testing.T) {
	f64 := []float64{1.0, 2.5, 3.14, 0.0, -1.0}
	f32 := toFloat32(f64)
	require.Len(t, f32, len(f64))
	for i, v := range f64 {
		assert.InDelta(t, v, float64(f32[i]), 1e-6)
	}
}

func TestToFloat32_Empty(t *testing.T) {
	assert.Empty(t, toFloat32(nil))
}

func TestSearchEvents_InvalidUserKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	results, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "hello",
			UserKey: session.UserKey{
				UserID: "user1",
			},
		},
	)
	assert.Error(t, err)
	assert.Nil(t, results)
}

func TestSearchEvents_EmptyQuery(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	results, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "   \t\n ",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
		},
	)
	assert.NoError(t, err)
	assert.Nil(t, results)
}

func TestSearchEvents_NilEmbedder(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := &Service{
		opts: ServiceOpts{
			maxResults: defaultMaxResults,
		},
		pgClient:           &mockPostgresClient{db: db},
		tableSessionEvents: "session_events",
		tableSessionStates: "session_states",
	}

	results, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "hello",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
		},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "embedder not configured")
	assert.Nil(t, results)
}

func TestSearchEvents_UnsupportedSearchMode(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	results, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "hello",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
			SearchMode: session.SearchMode("keyword"),
		},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported session search mode")
	assert.Nil(t, results)
}

func TestSearchEvents_EmbedderError(t *testing.T) {
	emb := &mockEmbedder{
		err: fmt.Errorf("embedding service unavailable"),
	}
	s, _, db := newTestService(t, emb)
	defer db.Close()

	results, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "hello",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
		},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "generate query embedding")
	assert.Nil(t, results)
}

func TestSearchEvents_EmptyEmbedding(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{},
	}
	s, _, db := newTestService(t, emb)
	defer db.Close()

	results, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "hello",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
		},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty embedding returned")
	assert.Nil(t, results)
}

func TestSearchEvents_Success(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	sessionCreatedAt := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	eventCreatedAt := time.Date(2025, 1, 2, 4, 5, 6, 0, time.UTC)
	evt := event.Event{
		InvocationID: "inv-1",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Hello there",
				}},
			},
		},
	}
	evtBytes, _ := json.Marshal(evt)

	rows := sqlmock.NewRows(
		[]string{
			"app_name", "user_id", "session_id",
			"session_created_at", "event_created_at",
			"event", "content_text", "role", "similarity",
		},
	).AddRow(
		"app", "user", "sess-2",
		sessionCreatedAt, eventCreatedAt,
		evtBytes, "[SessionDate: 2025-01-02] assistant: Hello there",
		"assistant", 0.95,
	)

	mock.ExpectQuery(`SELECT se\.app_name`).
		WithArgs(anyVectorArg{}, "app", "user").
		WillReturnRows(rows)

	results, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "hello",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "sess-2", results[0].SessionKey.SessionID)
	assert.Equal(t, sessionCreatedAt, results[0].SessionCreatedAt)
	assert.Equal(t, eventCreatedAt, results[0].EventCreatedAt)
	assert.Equal(t, model.RoleAssistant, results[0].Role)
	assert.Contains(t, results[0].Text, "SessionDate")
	assert.Equal(t, "inv-1", results[0].Event.InvocationID)
	assert.InDelta(t, 0.95, results[0].Score, 1e-9)
	assert.InDelta(t, 0.95, results[0].DenseScore, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchEvents_HybridSuccess(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	makeEventBytes := func(invID string, role model.Role, content string) []byte {
		evt := event.Event{
			InvocationID: invID,
			Response: &model.Response{
				Choices: []model.Choice{
					{Message: model.Message{
						Role:    role,
						Content: content,
					}},
				},
			},
		}
		b, _ := json.Marshal(evt)
		return b
	}

	denseRows := sqlmock.NewRows(
		[]string{
			"app_name", "user_id", "session_id",
			"session_created_at", "event_created_at",
			"event", "content_text", "role", "similarity",
		},
	).AddRow(
		"app", "user", "sess-a",
		time.Now(), time.Now(),
		makeEventBytes("inv-a", model.RoleAssistant, "A"),
		"A", "assistant", 0.91,
	).AddRow(
		"app", "user", "sess-b",
		time.Now(), time.Now().Add(time.Second),
		makeEventBytes("inv-b", model.RoleAssistant, "B"),
		"B", "assistant", 0.88,
	)

	keywordRows := sqlmock.NewRows(
		[]string{
			"app_name", "user_id", "session_id",
			"session_created_at", "event_created_at",
			"event", "content_text", "role", "similarity",
		},
	).AddRow(
		"app", "user", "sess-b",
		time.Now(), time.Now().Add(time.Second),
		makeEventBytes("inv-b", model.RoleAssistant, "B"),
		"B", "assistant", 0.70,
	).AddRow(
		"app", "user", "sess-c",
		time.Now(), time.Now().Add(2*time.Second),
		makeEventBytes("inv-c", model.RoleUser, "C"),
		"C", "user", 0.65,
	)

	mock.ExpectQuery(`SELECT se\.app_name`).
		WithArgs(anyVectorArg{}, "app", "user").
		WillReturnRows(denseRows)
	mock.ExpectQuery(`ts_rank\(se\.search_vector`).
		WithArgs("hello", "app", "user").
		WillReturnRows(keywordRows)

	results, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "hello",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
			SearchMode: session.SearchModeHybrid,
			MaxResults: 3,
		},
	)
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, "inv-b", results[0].Event.InvocationID)
	assert.Equal(t, "inv-a", results[1].Event.InvocationID)
	assert.Equal(t, "inv-c", results[2].Event.InvocationID)
	assert.InDelta(t, 0.88, results[0].DenseScore, 1e-9)
	assert.InDelta(t, 0.70, results[0].SparseScore, 1e-9)
	assert.InDelta(t, 1.0/61.0+1.0/62.0, results[0].Score, 1e-6)
	assert.InDelta(t, 0.91, results[1].DenseScore, 1e-9)
	assert.Zero(t, results[1].SparseScore)
	assert.InDelta(t, 0.65, results[2].SparseScore, 1e-9)
	assert.Zero(t, results[2].DenseScore)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchEvents_HybridKeywordErrorFallsBackToDense(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	evt := event.Event{
		InvocationID: "inv-1",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Dense only",
				}},
			},
		},
	}
	evtBytes, _ := json.Marshal(evt)
	denseRows := sqlmock.NewRows(
		[]string{
			"app_name", "user_id", "session_id",
			"session_created_at", "event_created_at",
			"event", "content_text", "role", "similarity",
		},
	).AddRow(
		"app", "user", "sess-1",
		time.Now(), time.Now(),
		evtBytes, "Dense only", "assistant", 0.9,
	)

	mock.ExpectQuery(`SELECT se\.app_name`).
		WithArgs(anyVectorArg{}, "app", "user").
		WillReturnRows(denseRows)
	mock.ExpectQuery(`ts_rank\(se\.search_vector`).
		WithArgs("hello", "app", "user").
		WillReturnError(fmt.Errorf("keyword query failed"))

	results, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "hello",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
			SearchMode: session.SearchModeHybrid,
			MaxResults: 1,
		},
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "inv-1", results[0].Event.InvocationID)
	assert.InDelta(t, 0.9, results[0].DenseScore, 1e-9)
	assert.Zero(t, results[0].SparseScore)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchEvents_FallbackTextAndRoleFromEvent(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	evt := event.Event{
		InvocationID: "inv-1",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "Fallback text",
				}},
			},
		},
	}
	evtBytes, _ := json.Marshal(evt)

	rows := sqlmock.NewRows(
		[]string{
			"app_name", "user_id", "session_id",
			"session_created_at", "event_created_at",
			"event", "content_text", "role", "similarity",
		},
	).AddRow(
		"app", "user", "sess-2",
		time.Now(), time.Now(),
		evtBytes, "", "", 0.81,
	)

	mock.ExpectQuery(`SELECT se\.app_name`).
		WithArgs(anyVectorArg{}, "app", "user").
		WillReturnRows(rows)

	results, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "fallback",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Fallback text", results[0].Text)
	assert.Equal(t, model.RoleUser, results[0].Role)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchEvents_QueryError(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	mock.ExpectQuery(`SELECT se\.app_name`).
		WithArgs(anyVectorArg{}, "app", "user").
		WillReturnError(fmt.Errorf("db connection lost"))

	results, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "hello",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
		},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "search session events")
	assert.Nil(t, results)
}

func TestSearchEvents_InvalidEventJSON(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	rows := sqlmock.NewRows(
		[]string{
			"app_name", "user_id", "session_id",
			"session_created_at", "event_created_at",
			"event", "content_text", "role", "similarity",
		},
	).AddRow(
		"app", "user", "sess-2",
		time.Now(), time.Now(),
		[]byte(`{invalid json`), "x", "assistant", 0.9,
	)

	mock.ExpectQuery(`SELECT se\.app_name`).
		WithArgs(anyVectorArg{}, "app", "user").
		WillReturnRows(rows)

	results, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "hello",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
		},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal event")
	assert.Nil(t, results)
}

func TestSearchEvents_ScanError(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	rows := sqlmock.NewRows(
		[]string{
			"app_name", "user_id", "session_id",
			"session_created_at", "event_created_at",
			"event", "content_text", "role", "similarity",
		},
	).AddRow(
		123, "user", "sess-2",
		"bad-time", time.Now(),
		"not-bytes", "x", "assistant", "not-a-float",
	)

	mock.ExpectQuery(`SELECT se\.app_name`).
		WithArgs(anyVectorArg{}, "app", "user").
		WillReturnRows(rows)

	results, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "hello",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
		},
	)
	assert.Error(t, err)
	assert.Nil(t, results)
}

func TestSearchEvents_TrimmedQuery(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	rows := sqlmock.NewRows(
		[]string{
			"app_name", "user_id", "session_id",
			"session_created_at", "event_created_at",
			"event", "content_text", "role", "similarity",
		},
	)

	mock.ExpectQuery(`SELECT se\.app_name`).
		WithArgs(anyVectorArg{}, "app", "user").
		WillReturnRows(rows)

	_, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "  hello world  ",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "hello world", emb.lastText)
}

func TestSearchEvents_UsesEmbedTimeout(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()
	s.opts.embedTimeout = 3 * time.Second
	now := time.Now()

	rows := sqlmock.NewRows(
		[]string{
			"app_name", "user_id", "session_id",
			"session_created_at", "event_created_at",
			"event", "content_text", "role", "similarity",
		},
	)
	mock.ExpectQuery(`SELECT se\.app_name`).
		WithArgs(anyVectorArg{}, "app", "user").
		WillReturnRows(rows)

	_, err := s.SearchEvents(
		context.Background(),
		session.EventSearchRequest{
			Query: "hello",
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
		},
	)
	require.NoError(t, err)
	require.True(t, emb.lastCtxHasDeadline)
	assert.WithinDuration(
		t,
		now.Add(s.opts.embedTimeout),
		emb.lastDeadline,
		500*time.Millisecond,
	)
}

func TestBuildSearchEventsSQL_Filters(t *testing.T) {
	s, _, db := newTestServiceWithSliceSupport(t, nil)
	defer db.Close()

	after := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	before := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	sql, args := s.buildSearchEventsSQL(
		session.EventSearchRequest{
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
			SessionIDs:        []string{"sess-1", "sess-2", "sess-1"},
			ExcludeSessionIDs: []string{"sess-3"},
			Roles:             []model.Role{model.RoleAssistant},
			CreatedAfter:      &after,
			CreatedBefore:     &before,
			MinScore:          0.7,
			FilterKey:         "branch/a",
		},
		pgvec.NewVector([]float32{0.1, 0.2}),
		7,
	)

	assert.Contains(t, sql, "se.session_id = ANY")
	assert.Contains(t, sql, "NOT (se.session_id = ANY")
	assert.Contains(t, sql, "se.role = ANY")
	assert.Contains(t, sql, "se.created_at >= ")
	assert.Contains(t, sql, "se.created_at <= ")
	assert.Contains(t, sql, "1 - (se.embedding <=> $1) >=")
	assert.Contains(t, sql, "filterKey")
	assert.Contains(t, sql, "LIMIT 7")
	require.Len(t, args, 12)
	assert.Equal(t, "app", args[1])
	assert.Equal(t, "user", args[2])
	assert.Equal(t, []string{"sess-1", "sess-2"}, args[3])
	assert.Equal(t, []string{"sess-3"}, args[4])
	assert.Equal(t, []string{"assistant"}, args[5])
	assert.Equal(t, after, args[6])
	assert.Equal(t, before, args[7])
	assert.Equal(t, 0.7, args[8])
	assert.Equal(t, "branch/a", args[9])
	assert.Equal(t, "branch/a/%", args[10])
	assert.Equal(t, "branch/a", args[11])
}

func TestBuildSearchEventsSQL_DefaultTopKAndTableName(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.tableSessionEvents = "custom_session_events"
	s.tableSessionStates = "custom_session_states"

	sql, args := s.buildSearchEventsSQL(
		session.EventSearchRequest{
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
		},
		pgvec.NewVector([]float32{0.1}),
		defaultMaxResults,
	)

	assert.Contains(t, sql, "FROM custom_session_events se")
	assert.Contains(t, sql, "JOIN custom_session_states ss")
	assert.Contains(t, sql, fmt.Sprintf("LIMIT %d", defaultMaxResults))
	require.Len(t, args, 3)
}

func TestBuildKeywordSearchEventsSQL_Filters(t *testing.T) {
	s, _, db := newTestServiceWithSliceSupport(t, nil)
	defer db.Close()

	after := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	before := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	sql, args := s.buildKeywordSearchEventsSQL(
		session.EventSearchRequest{
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
			SessionIDs:        []string{"sess-1", "sess-2"},
			ExcludeSessionIDs: []string{"sess-3"},
			Roles:             []model.Role{model.RoleAssistant},
			CreatedAfter:      &after,
			CreatedBefore:     &before,
			MinScore:          0.7,
			FilterKey:         "branch/a",
		},
		"kyoto trip",
		9,
	)

	assert.Contains(t, sql, "se.search_vector @@ plainto_tsquery")
	assert.Contains(t, sql, "ts_rank(se.search_vector")
	assert.Contains(t, sql, "se.session_id = ANY")
	assert.Contains(t, sql, "NOT (se.session_id = ANY")
	assert.Contains(t, sql, "se.role = ANY")
	assert.Contains(t, sql, "se.created_at >= ")
	assert.Contains(t, sql, "se.created_at <= ")
	assert.NotContains(t, sql, "embedding <=>")
	assert.Contains(t, sql, "LIMIT 9")
	require.Len(t, args, 11)
	assert.Equal(t, "kyoto trip", args[0])
	assert.Equal(t, "app", args[1])
	assert.Equal(t, "user", args[2])
	assert.Equal(t, []string{"sess-1", "sess-2"}, args[3])
	assert.Equal(t, []string{"sess-3"}, args[4])
	assert.Equal(t, []string{"assistant"}, args[5])
	assert.Equal(t, after, args[6])
	assert.Equal(t, before, args[7])
	assert.Equal(t, "branch/a", args[8])
	assert.Equal(t, "branch/a/%", args[9])
	assert.Equal(t, "branch/a", args[10])
}

func TestResolveHybridCandidateLimit(t *testing.T) {
	assert.Equal(t, 9, resolveHybridCandidateLimit(3, 0, 3))
	assert.Equal(t, 3, resolveHybridCandidateLimit(3, 1, 3))
	assert.Equal(t, 12, resolveHybridCandidateLimit(3, 4, 3))
}

func TestMergeHybridEventResults(t *testing.T) {
	now := time.Now()
	dense := []session.EventSearchResult{
		{
			SessionKey:     session.Key{SessionID: "sess-a"},
			Event:          event.Event{InvocationID: "inv-a"},
			EventCreatedAt: now,
			DenseScore:     0.9,
		},
		{
			SessionKey:     session.Key{SessionID: "sess-b"},
			Event:          event.Event{InvocationID: "inv-b"},
			EventCreatedAt: now.Add(time.Second),
			DenseScore:     0.8,
		},
	}
	keyword := []session.EventSearchResult{
		{
			SessionKey:     session.Key{SessionID: "sess-b"},
			Event:          event.Event{InvocationID: "inv-b"},
			EventCreatedAt: now.Add(time.Second),
			SparseScore:    0.7,
		},
		{
			SessionKey:     session.Key{SessionID: "sess-c"},
			Event:          event.Event{InvocationID: "inv-c"},
			EventCreatedAt: now.Add(2 * time.Second),
			SparseScore:    0.6,
		},
	}

	results := mergeHybridEventResults(dense, keyword, 60, 3)
	require.Len(t, results, 3)
	assert.Equal(t, "inv-b", results[0].Event.InvocationID)
	assert.Equal(t, "inv-a", results[1].Event.InvocationID)
	assert.Equal(t, "inv-c", results[2].Event.InvocationID)
	assert.InDelta(t, 0.8, results[0].DenseScore, 1e-9)
	assert.InDelta(t, 0.7, results[0].SparseScore, 1e-9)
}

func TestMergeHybridEventResults_DistinctEventIDs(t *testing.T) {
	now := time.Now()
	results := mergeHybridEventResults(
		[]session.EventSearchResult{
			{
				SessionKey: session.Key{
					AppName:   "app",
					UserID:    "user",
					SessionID: "sess-a",
				},
				Event: event.Event{
					ID:           "evt-user",
					InvocationID: "inv-a",
				},
				Role:           model.RoleUser,
				Text:           "user question",
				EventCreatedAt: now,
				DenseScore:     0.9,
			},
			{
				SessionKey: session.Key{
					AppName:   "app",
					UserID:    "user",
					SessionID: "sess-a",
				},
				Event: event.Event{
					ID:           "evt-assistant",
					InvocationID: "inv-a",
				},
				Role:           model.RoleAssistant,
				Text:           "assistant answer",
				EventCreatedAt: now.Add(time.Second),
				DenseScore:     0.8,
			},
		},
		nil,
		60,
		10,
	)
	require.Len(t, results, 2)
	assert.Equal(t, "evt-user", results[0].Event.ID)
	assert.Equal(t, "evt-assistant", results[1].Event.ID)
}

func TestCompactStrings(t *testing.T) {
	assert.Equal(
		t,
		[]string{"a", "b"},
		compactStrings([]string{" a ", "b", "", "a"}),
	)
	assert.Nil(t, compactStrings(nil))
}

func TestCompactRoles(t *testing.T) {
	assert.Equal(
		t,
		[]string{"assistant", "user"},
		compactRoles([]model.Role{
			model.RoleAssistant,
			model.RoleUser,
			model.RoleAssistant,
			"",
		}),
	)
	assert.Nil(t, compactRoles(nil))
}

func TestUpdateEventEmbedding_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	sess := &session.Session{
		ID:      "sess-1",
		AppName: "app",
		UserID:  "user",
	}
	evt := &event.Event{InvocationID: "inv-1"}
	eventBytes, _ := json.Marshal(evt)

	mock.ExpectExec("UPDATE session_events SET").
		WithArgs(
			"test content",
			"assistant",
			anyVectorArg{},
			"app", "user", "sess-1",
			string(eventBytes),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.updateEventEmbedding(
		context.Background(), sess, evt,
		"test content", "assistant",
		[]float64{0.1, 0.2, 0.3},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateEventEmbedding_DBError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	sess := &session.Session{
		ID:      "sess-1",
		AppName: "app",
		UserID:  "user",
	}
	evt := &event.Event{InvocationID: "inv-1"}
	eventBytes, _ := json.Marshal(evt)

	mock.ExpectExec("UPDATE session_events SET").
		WithArgs(
			"content",
			"user",
			anyVectorArg{},
			"app", "user", "sess-1",
			string(eventBytes),
		).
		WillReturnError(fmt.Errorf("db error"))

	err := s.updateEventEmbedding(
		context.Background(), sess, evt,
		"content", "user",
		[]float64{0.1, 0.2, 0.3},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "update event embedding")
}

func TestBuildSearchEventsSQL_FilterHierarchy(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	sql, _ := s.buildSearchEventsSQL(
		session.EventSearchRequest{
			UserKey: session.UserKey{
				AppName: "app",
				UserID:  "user",
			},
			FilterKey: "root/child",
		},
		pgvec.NewVector([]float32{0.1}),
		5,
	)

	assert.True(t, strings.Contains(sql, "filterKey"))
	assert.True(t, strings.Contains(sql, "branch"))
	assert.True(t, strings.Contains(sql, "|| '/%'"))
}
