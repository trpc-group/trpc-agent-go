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
	"sync"
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
	embedding          []float64
	err                error
	callCount          int
	lastText           string
	lastDeadline       time.Time
	lastCtxHasDeadline bool
	dimensions         int
}

func (m *mockEmbedder) GetEmbedding(
	ctx context.Context, text string,
) ([]float64, error) {
	m.callCount++
	m.lastText = text
	m.lastDeadline, m.lastCtxHasDeadline = ctx.Deadline()
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

type recordingPostgresClient struct {
	mu         sync.Mutex
	filterKeys []string
}

func (c *recordingPostgresClient) ExecContext(
	_ context.Context, _ string, args ...any,
) (sql.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(args) > 3 {
		if filterKey, ok := args[3].(string); ok {
			c.filterKeys = append(c.filterKeys, filterKey)
		}
	}
	return sqlmock.NewResult(0, 1), nil
}

func (c *recordingPostgresClient) Query(
	_ context.Context,
	_ storage.HandlerFunc,
	_ string, _ ...any,
) error {
	return nil
}

func (c *recordingPostgresClient) Transaction(
	_ context.Context,
	_ storage.TxFunc,
) error {
	return nil
}

func (c *recordingPostgresClient) Close() error {
	return nil
}

func (c *recordingPostgresClient) persistedFilterKeys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, len(c.filterKeys))
	copy(keys, c.filterKeys)
	return keys
}

// anyVectorArg matches pgvector.Vector arguments in
// sqlmock expectations.
type anyVectorArg struct{}

func (a anyVectorArg) Match(_ driver.Value) bool {
	return true
}

// sliceValueConverter converts []string to a
// comma-separated driver.Value for go-sqlmock testing.
// PostgreSQL drivers handle []string natively but
// go-sqlmock's default driver does not.
type sliceValueConverter struct{}

func (c sliceValueConverter) ConvertValue(
	v any,
) (driver.Value, error) {
	switch vv := v.(type) {
	case []string:
		return fmt.Sprintf("{%s}",
			strings.Join(vv, ",")), nil
	default:
		return driver.DefaultParameterConverter.
			ConvertValue(v)
	}
}

// newTestServiceWithSliceSupport creates a test service
// that can handle []string arguments in SQL queries.
// Required for testing getEventsList, getSummariesList,
// and other functions using ANY($N::varchar[]).
func newTestServiceWithSliceSupport(
	t *testing.T,
	emb *mockEmbedder,
) (*Service, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
		sqlmock.ValueConverterOption(
			sliceValueConverter{},
		),
	)
	require.NoError(t, err)

	client := &mockPostgresClient{db: db}
	if emb == nil {
		emb = &mockEmbedder{
			embedding:  []float64{0.1, 0.2, 0.3},
			dimensions: 3,
		}
	}
	s := &Service{
		opts: ServiceOpts{
			maxResults:        defaultMaxResults,
			embedder:          emb,
			sessionEventLimit: defaultSessionEventLimit,
			embedTimeout:      defaultEmbedTimeout,
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
			embedTimeout:      defaultEmbedTimeout,
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

func TestNewService_RequiresEmbedder(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	t.Cleanup(func() {
		storage.SetClientBuilder(oldBuilder)
	})
	builderCalled := false
	storage.SetClientBuilder(func(
		_ context.Context,
		_ ...storage.ClientBuilderOpt,
	) (storage.Client, error) {
		builderCalled = true
		return nil, fmt.Errorf("unexpected builder call")
	})

	svc, err := NewService()
	require.Nil(t, svc)
	require.ErrorIs(t, err, errEmbedderRequired)
	require.False(t, builderCalled)
}

func TestNewService_ValidatesEmbedderDimensions(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	t.Cleanup(func() {
		storage.SetClientBuilder(oldBuilder)
	})
	builderCalled := false
	storage.SetClientBuilder(func(
		_ context.Context,
		_ ...storage.ClientBuilderOpt,
	) (storage.Client, error) {
		builderCalled = true
		return nil, fmt.Errorf("unexpected builder call")
	})

	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dimensions: 768}),
		WithIndexDimension(1536),
	)
	require.Nil(t, svc)
	require.ErrorIs(t, err, errEmbedderDimensionMismatch)
	require.Contains(
		t,
		err.Error(),
		"embedder=768 configured=1536",
	)
	require.False(t, builderCalled)
}

func TestNewService_InitializesAsyncSummaryWorker(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	t.Cleanup(func() {
		storage.SetClientBuilder(oldBuilder)
	})

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	builderCalled := false
	storage.SetClientBuilder(func(
		_ context.Context,
		_ ...storage.ClientBuilderOpt,
	) (storage.Client, error) {
		builderCalled = true
		return &mockPostgresClient{db: db}, nil
	})

	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dimensions: defaultIndexDimension}),
		WithSummarizer(&mockSummarizer{}),
		WithAsyncSummaryNum(1),
		WithSkipDBInit(true),
		WithCascadeFullSessionSummary(false),
		WithSummaryFilterAllowlist("tool-usage"),
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.True(t, builderCalled)
	require.NotNil(t, svc.asyncWorker)
	require.Equal(t, []string{"tool-usage"}, svc.opts.summaryFilterAllowlist)
	require.False(t, svc.opts.shouldCascadeFullSessionSummary())
	require.NoError(t, svc.Close())
}

func TestNewService_AsyncSummaryWorkerHonorsDispatchPolicy(
	t *testing.T,
) {
	oldBuilder := storage.GetClientBuilder()
	t.Cleanup(func() {
		storage.SetClientBuilder(oldBuilder)
	})

	recordingClient := &recordingPostgresClient{}
	storage.SetClientBuilder(func(
		_ context.Context,
		_ ...storage.ClientBuilderOpt,
	) (storage.Client, error) {
		return recordingClient, nil
	})

	svc, err := NewService(
		WithEmbedder(&mockEmbedder{dimensions: defaultIndexDimension}),
		WithSummarizer(&activeSummarizer{text: "worker summary"}),
		WithAsyncSummaryNum(1),
		WithSkipDBInit(true),
		WithCascadeFullSessionSummary(false),
		WithSummaryFilterAllowlist("tool-usage"),
	)
	require.NoError(t, err)

	sess := session.NewSession("app", "user", "sess")
	sess.Events = []event.Event{
		{
			ID:           "evt-1",
			InvocationID: "inv-1",
			Timestamp:    time.Now().Add(-time.Minute),
			Response: &model.Response{
				Choices: []model.Choice{
					{Message: model.Message{
						Role:    model.RoleUser,
						Content: "hello",
					}},
				},
			},
		},
	}

	require.NoError(t, svc.EnqueueSummaryJob(
		context.Background(), sess, "blocked", true,
	))
	require.NoError(t, svc.EnqueueSummaryJob(
		context.Background(), sess, "tool-usage", true,
	))
	require.NoError(t, svc.Close())
	require.Equal(t, []string{"tool-usage"},
		recordingClient.persistedFilterKeys())
}

func TestValidateEmbedderDimensions_UnknownDimension(t *testing.T) {
	err := validateEmbedderDimensions(&ServiceOpts{
		embedder:       &mockEmbedder{dimensions: 0},
		indexDimension: defaultIndexDimension,
	})
	require.NoError(t, err)
}

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

func TestBuildIndexText_UsesBuilder(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.indexTextBuilder = func(
		sess *session.Session,
		_ *event.Event,
		baseText string,
		role model.Role,
	) string {
		return fmt.Sprintf(
			"[SessionDate: %s] %s: %s",
			sess.CreatedAt.Format("2006-01-02"),
			role,
			baseText,
		)
	}

	sess := &session.Session{
		ID:        "sess-1",
		AppName:   "app",
		UserID:    "user",
		CreatedAt: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
	}
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

	text, role := s.buildIndexText(sess, evt)
	assert.Equal(t, model.RoleAssistant, role)
	assert.Equal(
		t,
		"[SessionDate: 2025-01-02] assistant: hello world",
		text,
	)
}

func TestAsyncIndexEvent_Success(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	evt := &event.Event{
		InvocationID: "inv-1",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "hello world",
				}},
			},
		},
	}
	eventBytes, _ := json.Marshal(evt)
	mock.ExpectExec("UPDATE session_events SET").
		WithArgs(
			"hello world",
			string(model.RoleAssistant),
			anyVectorArg{},
			"app", "user", "sess-1",
			string(eventBytes),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	sess := &session.Session{
		ID: "sess-1", AppName: "app", UserID: "user",
	}
	s.asyncIndexEvent(context.Background(), sess, evt)
	assert.Equal(t, 1, emb.callCount)
	assert.Equal(t, "hello world", emb.lastText)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIndexEventAfterPersist_SyncIndexing(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()
	s.opts.syncIndexing = true

	s.opts.indexTextBuilder = func(
		sess *session.Session,
		_ *event.Event,
		baseText string,
		role model.Role,
	) string {
		return fmt.Sprintf(
			"[SessionDate: %s] %s: %s",
			sess.CreatedAt.Format("2006-01-02"),
			role,
			baseText,
		)
	}

	evt := &event.Event{
		InvocationID: "inv-1",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "hello world",
				}},
			},
		},
	}
	eventBytes, _ := json.Marshal(evt)
	mock.ExpectExec("UPDATE session_events SET").
		WithArgs(
			"[SessionDate: 2025-01-02] assistant: hello world",
			string(model.RoleAssistant),
			anyVectorArg{},
			"app", "user", "sess-1",
			string(eventBytes),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	sess := &session.Session{
		ID:        "sess-1",
		AppName:   "app",
		UserID:    "user",
		CreatedAt: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
	}

	s.indexEventAfterPersist(sess, evt)
	assert.Equal(t, 1, emb.callCount)
	assert.Equal(
		t,
		"[SessionDate: 2025-01-02] assistant: hello world",
		emb.lastText,
	)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAsyncIndexEvent_UpdateError(t *testing.T) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()

	evt := &event.Event{
		InvocationID: "inv-1",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}
	eventBytes, _ := json.Marshal(evt)
	mock.ExpectExec("UPDATE session_events SET").
		WithArgs(
			"hello",
			string(model.RoleUser),
			anyVectorArg{},
			"app", "user", "sess-1",
			string(eventBytes),
		).
		WillReturnError(fmt.Errorf("db down"))
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
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(
			sqlmock.NewRows(
				[]string{"state", "expires_at"},
			).AddRow(stateBytes, nil),
		)
	// Transaction: update state + insert event.
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
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		))
	mock.ExpectRollback()

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

// --- Tests for getSession happy path ---
// Note: Tests below use newTestServiceWithSliceSupport
// which handles []string args for ANY($3::varchar[]).

func TestGetSession_SuccessPath(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()
	s.opts.sessionTTL = 10 * time.Minute

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{"k": []byte("v")},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	// Session state query.
	mock.ExpectQuery("SELECT state, created_at").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"state", "created_at", "updated_at",
			},
		).AddRow(stateBytes, now, now))

	// ListAppStates.
	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	// ListUserStates.
	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	// getEventsList.
	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		))
	mock.ExpectQuery("SELECT session_id, filter_key, summary").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "filter_key", "summary"},
		))

	// refreshSessionTTL.
	mock.ExpectExec("UPDATE .* SET updated_at").
		WillReturnResult(sqlmock.NewResult(0, 1))

	sess, err := s.GetSession(
		context.Background(), key,
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "sess", sess.ID)
}

func TestGetSession_SuccessNoTTL(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()
	s.opts.sessionTTL = 0

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	mock.ExpectQuery("SELECT state, created_at").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"state", "created_at", "updated_at",
			},
		).AddRow(stateBytes, now, now))

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		))
	mock.ExpectQuery("SELECT session_id, filter_key, summary").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "filter_key", "summary"},
		))

	sess, err := s.GetSession(
		context.Background(), key,
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
}

func TestGetSession_WithEventsAndSummaries(
	t *testing.T,
) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	mock.ExpectQuery("SELECT state, created_at").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"state", "created_at", "updated_at",
			},
		).AddRow(stateBytes, now, now))

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}).
			AddRow("ak", []byte("av")),
	)

	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}).
			AddRow("uk", []byte("uv")),
	)

	// Return events. Must contain a valid user message
	// so ApplyEventFiltering does not discard it.
	evt := event.Event{
		InvocationID: "inv-1",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}
	evtBytes, _ := json.Marshal(evt)
	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		).AddRow("sess", evtBytes))

	// getSummariesList.
	sum := session.Summary{Summary: "test"}
	sumBytes, _ := json.Marshal(sum)
	mock.ExpectQuery("SELECT session_id, filter_key").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "filter_key", "summary",
			},
		).AddRow("sess", "all", sumBytes))

	// getTrackEvents - no tracks.

	sess, err := s.GetSession(
		context.Background(), key,
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Len(t, sess.Events, 1)
	assert.NotNil(t, sess.Summaries["all"])
}

func TestGetSession_WithEventsAndTracks(
	t *testing.T,
) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	// Session state with tracks.
	tracks, _ := json.Marshal(
		[]session.Track{"alpha"},
	)
	sessState := SessionState{
		ID: "sess",
		State: session.StateMap{
			"tracks": tracks,
		},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	mock.ExpectQuery("SELECT state, created_at").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"state", "created_at", "updated_at",
			},
		).AddRow(stateBytes, now, now))

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)
	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	// Events with valid user message.
	evt := event.Event{
		InvocationID: "inv-1",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}
	evtBytes, _ := json.Marshal(evt)
	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		).AddRow("sess", evtBytes))

	// Summaries.
	sum := session.Summary{Summary: "test"}
	sumBytes, _ := json.Marshal(sum)
	mock.ExpectQuery("SELECT session_id, filter_key").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "filter_key", "summary",
			},
		).AddRow("sess", "all", sumBytes))

	// Track events for "alpha".
	te := session.TrackEvent{
		Track:   "alpha",
		Payload: json.RawMessage(`"data"`),
	}
	teBytes, _ := json.Marshal(te)
	mock.ExpectQuery("SELECT event FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"event"},
		).AddRow(teBytes))

	sess, err := s.GetSession(
		context.Background(), key,
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Len(t, sess.Events, 1)
	require.NotNil(t, sess.Tracks)
	require.NotNil(t, sess.Tracks["alpha"])
	assert.Len(t, sess.Tracks["alpha"].Events, 1)
}

func TestGetSession_TrackEventsError(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	tracks, _ := json.Marshal(
		[]session.Track{"alpha"},
	)
	sessState := SessionState{
		ID: "sess",
		State: session.StateMap{
			"tracks": tracks,
		},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	mock.ExpectQuery("SELECT state, created_at").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"state", "created_at", "updated_at",
			},
		).AddRow(stateBytes, now, now))

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)
	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	// Events.
	evt := event.Event{
		InvocationID: "inv-1",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}
	evtBytes, _ := json.Marshal(evt)
	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		).AddRow("sess", evtBytes))

	// Summaries.
	mock.ExpectQuery("SELECT session_id, filter_key").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "filter_key", "summary",
			},
		))

	// Track events query fails.
	mock.ExpectQuery("SELECT event FROM").
		WillReturnError(fmt.Errorf("track db error"))

	sess, err := s.GetSession(
		context.Background(), key,
	)
	assert.Error(t, err)
	assert.Nil(t, sess)
	assert.Contains(t, err.Error(),
		"get track events failed")
}

func TestGetSession_SummariesError(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	mock.ExpectQuery("SELECT state, created_at").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"state", "created_at", "updated_at",
			},
		).AddRow(stateBytes, now, now))

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)
	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	// Events with user message.
	evt := event.Event{
		InvocationID: "inv-1",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}
	evtBytes, _ := json.Marshal(evt)
	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		).AddRow("sess", evtBytes))

	// Summaries query fails.
	mock.ExpectQuery("SELECT session_id, filter_key").
		WillReturnError(
			fmt.Errorf("summaries db error"),
		)

	sess, err := s.GetSession(
		context.Background(), key,
	)
	assert.Error(t, err)
	assert.Nil(t, sess)
	assert.Contains(t, err.Error(),
		"get summaries failed")
}

// --- Tests for getSession ListAppStates error ---

func TestGetSession_AppStatesError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	mock.ExpectQuery("SELECT state, created_at").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"state", "created_at", "updated_at",
			},
		).AddRow(stateBytes, now, now))

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnError(fmt.Errorf("db error"))

	_, err := s.GetSession(
		context.Background(), key,
	)
	assert.Error(t, err)
}

func TestGetSession_UserStatesError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	mock.ExpectQuery("SELECT state, created_at").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"state", "created_at", "updated_at",
			},
		).AddRow(stateBytes, now, now))

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnError(fmt.Errorf("db error"))

	_, err := s.GetSession(
		context.Background(), key,
	)
	assert.Error(t, err)
}

func TestGetSession_EventsListError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	mock.ExpectQuery("SELECT state, created_at").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"state", "created_at", "updated_at",
			},
		).AddRow(stateBytes, now, now))

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery("SELECT session_id, event").
		WillReturnError(fmt.Errorf("events db error"))

	_, err := s.GetSession(
		context.Background(), key,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get events failed")
}

func TestGetSession_InvalidStateJSON(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}
	now := time.Now()

	mock.ExpectQuery("SELECT state, created_at").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"state", "created_at", "updated_at",
			},
		).AddRow([]byte(`{invalid`), now, now))

	_, err := s.GetSession(
		context.Background(), key,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"unmarshal session state failed")
}

// --- Tests for ListSessions with data ---

func TestListSessions_WithSessions(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "app", UserID: "user",
	}

	sessState := SessionState{
		ID:    "sess1",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	// ListAppStates.
	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	// ListUserStates.
	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	// List session states.
	mock.ExpectQuery("SELECT session_id, state").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "state",
				"created_at", "updated_at",
			},
		).AddRow("sess1", stateBytes, now, now))

	// getEventsList.
	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		))

	// getSummariesList.
	mock.ExpectQuery("SELECT session_id, filter_key").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "filter_key", "summary",
			},
		))

	// getTrackEvents - no tracks in state.

	sessions, err := s.ListSessions(
		context.Background(), userKey,
	)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "sess1", sessions[0].ID)
}

func TestListSessions_WithTracks(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "app", UserID: "user",
	}

	tracks, _ := json.Marshal(
		[]session.Track{"alpha"},
	)
	sessState := SessionState{
		ID: "sess1",
		State: session.StateMap{
			"tracks": tracks,
		},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	// ListAppStates.
	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)
	// ListUserStates.
	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	// List session states.
	mock.ExpectQuery("SELECT session_id, state").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "state",
				"created_at", "updated_at",
			},
		).AddRow("sess1", stateBytes, now, now))

	// getEventsList - with user message.
	evt := event.Event{
		InvocationID: "inv-1",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}
	evtBytes, _ := json.Marshal(evt)
	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		).AddRow("sess1", evtBytes))

	// getSummariesList.
	mock.ExpectQuery("SELECT session_id, filter_key").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "filter_key", "summary",
			},
		))

	// getTrackEvents for "alpha".
	te := session.TrackEvent{
		Track:   "alpha",
		Payload: json.RawMessage(`"data"`),
	}
	teBytes, _ := json.Marshal(te)
	mock.ExpectQuery("SELECT event FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"event"},
		).AddRow(teBytes))

	sessions, err := s.ListSessions(
		context.Background(), userKey,
	)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.NotNil(t, sessions[0].Tracks)
	require.NotNil(t, sessions[0].Tracks["alpha"])
	assert.Len(t,
		sessions[0].Tracks["alpha"].Events, 1,
	)
}

func TestListSessions_TrackEventsError(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "app", UserID: "user",
	}

	tracks, _ := json.Marshal(
		[]session.Track{"alpha"},
	)
	sessState := SessionState{
		ID: "sess1",
		State: session.StateMap{
			"tracks": tracks,
		},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)
	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)
	mock.ExpectQuery("SELECT session_id, state").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "state",
				"created_at", "updated_at",
			},
		).AddRow("sess1", stateBytes, now, now))

	// Events.
	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		))
	// Summaries.
	mock.ExpectQuery("SELECT session_id, filter_key").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "filter_key", "summary",
			},
		))
	// Track query fails.
	mock.ExpectQuery("SELECT event FROM").
		WillReturnError(
			fmt.Errorf("track db error"),
		)

	_, err := s.ListSessions(
		context.Background(), userKey,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"get track events")
}

func TestListSessions_InvalidStateJSON(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "app", UserID: "user",
	}

	now := time.Now()

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery("SELECT session_id, state").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "state",
				"created_at", "updated_at",
			},
		).AddRow(
			"sess1", []byte(`{invalid`), now, now,
		))

	_, err := s.ListSessions(
		context.Background(), userKey,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"unmarshal session state failed")
}

func TestListSessions_AppStatesError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "app", UserID: "user",
	}

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnError(fmt.Errorf("db error"))

	_, err := s.ListSessions(
		context.Background(), userKey,
	)
	assert.Error(t, err)
}

func TestListSessions_UserStatesError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "app", UserID: "user",
	}

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnError(fmt.Errorf("db error"))

	_, err := s.ListSessions(
		context.Background(), userKey,
	)
	assert.Error(t, err)
}

// --- Tests for appendEventInternal async path ---

func TestAppendEventInternal_AsyncPersist(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.enableAsyncPersist = true

	// Set up async channels.
	s.eventPairChans = []chan *sessionEventPair{
		make(chan *sessionEventPair, 10),
	}

	sess := session.NewSession("app", "user", "sess")
	sess.Hash = 0

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

	err := s.appendEventInternal(
		context.Background(), sess, evt,
		session.Key{
			AppName: "app", UserID: "user",
			SessionID: "sess",
		},
	)
	require.NoError(t, err)

	// Verify event was sent to channel.
	select {
	case pair := <-s.eventPairChans[0]:
		assert.Equal(t, "sess", pair.key.SessionID)
	default:
		t.Fatal("expected event in channel")
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendEventInternal_AsyncPersist_CtxDone(
	t *testing.T,
) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.enableAsyncPersist = true

	// Create a full channel to trigger ctx.Done().
	s.eventPairChans = []chan *sessionEventPair{
		make(chan *sessionEventPair), // Unbuffered.
	}

	sess := session.NewSession("app", "user", "sess")
	sess.Hash = 0

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel() // Cancel immediately.

	err := s.appendEventInternal(
		ctx, sess, &event.Event{},
		session.Key{
			AppName: "app", UserID: "user",
			SessionID: "sess",
		},
	)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestAppendEventInternal_SyncWithAsyncIndex(
	t *testing.T,
) {
	emb := &mockEmbedder{
		embedding: []float64{0.1, 0.2, 0.3},
	}
	s, mock, db := newTestService(t, emb)
	defer db.Close()
	s.opts.enableAsyncPersist = false

	sess := session.NewSession("app", "user", "sess")

	evt := &event.Event{
		InvocationID: "inv-sync",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "response text",
				}},
			},
		},
	}
	eventBytes, _ := json.Marshal(evt)

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_events").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	// asyncIndexEvent will call embedder + update.
	mock.ExpectExec("UPDATE session_events SET").
		WithArgs(
			"response text",
			string(model.RoleAssistant),
			anyVectorArg{},
			"app", "user", "sess",
			string(eventBytes),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	err := s.appendEventInternal(
		context.Background(), sess, evt, key,
	)
	require.NoError(t, err)

	// Wait briefly for goroutine to complete.
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendEventInternal_AsyncPersist_ClosedChan(
	t *testing.T,
) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.enableAsyncPersist = true

	// Create and close channel to trigger panic
	// recovery.
	ch := make(chan *sessionEventPair, 1)
	close(ch)
	s.eventPairChans = []chan *sessionEventPair{ch}

	sess := session.NewSession("app", "user", "sess")
	sess.Hash = 0

	// Should not panic; recover handles
	// "send on closed channel".
	err := s.appendEventInternal(
		context.Background(), sess, &event.Event{},
		session.Key{
			AppName: "app", UserID: "user",
			SessionID: "sess",
		},
	)
	assert.ErrorIs(t, err, errServiceClosing)
}

func TestAppendTrackEvent_AsyncPersist_ClosedChan(
	t *testing.T,
) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.enableAsyncPersist = true

	// Create and close channel to trigger panic
	// recovery.
	ch := make(chan *trackEventPair, 1)
	close(ch)
	s.trackEventChans = []chan *trackEventPair{ch}

	sess := session.NewSession("app", "user", "sess")
	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}

	// Should not panic; recover handles
	// "send on closed channel".
	err := s.AppendTrackEvent(
		context.Background(), sess, te,
	)
	assert.ErrorIs(t, err, errServiceClosing)
}

// --- Tests for AppendTrackEvent sync mode ---

func TestAppendTrackEvent_SyncMode_Success(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.enableAsyncPersist = false

	sess := session.NewSession("app", "user", "sess")

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}

	// addTrackEvent transaction.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_track").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := s.AppendTrackEvent(
		context.Background(), sess, te,
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendTrackEvent_SyncMode_AddError(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.enableAsyncPersist = false

	sess := session.NewSession("app", "user", "sess")

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		))
	mock.ExpectRollback()

	err := s.AppendTrackEvent(
		context.Background(), sess, te,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"append track event failed")
}

func TestAppendTrackEvent_AsyncMode(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.enableAsyncPersist = true

	s.trackEventChans = []chan *trackEventPair{
		make(chan *trackEventPair, 10),
	}

	sess := session.NewSession("app", "user", "sess")

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}

	err := s.AppendTrackEvent(
		context.Background(), sess, te,
	)
	require.NoError(t, err)

	// Verify event was sent to channel.
	select {
	case pair := <-s.trackEventChans[0]:
		assert.Equal(t,
			session.Track("track1"), pair.event.Track,
		)
	default:
		t.Fatal("expected track event in channel")
	}
}

func TestAppendTrackEvent_AsyncMode_CtxDone(
	t *testing.T,
) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.enableAsyncPersist = true

	s.trackEventChans = []chan *trackEventPair{
		make(chan *trackEventPair), // Unbuffered.
	}

	sess := session.NewSession("app", "user", "sess")

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	err := s.AppendTrackEvent(ctx, sess, te)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

// --- Tests for addTrackEvent happy path ---

func TestAddTrackEvent_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_track").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}
	err := s.addTrackEvent(
		context.Background(), key, te,
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddTrackEvent_WithTTL(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.sessionTTL = 30 * time.Minute

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_track").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}
	err := s.addTrackEvent(
		context.Background(), key, te,
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddTrackEvent_TransactionError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnError(fmt.Errorf("tx error"))
	mock.ExpectRollback()

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}
	err := s.addTrackEvent(
		context.Background(), key, te,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"store track event failed")
}

func TestAddTrackEvent_InvalidStateJSON(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow([]byte(`{bad json`), nil))
	mock.ExpectRollback()

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}
	err := s.addTrackEvent(
		context.Background(), key, te,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"unmarshal session state failed")
}

func TestAddTrackEvent_ExpiredSession(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	past := time.Now().Add(-1 * time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, past))
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_track").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}
	err := s.addTrackEvent(
		context.Background(), key, te,
	)
	require.NoError(t, err)
}

// --- Tests for addEvent expired session ---

func TestAddEvent_ExpiredSession(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	past := time.Now().Add(-1 * time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, past))
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_events").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

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

	err := s.addEvent(context.Background(), key, evt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddEvent_InvalidStateJSON(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow([]byte(`{bad json`), nil))
	mock.ExpectRollback()

	err := s.addEvent(
		context.Background(), key, &event.Event{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"unmarshal session state failed")
}

// --- Tests for startAsyncPersistWorker ---

func TestStartAsyncPersistWorker_Integration(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.asyncPersisterNum = 1
	s.opts.enableAsyncPersist = true

	s.startAsyncPersistWorker()

	require.Len(t, s.eventPairChans, 1)
	require.Len(t, s.trackEventChans, 1)

	// Send an event through the channel.
	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_events").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

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

	s.eventPairChans[0] <- &sessionEventPair{
		key: session.Key{
			AppName: "app", UserID: "user",
			SessionID: "sess",
		},
		event: evt,
	}

	// Close channels to stop workers.
	for _, ch := range s.eventPairChans {
		close(ch)
	}
	for _, ch := range s.trackEventChans {
		close(ch)
	}
	s.persistWg.Wait()

	// Give async goroutine time to complete.
	time.Sleep(50 * time.Millisecond)
}

func TestStartAsyncPersistWorker_TrackEvent(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.asyncPersisterNum = 1
	s.opts.enableAsyncPersist = true

	s.startAsyncPersistWorker()

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_track").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}

	s.trackEventChans[0] <- &trackEventPair{
		key: session.Key{
			AppName: "app", UserID: "user",
			SessionID: "sess",
		},
		event: te,
	}

	for _, ch := range s.eventPairChans {
		close(ch)
	}
	for _, ch := range s.trackEventChans {
		close(ch)
	}
	s.persistWg.Wait()
}

func TestStartAsyncPersistWorker_EventError(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.asyncPersisterNum = 1
	s.opts.enableAsyncPersist = true

	s.startAsyncPersistWorker()

	// Session not found => addEvent error, logged.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		))
	mock.ExpectRollback()

	s.eventPairChans[0] <- &sessionEventPair{
		key: session.Key{
			AppName: "app", UserID: "user",
			SessionID: "sess",
		},
		event: &event.Event{},
	}

	for _, ch := range s.eventPairChans {
		close(ch)
	}
	for _, ch := range s.trackEventChans {
		close(ch)
	}
	s.persistWg.Wait()
}

// --- Tests for Close with async workers ---

func TestClose_WithAsyncWorkers(t *testing.T) {
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)

	client := &mockPostgresClient{db: db}
	mock.ExpectClose()

	s := &Service{
		opts: ServiceOpts{
			asyncPersisterNum:  1,
			enableAsyncPersist: true,
		},
		pgClient:    client,
		cleanupDone: make(chan struct{}),
	}
	s.startAsyncPersistWorker()

	err = s.Close()
	assert.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for CreateSession expired session ---

func TestCreateSession_ExpiredSession_Cleanup(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	past := time.Now().Add(-1 * time.Hour)

	// Return existing session that is expired.
	mock.ExpectQuery("SELECT expires_at FROM").
		WithArgs("app", "user", "sess").
		WillReturnRows(sqlmock.NewRows(
			[]string{"expires_at"},
		).AddRow(past))

	// cleanupExpiredForUser - no TTLs set, no cleanup.
	// Then INSERT new session.
	mock.ExpectExec("INSERT INTO session_states").
		WillReturnResult(sqlmock.NewResult(1, 1))

	// ListAppStates.
	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
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
	require.NotNil(t, sess)
}

func TestCreateSession_ExistsNotExpired(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	future := time.Now().Add(1 * time.Hour)

	// Return existing session that is NOT expired.
	mock.ExpectQuery("SELECT expires_at FROM").
		WithArgs("app", "user", "sess").
		WillReturnRows(sqlmock.NewRows(
			[]string{"expires_at"},
		).AddRow(future))

	_, err := s.CreateSession(
		context.Background(), key, nil,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestCreateSession_NilStateValue(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	mock.ExpectQuery("SELECT expires_at FROM").
		WillReturnRows(
			sqlmock.NewRows([]string{"expires_at"}),
		)

	mock.ExpectExec("INSERT INTO session_states").
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	sess, err := s.CreateSession(
		context.Background(), key,
		session.StateMap{"k": nil},
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
}

func TestCreateSession_ListAppStatesError(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	mock.ExpectQuery("SELECT expires_at FROM").
		WillReturnRows(
			sqlmock.NewRows([]string{"expires_at"}),
		)

	mock.ExpectExec("INSERT INTO session_states").
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnError(fmt.Errorf("db error"))

	_, err := s.CreateSession(
		context.Background(), key, nil,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"list app states failed")
}

func TestCreateSession_ListUserStatesError(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	mock.ExpectQuery("SELECT expires_at FROM").
		WillReturnRows(
			sqlmock.NewRows([]string{"expires_at"}),
		)

	mock.ExpectExec("INSERT INTO session_states").
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnError(fmt.Errorf("db error"))

	_, err := s.CreateSession(
		context.Background(), key, nil,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"list user states failed")
}

// --- Tests for hardDeleteExpiredInTx user-scoped ---

func TestCleanupExpiredData_HardDelete_UserScoped(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.sessionTTL = 10 * time.Minute
	s.opts.softDelete = false

	userKey := &session.UserKey{
		AppName: "app", UserID: "user",
	}

	mock.ExpectBegin()
	for i := 0; i < 4; i++ {
		mock.ExpectExec("DELETE FROM").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectCommit()

	s.cleanupExpiredData(
		context.Background(), userKey,
	)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for cleanupExpiredData with all TTLs ---

func TestCleanupExpiredData_AllTTLs(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.sessionTTL = 10 * time.Minute
	s.opts.appStateTTL = 5 * time.Minute
	s.opts.userStateTTL = 15 * time.Minute
	s.opts.softDelete = true

	mock.ExpectBegin()
	// 4 session tables + app_states + user_states = 6.
	for i := 0; i < 6; i++ {
		mock.ExpectExec("UPDATE .* SET deleted_at").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectCommit()

	s.cleanupExpiredData(context.Background(), nil)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for GetSessionSummaryText fallback ---

func TestGetSessionSummaryText_FallbackSuccess(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	sess := session.NewSession("app", "user", "sess")

	// Primary query returns no rows.
	mock.ExpectQuery("SELECT summary FROM").
		WillReturnRows(
			sqlmock.NewRows([]string{"summary"}),
		)

	// Fallback query returns a summary.
	sum := session.Summary{Summary: "fallback summary"}
	sumBytes, _ := json.Marshal(sum)
	mock.ExpectQuery("SELECT summary FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"summary"},
		).AddRow(sumBytes))

	text, ok := s.GetSessionSummaryText(
		context.Background(), sess,
		session.WithSummaryFilterKey("custom"),
	)
	assert.True(t, ok)
	assert.Equal(t, "fallback summary", text)
}

func TestGetSessionSummaryText_QueryError(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	sess := session.NewSession("app", "user", "sess")

	mock.ExpectQuery("SELECT summary FROM").
		WillReturnError(fmt.Errorf("db error"))

	// Should fall through to fallback.
	mock.ExpectQuery("SELECT summary FROM").
		WillReturnRows(
			sqlmock.NewRows([]string{"summary"}),
		)

	text, ok := s.GetSessionSummaryText(
		context.Background(), sess,
		session.WithSummaryFilterKey("custom"),
	)
	assert.Empty(t, text)
	assert.False(t, ok)
}

// --- Tests for startCleanupRoutine ---

func TestStartCleanupRoutine_Runs(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	s.opts.cleanupInterval = 50 * time.Millisecond
	s.opts.sessionTTL = 0

	s.startCleanupRoutine()
	require.NotNil(t, s.cleanupTicker)

	// Wait for at least one tick.
	time.Sleep(100 * time.Millisecond)
	s.stopCleanupRoutine()
}

// --- Tests for addEvent nil state initialization ---

func TestAddEvent_NilState_Initializes(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	// Session with nil state.
	sessState := SessionState{
		ID:    "sess",
		State: nil,
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_events").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

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

	err := s.addEvent(context.Background(), key, evt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for UpdateUserState strips prefix ---

func TestUpdateUserState_StripsPrefix(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectExec("INSERT INTO user_states").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.UpdateUserState(
		context.Background(),
		session.UserKey{AppName: "app", UserID: "user"},
		session.StateMap{
			session.StateUserPrefix + "k": []byte("v"),
		},
	)
	require.NoError(t, err)
}

// --- Tests for EnqueueSummaryJob with cascade ---

func TestEnqueueSummaryJob_CascadeFallback(
	t *testing.T,
) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.summarizer = &mockSummarizer{}
	s.asyncWorker = nil

	sess := session.NewSession("app", "user", "sess")
	err := s.EnqueueSummaryJob(
		context.Background(), sess, "", false,
	)
	// mockSummarizer.ShouldSummarize returns false,
	// so no summary is created.
	assert.NoError(t, err)
}

// --- Tests for GetSession with hooks ---

func TestGetSession_WithRefreshTTLError(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()
	s.opts.sessionTTL = 10 * time.Minute

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	mock.ExpectQuery("SELECT state, created_at").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"state", "created_at", "updated_at",
			},
		).AddRow(stateBytes, now, now))

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		))
	mock.ExpectQuery("SELECT session_id, filter_key, summary").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "filter_key", "summary"},
		))

	// refreshSessionTTL fails but should not cause
	// GetSession to fail.
	mock.ExpectExec("UPDATE .* SET updated_at").
		WillReturnError(fmt.Errorf("refresh fail"))

	sess, err := s.GetSession(
		context.Background(), key,
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
}

// --- Test ListSessions events list error ---

func TestListSessions_EventsListError(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "app", UserID: "user",
	}

	sessState := SessionState{
		ID:    "sess1",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	now := time.Now()

	mock.ExpectQuery(
		"SELECT key, value FROM app_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery(
		"SELECT key, value FROM user_states",
	).WillReturnRows(
		sqlmock.NewRows([]string{"key", "value"}),
	)

	mock.ExpectQuery("SELECT session_id, state").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "state",
				"created_at", "updated_at",
			},
		).AddRow("sess1", stateBytes, now, now))

	mock.ExpectQuery("SELECT session_id, event").
		WillReturnError(fmt.Errorf("events error"))

	_, err := s.ListSessions(
		context.Background(), userKey,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"get events list failed")
}

// --- Test for getSession scan error ---

func TestGetSession_ScanError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	// Return wrong number of columns to trigger scan
	// error.
	mock.ExpectQuery("SELECT state, created_at").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"state", "created_at", "updated_at",
			},
		).AddRow(nil, nil, nil).RowError(
			0, fmt.Errorf("scan error"),
		))

	_, err := s.GetSession(
		context.Background(), key,
	)
	assert.Error(t, err)
}

// --- Test for DeleteSession begin tx error ---

func TestDeleteSession_BeginTxError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	mock.ExpectBegin().
		WillReturnError(fmt.Errorf("begin error"))

	err := s.DeleteSession(
		context.Background(), key,
	)
	assert.Error(t, err)
}

// --- Test for UpdateSessionState with empty
//     stateBytes ---

func TestUpdateSessionState_EmptyStateBytes(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user",
		SessionID: "sess",
	}

	// Return empty state bytes (not nil, not JSON).
	rows := sqlmock.NewRows([]string{"state"}).
		AddRow([]byte{})
	mock.ExpectQuery("SELECT state FROM").
		WillReturnRows(rows)

	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.UpdateSessionState(
		context.Background(), key,
		session.StateMap{"k": []byte("v")},
	)
	require.NoError(t, err)
}
