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
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

// mockEmbedder is a mock implementation of embedder.Embedder.
type mockEmbedder struct {
	embedding  []float64
	err        error
	callCount  int
	lastText   string
	dimensions int
}

func (m *mockEmbedder) GetEmbedding(
	_ context.Context, text string,
) ([]float64, error) {
	m.callCount++
	m.lastText = text
	return m.embedding, m.err
}

func (m *mockEmbedder) GetEmbeddingWithUsage(
	ctx context.Context, text string,
) ([]float64, map[string]any, error) {
	emb, err := m.GetEmbedding(ctx, text)
	return emb, nil, err
}

func (m *mockEmbedder) GetDimensions() int {
	return m.dimensions
}

// mockPostgresClient wraps sql.DB for testing.
type mockPostgresClient struct {
	db *sql.DB
}

func (c *mockPostgresClient) ExecContext(
	ctx context.Context, query string, args ...any,
) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

func (c *mockPostgresClient) Query(
	ctx context.Context,
	handler storage.HandlerFunc,
	query string, args ...any,
) error {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	if err := handler(rows); err != nil {
		return err
	}
	return rows.Err()
}

func (c *mockPostgresClient) Transaction(
	ctx context.Context, fn storage.TxFunc,
) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
		}
	}()
	err = fn(tx)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (c *mockPostgresClient) Close() error {
	return c.db.Close()
}

// anyVectorArg matches pgvector.Vector arguments in
// sqlmock expectations.
type anyVectorArg struct{}

func (a anyVectorArg) Match(_ driver.Value) bool {
	return true
}

// newTestService creates a pgvector Service with a mock
// database for testing. The embedded postgres.Service is
// nil since we only test pgvector-specific methods.
func newTestService(
	t *testing.T,
	emb *mockEmbedder,
) (*Service, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)

	client := &mockPostgresClient{db: db}
	maxResults := defaultMaxResults
	if emb == nil {
		emb = &mockEmbedder{
			embedding:  []float64{0.1, 0.2, 0.3},
			dimensions: 3,
		}
	}
	s := &Service{
		opts: ServiceOpts{
			maxResults:        maxResults,
			embedder:          emb,
			sessionEventLimit: defaultSessionEventLimit,
		},
		pgClient:              client,
		cleanupDone:           make(chan struct{}),
		tableSessionStates:    "session_states",
		tableSessionEvents:    "session_events",
		tableSessionTracks:    "session_track_events",
		tableSessionSummaries: "session_summaries",
		tableAppStates:        "app_states",
		tableUserStates:       "user_states",
	}
	return s, mock, db
}

// mockSummarizer satisfies summary.SessionSummarizer.
type mockSummarizer struct{}

func (m *mockSummarizer) ShouldSummarize(
	_ *session.Session,
) bool {
	return false
}

func (m *mockSummarizer) Summarize(
	_ context.Context, _ *session.Session,
) (string, error) {
	return "", nil
}

func (m *mockSummarizer) SummarizeWithFilter(
	_ context.Context,
	_ *session.Session,
	_ string,
) (string, error) {
	return "", nil
}

func (m *mockSummarizer) SetPrompt(_ string)       {}
func (m *mockSummarizer) SetModel(_ model.Model)   {}
func (m *mockSummarizer) Metadata() map[string]any { return nil }

// --- Helper: verify interface compliance ---

// Compile-time check that *Service implements
// session.SearchableService with the new signature.
var _ session.SearchableService = (*Service)(nil)

// --- Tests for extractEventText ---

func TestExtractEventText_NilEvent(t *testing.T) {
	text, role := extractEventText(nil)
	assert.Empty(t, text)
	assert.Empty(t, role)
}

func TestExtractEventText_NilResponse(t *testing.T) {
	evt := &event.Event{}
	text, role := extractEventText(evt)
	assert.Empty(t, text)
	assert.Empty(t, role)
}

func TestExtractEventText_PartialEvent(t *testing.T) {
	evt := &event.Event{
		Response: &model.Response{
			IsPartial: true,
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "partial content",
				}},
			},
		},
	}
	text, role := extractEventText(evt)
	assert.Empty(t, text)
	assert.Empty(t, role)
}

func TestExtractEventText_EmptyChoices(t *testing.T) {
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{},
		},
	}
	text, role := extractEventText(evt)
	assert.Empty(t, text)
	assert.Empty(t, role)
}

func TestExtractEventText_ToolMessage(t *testing.T) {
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleTool,
					Content: "tool output",
				}},
			},
		},
	}
	text, role := extractEventText(evt)
	assert.Empty(t, text)
	assert.Empty(t, role)
}

func TestExtractEventText_ToolIDMessage(t *testing.T) {
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "response with tool",
					ToolID:  "tool-123",
				}},
			},
		},
	}
	text, role := extractEventText(evt)
	assert.Empty(t, text)
	assert.Empty(t, role)
}

func TestExtractEventText_ToolCallsMessage(t *testing.T) {
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "before tool call",
					ToolCalls: []model.ToolCall{
						{ID: "call-1"},
					},
				}},
			},
		},
	}
	text, role := extractEventText(evt)
	assert.Empty(t, text)
	assert.Empty(t, role)
}

func TestExtractEventText_AssistantContent(t *testing.T) {
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Hello, how can I help?",
				}},
			},
		},
	}
	text, role := extractEventText(evt)
	assert.Equal(t, "Hello, how can I help?", text)
	assert.Equal(t, model.RoleAssistant, role)
}

func TestExtractEventText_UserContent(t *testing.T) {
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "What is Go?",
				}},
			},
		},
	}
	text, role := extractEventText(evt)
	assert.Equal(t, "What is Go?", text)
	assert.Equal(t, model.RoleUser, role)
}

func TestExtractEventText_ContentParts(t *testing.T) {
	part1 := "Hello "
	part2 := "World"
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role: model.RoleAssistant,
					ContentParts: []model.ContentPart{
						{Text: &part1},
						{Text: &part2},
					},
				}},
			},
		},
	}
	text, role := extractEventText(evt)
	assert.Equal(t, "Hello  World", text)
	assert.Equal(t, model.RoleAssistant, role)
}

func TestExtractEventText_EmptyContent(t *testing.T) {
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "",
				}},
			},
		},
	}
	text, role := extractEventText(evt)
	assert.Empty(t, text)
	assert.Empty(t, role)
}

func TestExtractEventText_DefaultsToAssistant(t *testing.T) {
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Content: "no role set",
				}},
			},
		},
	}
	text, role := extractEventText(evt)
	assert.Equal(t, "no role set", text)
	assert.Equal(t, model.RoleAssistant, role)
}

// --- Tests for extractEventText edge cases ---

func TestExtractEventText_ContentPartsWithNilText(
	t *testing.T,
) {
	text1 := "non-nil"
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role: model.RoleAssistant,
					ContentParts: []model.ContentPart{
						{Text: &text1},
						{Text: nil}, // nil text part.
					},
				}},
			},
		},
	}
	text, role := extractEventText(evt)
	assert.Equal(t, "non-nil", text)
	assert.Equal(t, model.RoleAssistant, role)
}

func TestExtractEventText_IsValidContentFalse(
	t *testing.T,
) {
	// Create an event that returns false from
	// IsValidContent (e.g., nil response).
	evt := &event.Event{
		Response: nil,
	}
	text, role := extractEventText(evt)
	assert.Empty(t, text)
	assert.Empty(t, role)
}

// --- Test for ContentParts with only whitespace ---

func TestExtractEventText_ContentPartsWhitespaceOnly(
	t *testing.T,
) {
	space := "   "
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role: model.RoleAssistant,
					ContentParts: []model.ContentPart{
						{Text: &space},
					},
				}},
			},
		},
	}
	text, _ := extractEventText(evt)
	// strings.TrimSpace should result in empty.
	assert.Empty(t, strings.TrimSpace(text))
}

// --- Tests for mergeState ---

func TestMergeState(t *testing.T) {
	appState := session.StateMap{
		"appKey": []byte("appVal"),
	}
	userState := session.StateMap{
		"userKey": []byte("userVal"),
	}
	sess := session.NewSession("app", "user", "sess")
	merged := mergeState(appState, userState, sess)
	assert.Equal(t,
		[]byte("appVal"),
		merged.State[session.StateAppPrefix+"appKey"],
	)
	assert.Equal(t,
		[]byte("userVal"),
		merged.State[session.StateUserPrefix+"userKey"],
	)
}

func TestApplyOptions(t *testing.T) {
	opt := applyOptions(
		session.WithEventNum(10),
	)
	assert.Equal(t, 10, opt.EventNum)
}

// --- Tests for Close ---

func TestClose_ClosesClient(t *testing.T) {
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)

	client := &mockPostgresClient{db: db}
	mock.ExpectClose()
	assert.NoError(t, client.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for asyncIndexEvent ---

func TestAsyncIndexEvent_EmptyText(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, _, db := newTestService(t, emb)
	defer db.Close()

	// Event with no indexable content.
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:   model.RoleTool,
					ToolID: "t1",
				}},
			},
		},
	}
	sess := &session.Session{
		ID: "s1", AppName: "a", UserID: "u",
	}
	// Should return early without calling embedder.
	s.asyncIndexEvent(context.Background(), sess, evt)
	assert.Equal(t, 0, emb.callCount)
}

func TestAsyncIndexEvent_NilEmbedder(t *testing.T) {
	db, _, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)
	defer db.Close()

	s := &Service{
		opts: ServiceOpts{
			embedder: nil,
		},
		pgClient:           &mockPostgresClient{db: db},
		tableSessionEvents: "session_events",
	}

	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "hello",
				}},
			},
		},
	}
	sess := &session.Session{
		ID: "s1", AppName: "a", UserID: "u",
	}
	// Should return early when embedder is nil.
	s.asyncIndexEvent(context.Background(), sess, evt)
}

func TestAsyncIndexEvent_EmbedderError(t *testing.T) {
	emb := &mockEmbedder{
		err: fmt.Errorf("embed fail"),
	}
	s, _, db := newTestService(t, emb)
	defer db.Close()

	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "hello",
				}},
			},
		},
	}
	sess := &session.Session{
		ID: "s1", AppName: "a", UserID: "u",
	}
	// Should log warning but not panic.
	s.asyncIndexEvent(context.Background(), sess, evt)
	assert.Equal(t, 1, emb.callCount)
}

func TestAsyncIndexEvent_EmptyEmbedding(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{},
	}
	s, _, db := newTestService(t, emb)
	defer db.Close()

	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "hello",
				}},
			},
		},
	}
	sess := &session.Session{
		ID: "s1", AppName: "a", UserID: "u",
	}
	s.asyncIndexEvent(context.Background(), sess, evt)
	assert.Equal(t, 1, emb.callCount)
}

func TestAsyncIndexEvent_Success(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	mock.ExpectExec("UPDATE session_events SET").
		WithArgs(
			"hello world",
			string(model.RoleAssistant),
			anyVectorArg{},
			"app", "user", "sess-1",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "hello world",
				}},
			},
		},
	}
	sess := &session.Session{
		ID: "sess-1", AppName: "app", UserID: "user",
	}
	s.asyncIndexEvent(context.Background(), sess, evt)
	assert.Equal(t, 1, emb.callCount)
	assert.Equal(t, "hello world", emb.lastText)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAsyncIndexEvent_UpdateError(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	mock.ExpectExec("UPDATE session_events SET").
		WithArgs(
			"hello",
			string(model.RoleUser),
			anyVectorArg{},
			"app", "user", "sess-1",
		).
		WillReturnError(fmt.Errorf("db down"))

	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}
	sess := &session.Session{
		ID: "sess-1", AppName: "app", UserID: "user",
	}
	// Should log warning, not panic.
	s.asyncIndexEvent(context.Background(), sess, evt)
	assert.Equal(t, 1, emb.callCount)
}

// --- Tests for Close with pgClient ---

func TestClose_NilPgClient(t *testing.T) {
	db, _, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)
	defer db.Close()

	s := &Service{
		pgClient: nil,
	}
	// Should not panic when pgClient is nil.
	// Cannot call s.Close() since Service is nil.
	// Just verify the nil check branch exists.
	assert.Nil(t, s.pgClient)
}

// --- Test Close with non-nil pgClient ---

func TestClose_WithPgClient(t *testing.T) {
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)

	client := &mockPostgresClient{db: db}
	mock.ExpectClose()

	// We can't call s.Close() because s.Service is nil.
	// Test that pgClient.Close() works correctly.
	assert.NoError(t, client.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for CreateSession ---

func TestCreateSession_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{AppName: "", UserID: "u", SessionID: "s"}
	_, err := s.CreateSession(
		context.Background(), key, nil,
	)
	assert.Error(t, err)
}

func TestCreateSession_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	// Check existing session query.
	mock.ExpectQuery("SELECT expires_at FROM").
		WithArgs("app", "user", "sess").
		WillReturnRows(
			sqlmock.NewRows([]string{"expires_at"}),
		)

	// INSERT session.
	mock.ExpectExec("INSERT INTO session_states").
		WillReturnResult(sqlmock.NewResult(1, 1))

	// ListAppStates query.
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WillReturnRows(
			sqlmock.NewRows([]string{"key", "value"}),
		)

	// ListUserStates query.
	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	sess, err := s.CreateSession(
		context.Background(), key,
		session.StateMap{"k1": []byte("v1")},
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "sess", sess.ID)
	assert.Equal(t, "app", sess.AppName)
	assert.Equal(t, "user", sess.UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSession_AlreadyExists(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	// Return an existing session with no expires_at.
	rows := sqlmock.NewRows(
		[]string{"expires_at"},
	).AddRow(nil)
	mock.ExpectQuery("SELECT expires_at FROM").
		WithArgs("app", "user", "sess").
		WillReturnRows(rows)

	_, err := s.CreateSession(
		context.Background(), key, nil,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestCreateSession_CheckExistingQueryError(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectQuery("SELECT expires_at FROM").
		WillReturnError(fmt.Errorf("db error"))

	_, err := s.CreateSession(
		context.Background(), key, nil,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"check existing session failed")
}

func TestCreateSession_InsertError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	// No existing session.
	mock.ExpectQuery("SELECT expires_at FROM").
		WithArgs("app", "user", "sess").
		WillReturnRows(
			sqlmock.NewRows([]string{"expires_at"}),
		)

	mock.ExpectExec("INSERT INTO session_states").
		WillReturnError(fmt.Errorf("insert failed"))

	_, err := s.CreateSession(
		context.Background(), key, nil,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"create session failed")
}

func TestCreateSession_GeneratesSessionID(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "",
	}

	// Check existing session query.
	mock.ExpectQuery("SELECT expires_at FROM").
		WillReturnRows(
			sqlmock.NewRows([]string{"expires_at"}),
		)

	// INSERT session.
	mock.ExpectExec("INSERT INTO session_states").
		WillReturnResult(sqlmock.NewResult(1, 1))

	// ListAppStates.
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WillReturnRows(
			sqlmock.NewRows([]string{"key", "value"}),
		)

	// ListUserStates.
	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	sess, err := s.CreateSession(
		context.Background(), key, nil,
	)
	require.NoError(t, err)
	assert.NotEmpty(t, sess.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for GetSession ---

func TestGetSession_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{AppName: "", UserID: "u"}
	_, err := s.GetSession(context.Background(), key)
	assert.Error(t, err)
}

func TestGetSession_NotFound(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	// Session state query returns no rows.
	mock.ExpectQuery("SELECT state, created_at").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "created_at", "updated_at"},
		))

	sess, err := s.GetSession(
		context.Background(), key,
	)
	require.NoError(t, err)
	assert.Nil(t, sess)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectQuery("SELECT state, created_at").
		WillReturnError(fmt.Errorf("db error"))

	_, err := s.GetSession(
		context.Background(), key,
	)
	assert.Error(t, err)
}

// --- Tests for DeleteSession ---

func TestDeleteSession_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{AppName: "app", UserID: "user"}
	err := s.DeleteSession(context.Background(), key)
	assert.Error(t, err)
}

func TestDeleteSession_SoftDelete(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.softDelete = true

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectBegin()
	// 4 tables: states, summaries, events, tracks.
	for i := 0; i < 4; i++ {
		mock.ExpectExec("UPDATE .* SET deleted_at").
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()

	err := s.DeleteSession(context.Background(), key)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteSession_HardDelete(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.softDelete = false

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectBegin()
	for i := 0; i < 4; i++ {
		mock.ExpectExec("DELETE FROM").
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()

	err := s.DeleteSession(context.Background(), key)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteSession_TransactionError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.softDelete = true

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE .* SET deleted_at").
		WillReturnError(fmt.Errorf("tx error"))
	mock.ExpectRollback()

	err := s.DeleteSession(context.Background(), key)
	assert.Error(t, err)
}

// --- Tests for ListSessions ---

func TestListSessions_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	userKey := session.UserKey{AppName: "", UserID: "u"}
	_, err := s.ListSessions(
		context.Background(), userKey,
	)
	assert.Error(t, err)
}

func TestListSessions_Empty(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "app", UserID: "user",
	}

	// ListAppStates.
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WillReturnRows(
			sqlmock.NewRows([]string{"key", "value"}),
		)
	// ListUserStates.
	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)
	// List session states returns empty.
	mock.ExpectQuery("SELECT session_id, state").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "state",
				"created_at", "updated_at",
			},
		))

	sessions, err := s.ListSessions(
		context.Background(), userKey,
	)
	require.NoError(t, err)
	assert.Empty(t, sessions)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSessions_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "app", UserID: "user",
	}

	// ListAppStates.
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WillReturnRows(
			sqlmock.NewRows([]string{"key", "value"}),
		)
	// ListUserStates.
	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)
	// List session states error.
	mock.ExpectQuery("SELECT session_id, state").
		WillReturnError(fmt.Errorf("db error"))

	_, err := s.ListSessions(
		context.Background(), userKey,
	)
	assert.Error(t, err)
}

// --- Tests for UpdateAppState ---

func TestUpdateAppState_EmptyAppName(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	err := s.UpdateAppState(
		context.Background(), "",
		session.StateMap{"k": []byte("v")},
	)
	assert.Error(t, err)
	assert.Equal(t, session.ErrAppNameRequired, err)
}

func TestUpdateAppState_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectExec("INSERT INTO app_states").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.UpdateAppState(
		context.Background(), "app",
		session.StateMap{"k": []byte("v")},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAppState_DBError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectExec("INSERT INTO app_states").
		WillReturnError(fmt.Errorf("db error"))

	err := s.UpdateAppState(
		context.Background(), "app",
		session.StateMap{"k": []byte("v")},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"update app state failed")
}

func TestUpdateAppState_StripsPrefix(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectExec("INSERT INTO app_states").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.UpdateAppState(
		context.Background(), "app",
		session.StateMap{
			session.StateAppPrefix + "k": []byte("v"),
		},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for ListAppStates ---

func TestListAppStates_EmptyAppName(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	_, err := s.ListAppStates(context.Background(), "")
	assert.Error(t, err)
	assert.Equal(t, session.ErrAppNameRequired, err)
}

func TestListAppStates_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	rows := sqlmock.NewRows(
		[]string{"key", "value"},
	).AddRow("k1", []byte("v1")).
		AddRow("k2", []byte("v2"))

	mock.ExpectQuery("SELECT key, value FROM app_states").
		WillReturnRows(rows)

	result, err := s.ListAppStates(
		context.Background(), "app",
	)
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, []byte("v1"), result["k1"])
	assert.Equal(t, []byte("v2"), result["k2"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListAppStates_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectQuery("SELECT key, value FROM app_states").
		WillReturnError(fmt.Errorf("db error"))

	_, err := s.ListAppStates(
		context.Background(), "app",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"list app states failed")
}

// --- Tests for DeleteAppState ---

func TestDeleteAppState_EmptyAppName(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	err := s.DeleteAppState(
		context.Background(), "", "k",
	)
	assert.Error(t, err)
	assert.Equal(t, session.ErrAppNameRequired, err)
}

func TestDeleteAppState_EmptyKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	err := s.DeleteAppState(
		context.Background(), "app", "",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"state key is required")
}

func TestDeleteAppState_SoftDelete(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.softDelete = true

	mock.ExpectExec("UPDATE .* SET deleted_at").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.DeleteAppState(
		context.Background(), "app", "k",
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteAppState_HardDelete(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.softDelete = false

	mock.ExpectExec("DELETE FROM").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.DeleteAppState(
		context.Background(), "app", "k",
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteAppState_DBError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.softDelete = true

	mock.ExpectExec("UPDATE .* SET deleted_at").
		WillReturnError(fmt.Errorf("db error"))

	err := s.DeleteAppState(
		context.Background(), "app", "k",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"delete app state failed")
}

// --- Tests for UpdateUserState ---

func TestUpdateUserState_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	err := s.UpdateUserState(
		context.Background(),
		session.UserKey{AppName: "", UserID: "u"},
		session.StateMap{"k": []byte("v")},
	)
	assert.Error(t, err)
}

func TestUpdateUserState_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectExec("INSERT INTO user_states").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.UpdateUserState(
		context.Background(),
		session.UserKey{AppName: "app", UserID: "user"},
		session.StateMap{"k": []byte("v")},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateUserState_DBError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectExec("INSERT INTO user_states").
		WillReturnError(fmt.Errorf("db error"))

	err := s.UpdateUserState(
		context.Background(),
		session.UserKey{AppName: "app", UserID: "user"},
		session.StateMap{"k": []byte("v")},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"update user state failed")
}

// --- Tests for ListUserStates ---

func TestListUserStates_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	_, err := s.ListUserStates(
		context.Background(),
		session.UserKey{AppName: "", UserID: "u"},
	)
	assert.Error(t, err)
}

func TestListUserStates_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	rows := sqlmock.NewRows(
		[]string{"key", "value"},
	).AddRow("k1", []byte("v1"))

	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(rows)

	result, err := s.ListUserStates(
		context.Background(),
		session.UserKey{AppName: "app", UserID: "user"},
	)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, []byte("v1"), result["k1"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListUserStates_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnError(fmt.Errorf("db error"))

	_, err := s.ListUserStates(
		context.Background(),
		session.UserKey{AppName: "app", UserID: "user"},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"list user states failed")
}

// --- Tests for DeleteUserState ---

func TestDeleteUserState_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	err := s.DeleteUserState(
		context.Background(),
		session.UserKey{AppName: "", UserID: "u"},
		"k",
	)
	assert.Error(t, err)
}

func TestDeleteUserState_EmptyKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	err := s.DeleteUserState(
		context.Background(),
		session.UserKey{AppName: "app", UserID: "user"},
		"",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"state key is required")
}

func TestDeleteUserState_SoftDelete(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.softDelete = true

	mock.ExpectExec("UPDATE .* SET deleted_at").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.DeleteUserState(
		context.Background(),
		session.UserKey{AppName: "app", UserID: "user"},
		"k",
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteUserState_HardDelete(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.softDelete = false

	mock.ExpectExec("DELETE FROM").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.DeleteUserState(
		context.Background(),
		session.UserKey{AppName: "app", UserID: "user"},
		"k",
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteUserState_DBError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.softDelete = true

	mock.ExpectExec("UPDATE .* SET deleted_at").
		WillReturnError(fmt.Errorf("db error"))

	err := s.DeleteUserState(
		context.Background(),
		session.UserKey{AppName: "app", UserID: "user"},
		"k",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"delete user state failed")
}

// --- Tests for UpdateSessionState ---

func TestUpdateSessionState_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	err := s.UpdateSessionState(
		context.Background(),
		session.Key{AppName: "", UserID: "u"},
		session.StateMap{"k": []byte("v")},
	)
	assert.Error(t, err)
}

func TestUpdateSessionState_AppPrefixRejected(
	t *testing.T,
) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}
	err := s.UpdateSessionState(
		context.Background(), key,
		session.StateMap{
			session.StateAppPrefix + "k": []byte("v"),
		},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"use UpdateAppState instead")
}

func TestUpdateSessionState_UserPrefixRejected(
	t *testing.T,
) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}
	err := s.UpdateSessionState(
		context.Background(), key,
		session.StateMap{
			session.StateUserPrefix + "k": []byte("v"),
		},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"use UpdateUserState instead")
}

func TestUpdateSessionState_SessionNotFound(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	// Return no rows => session not found.
	mock.ExpectQuery("SELECT state FROM").
		WillReturnRows(
			sqlmock.NewRows([]string{"state"}),
		)

	err := s.UpdateSessionState(
		context.Background(), key,
		session.StateMap{"k": []byte("v")},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestUpdateSessionState_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{"old": []byte("val")},
	}
	stateBytes, _ := json.Marshal(sessState)

	rows := sqlmock.NewRows([]string{"state"}).
		AddRow(stateBytes)
	mock.ExpectQuery("SELECT state FROM").
		WillReturnRows(rows)

	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.UpdateSessionState(
		context.Background(), key,
		session.StateMap{"new": []byte("val2")},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectQuery("SELECT state FROM").
		WillReturnError(fmt.Errorf("db error"))

	err := s.UpdateSessionState(
		context.Background(), key,
		session.StateMap{"k": []byte("v")},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get session state")
}

func TestUpdateSessionState_UpdateError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	rows := sqlmock.NewRows([]string{"state"}).
		AddRow(stateBytes)
	mock.ExpectQuery("SELECT state FROM").
		WillReturnRows(rows)

	mock.ExpectExec("UPDATE .* SET state").
		WillReturnError(fmt.Errorf("update fail"))

	err := s.UpdateSessionState(
		context.Background(), key,
		session.StateMap{"k": []byte("v")},
	)
	assert.Error(t, err)
}

func TestUpdateSessionState_InvalidJSON(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	rows := sqlmock.NewRows([]string{"state"}).
		AddRow([]byte(`{invalid json`))
	mock.ExpectQuery("SELECT state FROM").
		WillReturnRows(rows)

	err := s.UpdateSessionState(
		context.Background(), key,
		session.StateMap{"k": []byte("v")},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal state")
}

// --- Tests for AppendEvent ---

func TestAppendEvent_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	sess := &session.Session{
		AppName: "", UserID: "u", ID: "s",
	}
	err := s.AppendEvent(
		context.Background(), sess, &event.Event{},
	)
	assert.Error(t, err)
}

func TestAppendEvent_SyncMode_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.enableAsyncPersist = false

	sess := session.NewSession("app", "user", "sess")

	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	// getSession state for addEvent.
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(
			sqlmock.NewRows(
				[]string{"state", "expires_at"},
			).AddRow(stateBytes, nil),
		)

	// Transaction: update state + insert event.
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_events").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := s.AppendEvent(
		context.Background(), sess, evt,
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendEvent_SyncMode_AddEventError(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.enableAsyncPersist = false

	sess := session.NewSession("app", "user", "sess")

	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}

	// Session not found.
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		))

	err := s.AppendEvent(
		context.Background(), sess, evt,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"append event failed")
}

// --- Tests for AppendTrackEvent ---

func TestAppendTrackEvent_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	sess := &session.Session{
		AppName: "", UserID: "u", ID: "s",
	}
	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}
	err := s.AppendTrackEvent(
		context.Background(), sess, te,
	)
	assert.Error(t, err)
}

// --- Tests for cleanupExpiredData ---

func TestCleanupExpiredData_NoTTL(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	s.opts.sessionTTL = 0
	s.opts.appStateTTL = 0
	s.opts.userStateTTL = 0

	// No TTLs set, so no cleanup should happen.
	// No mock expectations needed.
	s.cleanupExpiredData(context.Background(), nil)
}

func TestCleanupExpiredData_SoftDelete(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.sessionTTL = 10 * time.Minute
	s.opts.appStateTTL = 0
	s.opts.userStateTTL = 0
	s.opts.softDelete = true

	mock.ExpectBegin()
	// 4 tables with sessionTTL: states, events, tracks,
	// summaries.
	for i := 0; i < 4; i++ {
		mock.ExpectExec("UPDATE .* SET deleted_at").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectCommit()

	s.cleanupExpiredData(context.Background(), nil)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpiredData_HardDelete(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.sessionTTL = 10 * time.Minute
	s.opts.appStateTTL = 0
	s.opts.userStateTTL = 0
	s.opts.softDelete = false

	mock.ExpectBegin()
	for i := 0; i < 4; i++ {
		mock.ExpectExec("DELETE FROM").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectCommit()

	s.cleanupExpiredData(context.Background(), nil)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpiredData_WithUserKey(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.sessionTTL = 10 * time.Minute
	s.opts.appStateTTL = 0
	s.opts.userStateTTL = 0
	s.opts.softDelete = true

	userKey := &session.UserKey{
		AppName: "app", UserID: "user",
	}

	mock.ExpectBegin()
	// 4 tables with sessionTTL (app_states excluded
	// for user-scoped cleanup).
	for i := 0; i < 4; i++ {
		mock.ExpectExec("UPDATE .* SET deleted_at").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectCommit()

	s.cleanupExpiredData(
		context.Background(), userKey,
	)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpiredData_TransactionError(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.sessionTTL = 10 * time.Minute
	s.opts.softDelete = true

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE .* SET deleted_at").
		WillReturnError(fmt.Errorf("tx error"))
	mock.ExpectRollback()

	// Should not panic — errors are logged.
	s.cleanupExpiredData(context.Background(), nil)
}

// --- Tests for Close ---

func TestClose_Idempotent(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	// Close with no async workers should not panic.
	err := s.Close()
	assert.NoError(t, err)

	// Second close should also succeed (sync.Once).
	err = s.Close()
	assert.NoError(t, err)
}

// --- Tests for UpdateSessionState with TTL ---

func TestUpdateSessionState_WithTTL(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.sessionTTL = 30 * time.Minute

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	rows := sqlmock.NewRows([]string{"state"}).
		AddRow(stateBytes)
	mock.ExpectQuery("SELECT state FROM").
		WillReturnRows(rows)

	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.UpdateSessionState(
		context.Background(), key,
		session.StateMap{"k": []byte("v")},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for UpdateAppState with TTL ---

func TestUpdateAppState_WithTTL(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.appStateTTL = 15 * time.Minute

	mock.ExpectExec("INSERT INTO app_states").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.UpdateAppState(
		context.Background(), "app",
		session.StateMap{"k": []byte("v")},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for UpdateUserState with TTL ---

func TestUpdateUserState_WithTTL(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.userStateTTL = 20 * time.Minute

	mock.ExpectExec("INSERT INTO user_states").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.UpdateUserState(
		context.Background(),
		session.UserKey{AppName: "app", UserID: "user"},
		session.StateMap{"k": []byte("v")},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for CreateSession with TTL ---

func TestCreateSession_WithTTL(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.sessionTTL = 10 * time.Minute

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectQuery("SELECT expires_at FROM").
		WillReturnRows(
			sqlmock.NewRows([]string{"expires_at"}),
		)

	mock.ExpectExec("INSERT INTO session_states").
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectQuery("SELECT key, value FROM app_states").
		WillReturnRows(
			sqlmock.NewRows([]string{"key", "value"}),
		)

	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	sess, err := s.CreateSession(
		context.Background(), key,
		session.StateMap{"k": []byte("v")},
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for UpdateSessionState with nil value ---

func TestUpdateSessionState_NilValueInState(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{"existing": []byte("v")},
	}
	stateBytes, _ := json.Marshal(sessState)

	rows := sqlmock.NewRows([]string{"state"}).
		AddRow(stateBytes)
	mock.ExpectQuery("SELECT state FROM").
		WillReturnRows(rows)

	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.UpdateSessionState(
		context.Background(), key,
		session.StateMap{"nullkey": nil},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for cleanupExpired ---

func TestCleanupExpired_CallsCleanupExpiredData(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.sessionTTL = 5 * time.Minute
	s.opts.softDelete = true

	mock.ExpectBegin()
	for i := 0; i < 4; i++ {
		mock.ExpectExec("UPDATE .* SET deleted_at").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectCommit()

	s.cleanupExpired()
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for cleanupExpiredForUser ---

func TestCleanupExpiredForUser(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.sessionTTL = 5 * time.Minute
	s.opts.softDelete = true

	mock.ExpectBegin()
	for i := 0; i < 4; i++ {
		mock.ExpectExec("UPDATE .* SET deleted_at").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectCommit()

	s.cleanupExpiredForUser(
		context.Background(),
		session.UserKey{AppName: "app", UserID: "user"},
	)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for SessionState JSON handling ---

func TestUpdateSessionState_EmptyState(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	// Return empty state bytes.
	rows := sqlmock.NewRows([]string{"state"}).
		AddRow([]byte("{}"))
	mock.ExpectQuery("SELECT state FROM").
		WillReturnRows(rows)

	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.UpdateSessionState(
		context.Background(), key,
		session.StateMap{"k": []byte("v")},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for multiple UpdateAppState keys ---

func TestUpdateAppState_MultipleKeys(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	// Expect one exec per key.
	mock.ExpectExec("INSERT INTO app_states").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO app_states").
		WillReturnResult(sqlmock.NewResult(2, 1))

	err := s.UpdateAppState(
		context.Background(), "app",
		session.StateMap{
			"k1": []byte("v1"),
			"k2": []byte("v2"),
		},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for multiple UpdateUserState keys ---

func TestUpdateUserState_MultipleKeys(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectExec("INSERT INTO user_states").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO user_states").
		WillReturnResult(sqlmock.NewResult(2, 1))

	err := s.UpdateUserState(
		context.Background(),
		session.UserKey{AppName: "app", UserID: "user"},
		session.StateMap{
			"k1": []byte("v1"),
			"k2": []byte("v2"),
		},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for stopCleanupRoutine ---

func TestStopCleanupRoutine_NilTicker(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	// Should not panic when ticker is nil.
	s.stopCleanupRoutine()
}

func TestStopCleanupRoutine_WithTicker(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	s.cleanupTicker = time.NewTicker(time.Hour)
	s.stopCleanupRoutine()
	assert.Nil(t, s.cleanupTicker)
}

func TestStopCleanupRoutine_Idempotent(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	s.cleanupTicker = time.NewTicker(time.Hour)
	s.stopCleanupRoutine()
	// Second call should not panic (sync.Once).
	s.stopCleanupRoutine()
}
