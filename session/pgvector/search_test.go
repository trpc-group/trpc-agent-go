//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// --- Tests for toFloat32 ---

func TestToFloat32(t *testing.T) {
	f64 := []float64{1.0, 2.5, 3.14, 0.0, -1.0}
	f32 := toFloat32(f64)
	require.Len(t, f32, len(f64))
	for i, v := range f64 {
		assert.InDelta(t, v, float64(f32[i]), 1e-6)
	}
}

func TestToFloat32_Empty(t *testing.T) {
	f32 := toFloat32(nil)
	assert.Empty(t, f32)
}

// --- Tests for SearchEvents ---

func TestSearchEvents_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "",
		UserID:    "user1",
		SessionID: "sess1",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "hello",
	)
	assert.Error(t, err)
	assert.Nil(t, results)
}

func TestSearchEvents_EmptyQuery(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "",
	)
	assert.NoError(t, err)
	assert.Nil(t, results)
}

func TestSearchEvents_WhitespaceQuery(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "   \t\n  ",
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
			embedder:   nil,
		},
		pgClient:           &mockPostgresClient{db: db},
		tableSessionEvents: "session_events",
	}

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "hello",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"embedder not configured")
	assert.Nil(t, results)
}

func TestSearchEvents_EmbedderError(t *testing.T) {
	emb := &mockEmbedder{
		err: fmt.Errorf("embedding service unavailable"),
	}
	s, _, db := newTestService(t, emb)
	defer db.Close()

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "hello",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"generate query embedding")
	assert.Nil(t, results)
}

func TestSearchEvents_EmptyEmbedding(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{},
	}
	s, _, db := newTestService(t, emb)
	defer db.Close()

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "hello",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"empty embedding returned")
	assert.Nil(t, results)
}

func TestSearchEvents_Success(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	evt1 := event.Event{
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
	evt1Bytes, _ := json.Marshal(evt1)

	rows := sqlmock.NewRows(
		[]string{"event", "similarity"},
	).AddRow(evt1Bytes, 0.95)

	mock.ExpectQuery("SELECT event").
		WithArgs(
			anyVectorArg{},
			"app", "user", "sess",
		).
		WillReturnRows(rows)

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "hello",
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "inv-1", results[0].Event.InvocationID)
	assert.InDelta(t, 0.95, results[0].Score, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchEvents_MultipleResults(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	evt1 := event.Event{InvocationID: "inv-1"}
	evt2 := event.Event{InvocationID: "inv-2"}
	evt1Bytes, _ := json.Marshal(evt1)
	evt2Bytes, _ := json.Marshal(evt2)

	rows := sqlmock.NewRows(
		[]string{"event", "similarity"},
	).
		AddRow(evt1Bytes, 0.95).
		AddRow(evt2Bytes, 0.80)

	mock.ExpectQuery("SELECT event").
		WithArgs(
			anyVectorArg{},
			"app", "user", "sess",
		).
		WillReturnRows(rows)

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "test query",
	)
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "inv-1", results[0].Event.InvocationID)
	assert.Equal(t, "inv-2", results[1].Event.InvocationID)
	assert.Greater(t, results[0].Score, results[1].Score)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchEvents_NoResults(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	rows := sqlmock.NewRows(
		[]string{"event", "similarity"},
	)

	mock.ExpectQuery("SELECT event").
		WithArgs(
			anyVectorArg{},
			"app", "user", "sess",
		).
		WillReturnRows(rows)

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "hello",
	)
	require.NoError(t, err)
	assert.Empty(t, results)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchEvents_QueryError(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	mock.ExpectQuery("SELECT event").
		WithArgs(
			anyVectorArg{},
			"app", "user", "sess",
		).
		WillReturnError(fmt.Errorf("db connection lost"))

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "hello",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"search session events")
	assert.Nil(t, results)
}

func TestSearchEvents_WithTopK(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	rows := sqlmock.NewRows(
		[]string{"event", "similarity"},
	)

	// Expect SQL to contain LIMIT 3 when topK=3.
	mock.ExpectQuery(
		`SELECT event.*LIMIT 3`,
	).
		WithArgs(
			anyVectorArg{},
			"app", "user", "sess",
		).
		WillReturnRows(rows)

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "hello",
		session.WithTopK(3),
	)
	require.NoError(t, err)
	assert.Empty(t, results)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchEvents_DefaultTopK(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	rows := sqlmock.NewRows(
		[]string{"event", "similarity"},
	)

	// Default maxResults is 5, so LIMIT should be 5.
	mock.ExpectQuery(
		fmt.Sprintf(`SELECT event.*LIMIT %d`,
			defaultMaxResults),
	).
		WithArgs(
			anyVectorArg{},
			"app", "user", "sess",
		).
		WillReturnRows(rows)

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "hello",
	)
	require.NoError(t, err)
	assert.Empty(t, results)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchEvents_InvalidEventJSON(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	rows := sqlmock.NewRows(
		[]string{"event", "similarity"},
	).AddRow([]byte(`{invalid json`), 0.9)

	mock.ExpectQuery("SELECT event").
		WithArgs(
			anyVectorArg{},
			"app", "user", "sess",
		).
		WillReturnRows(rows)

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "hello",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal event")
	assert.Nil(t, results)
}

// --- Tests for updateLatestEventEmbedding ---

func TestUpdateLatestEventEmbedding_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	sess := &session.Session{
		ID:      "sess-1",
		AppName: "app",
		UserID:  "user",
	}

	mock.ExpectExec("UPDATE session_events SET").
		WithArgs(
			"test content",
			"assistant",
			anyVectorArg{},
			"app", "user", "sess-1",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.updateLatestEventEmbedding(
		context.Background(), sess,
		"test content", "assistant",
		[]float64{0.1, 0.2, 0.3},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateLatestEventEmbedding_DBError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	sess := &session.Session{
		ID:      "sess-1",
		AppName: "app",
		UserID:  "user",
	}

	mock.ExpectExec("UPDATE session_events SET").
		WithArgs(
			"content",
			"user",
			anyVectorArg{},
			"app", "user", "sess-1",
		).
		WillReturnError(fmt.Errorf("db error"))

	err := s.updateLatestEventEmbedding(
		context.Background(), sess,
		"content", "user",
		[]float64{0.1, 0.2, 0.3},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"update event embedding")
}

// Verify SearchOption can be used standalone.
func TestSearchOption_Standalone(t *testing.T) {
	var opts []session.SearchOption
	opts = append(opts, session.WithTopK(3))
	so := session.SearchOptions{}
	for _, o := range opts {
		o(&so)
	}
	assert.Equal(t, 3, so.TopK)
}

// --- Tests for SearchEvents SQL generation ---

func TestSearchEvents_SQLContainsTableName(t *testing.T) {
	const tableName = "custom_session_events"
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)
	defer db.Close()

	s := &Service{
		opts: ServiceOpts{
			maxResults: 5,
			embedder:   emb,
		},
		pgClient:           &mockPostgresClient{db: db},
		tableSessionEvents: tableName,
	}

	rows := sqlmock.NewRows(
		[]string{"event", "similarity"},
	)

	mock.ExpectQuery(tableName).
		WithArgs(
			anyVectorArg{},
			"app", "user", "sess",
		).
		WillReturnRows(rows)

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	_, err = s.SearchEvents(
		context.Background(), key, "hello",
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for trimmed query ---

func TestSearchEvents_TrimmedQuery(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	rows := sqlmock.NewRows(
		[]string{"event", "similarity"},
	)

	mock.ExpectQuery("SELECT event").
		WithArgs(
			anyVectorArg{},
			"app", "user", "sess",
		).
		WillReturnRows(rows)

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	_, err := s.SearchEvents(
		context.Background(), key, "  hello world  ",
	)
	require.NoError(t, err)
	// Verify embedder received trimmed text.
	assert.Equal(t, "hello world", emb.lastText)
}

// Test that query is passed to embedder correctly.
func TestSearchEvents_QueryPassedToEmbedder(
	t *testing.T,
) {
	emb := &mockEmbedder{
		embedding: []float64{0.5, 0.6},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	rows := sqlmock.NewRows(
		[]string{"event", "similarity"},
	)
	mock.ExpectQuery("SELECT event").
		WithArgs(
			anyVectorArg{},
			"app", "user", "sess",
		).
		WillReturnRows(rows)

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	_, err := s.SearchEvents(
		context.Background(), key, "specific query",
	)
	require.NoError(t, err)
	assert.Equal(t, "specific query", emb.lastText)
	assert.Equal(t, 1, emb.callCount)
}

// Test key validation for different missing fields.
func TestSearchEvents_MissingKeyFields(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	tests := []struct {
		name string
		key  session.Key
	}{
		{
			name: "missing app name",
			key: session.Key{
				UserID:    "u",
				SessionID: "s",
			},
		},
		{
			name: "missing user id",
			key: session.Key{
				AppName:   "a",
				SessionID: "s",
			},
		},
		{
			name: "missing session id",
			key: session.Key{
				AppName: "a",
				UserID:  "u",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.SearchEvents(
				context.Background(), tc.key, "q",
			)
			assert.Error(t, err)
		})
	}
}

// --- Test SearchEvents scan error ---

func TestSearchEvents_ScanError(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	// Return columns with wrong types to trigger
	// scan error.
	rows := sqlmock.NewRows(
		[]string{"event", "similarity"},
	).AddRow("not-bytes-or-json", "not-a-float")

	mock.ExpectQuery("SELECT event").
		WithArgs(
			anyVectorArg{},
			"app", "user", "sess",
		).
		WillReturnRows(rows)

	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	results, err := s.SearchEvents(
		context.Background(), key, "hello",
	)
	assert.Error(t, err)
	assert.Nil(t, results)
}
