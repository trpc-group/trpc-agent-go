//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestService_AppendEvent(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		opts:               ServiceOpts{sessionTTL: time.Hour},
		tableSessionStates: "session_states",
		tableSessionEvents: "session_events",
	}

	ctx := context.Background()
	sess := session.NewSession("test-app", "test-user", "test-session")
	sess.State = session.StateMap{"k1": []byte("v1")}
	sess.CreatedAt = time.Now()

	e := &event.Event{
		ID: "evt1",
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "test content",
					},
				},
			},
		},
	}

	// Mock responses for addEvent
	// 1. query session state

	now := time.Now()
	sessState := SessionState{
		ID:        sess.ID,
		State:     sess.State,
		CreatedAt: now,
		UpdatedAt: now,
	}
	stateBytes, _ := json.Marshal(sessState)

	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{string(stateBytes), now}}), nil
	}

	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return nil
	}

	err := s.AppendEvent(ctx, sess, e)
	assert.NoError(t, err)
}

func TestService_AppendEvent_Error(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		tableSessionStates: "session_states",
	}
	ctx := context.Background()
	sess := session.NewSession("test-app", "test-user", "test-session")
	sess.CreatedAt = time.Now()
	e := &event.Event{ID: "evt1"}

	// Mock DB error in addEvent (query state)
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}

	err := s.AppendEvent(ctx, sess, e)
	assert.Error(t, err)
}

func TestService_AsyncAppendEvent(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient: mockCli,
		opts: ServiceOpts{
			sessionTTL:         time.Hour,
			enableAsyncPersist: true,
			asyncPersisterNum:  1,
			batchSize:          10,
			batchTimeout:       time.Millisecond * 100,
		},
		tableSessionStates: "session_states",
		tableSessionEvents: "session_events",
	}

	s.startAsyncPersistWorker()
	defer func() {
		for _, ch := range s.eventPairChans {
			close(ch)
		}
		s.persistWg.Wait()
	}()

	ctx := context.Background()
	sess := session.NewSession("test-app", "test-user", "test-session")
	sess.State = session.StateMap{"k1": []byte("v1")}
	sess.CreatedAt = time.Now()

	e := &event.Event{
		ID: "evt1",
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "test content",
					},
				},
			},
		},
	}

	// Mock query for addEvent (called by async worker)
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		now := time.Now()
		sessState := SessionState{
			ID:        sess.ID,
			State:     sess.State,
			CreatedAt: now,
			UpdatedAt: now,
		}
		stateBytes, _ := json.Marshal(sessState)
		return newMockRows([][]any{{string(stateBytes), now}}), nil
	}

	err := s.AppendEvent(ctx, sess, e)
	assert.NoError(t, err)

	// Wait for async processing
	time.Sleep(time.Millisecond * 200)
}

func TestService_GetSessionSummaryText(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}

	ctx := context.Background()
	sess := session.NewSession("test-app", "test-user", "test-session")
	sess.CreatedAt = time.Now()

	summary := session.Summary{
		Summary: "test summary",
	}
	summaryBytes, _ := json.Marshal(summary)

	// Case 1: Filter key found
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{string(summaryBytes)}}), nil
	}

	text, ok := s.GetSessionSummaryText(ctx, sess, session.WithSummaryFilterKey("test-filter"))
	assert.True(t, ok)
	assert.Equal(t, "test summary", text)

	// Case 2: Filter key not found, fallback to all contents found
	callCount := 0
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		callCount++
		if callCount == 1 {
			return newMockRows([][]any{}), nil
		}
		return newMockRows([][]any{{string(summaryBytes)}}), nil
	}

	text, ok = s.GetSessionSummaryText(ctx, sess, session.WithSummaryFilterKey("missing-filter"))
	assert.True(t, ok)
	assert.Equal(t, "test summary", text)

	// Case 3: Both not found
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{}), nil
	}
	text, ok = s.GetSessionSummaryText(ctx, sess, session.WithSummaryFilterKey("missing-filter"))
	assert.False(t, ok)
	assert.Equal(t, "", text)
}

func TestService_GetSessionSummaryText_Error(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	sess := session.NewSession("test-app", "test-user", "test-session")
	sess.CreatedAt = time.Now()

	// Query error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}

	text, ok := s.GetSessionSummaryText(ctx, sess)
	assert.False(t, ok)
	assert.Equal(t, "", text)
}

func TestService_CreateSessionSummary(t *testing.T) {
	mockCli := &mockClient{}
	// Need a mock summarizer
	// Since summarizer is an interface, we can't easily mock it without defining a mock struct
	// For now, we test the nil summarizer case and error cases

	s := &Service{
		chClient: mockCli,
		opts:     ServiceOpts{}, // No summarizer
	}

	ctx := context.Background()
	sess := session.NewSession("test-app", "test-user", "test-session")

	err := s.CreateSessionSummary(ctx, sess, "test-filter", false)
	assert.NoError(t, err) // Should return nil if no summarizer
}

func TestService_Cleanup(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient: mockCli,
		opts: ServiceOpts{
			cleanupInterval:  time.Millisecond * 10,
			sessionTTL:       time.Hour,
			deletedRetention: time.Hour,
		},
		tableSessionStates:    "session_states",
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
		tableAppStates:        "app_states",
		tableUserStates:       "user_states",
	}

	// Mock queries for cleanup
	// The cleanup routine calls multiple queries.
	// 1. cleanupDeletedData: Exec (ALTER TABLE DELETE) x 5
	// 2. softDeleteExpiredSessions: Query expired -> BatchInsert
	// 3. softDeleteExpiredAppStates: Query expired -> BatchInsert
	// 4. softDeleteExpiredUserStates: Query expired -> BatchInsert

	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{}), nil
	}
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return nil
	}

	s.startCleanupRoutine()
	time.Sleep(time.Millisecond * 50)
	s.stopCleanupRoutine()
}

func TestService_FlushEventBatch_Error(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient: mockCli,
		opts: ServiceOpts{
			sessionTTL: time.Hour,
		},
		tableSessionStates: "session_states",
	}

	// Prepare a batch
	batch := []*sessionEventPair{
		{
			key:   session.Key{AppName: "app", UserID: "user", SessionID: "sess"},
			event: &event.Event{ID: "evt1"},
		},
	}

	// Mock DB error in addEvent
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}

	// Should not panic or return error (logs error)
	s.flushEventBatch(batch)
}

func TestService_GetEventsList_MultiRow(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		tableSessionEvents: "session_events",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// Prepare multi-row events
	now := time.Now()
	evt1 := &event.Event{
		ID: "evt1",
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "content1",
					},
				},
			},
		},
	}
	evt1Bytes, _ := json.Marshal(evt1)
	evt2 := &event.Event{
		ID: "evt2",
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "content2",
					},
				},
			},
		},
	}
	evt2Bytes, _ := json.Marshal(evt2)

	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{
			{"app", "user", "sess", string(evt1Bytes)},
			{"app", "user", "sess", string(evt2Bytes)},
		}), nil
	}

	eventsList, err := s.getEventsList(ctx, []session.Key{key}, []time.Time{now}, 0, time.Time{})
	assert.NoError(t, err)
	assert.Len(t, eventsList, 1)
	events := eventsList[0]
	assert.Len(t, events, 2)
	assert.Equal(t, "evt1", events[0].ID)
	assert.Equal(t, "evt2", events[1].ID)
}

func TestService_GetSummary(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	createdAt := time.Now().Add(-time.Hour)

	// Prepare summaries with different filter keys
	sum1 := session.Summary{Summary: "sum1"}
	sum1Bytes, _ := json.Marshal(sum1)
	sum2 := session.Summary{Summary: "sum2"}
	sum2Bytes, _ := json.Marshal(sum2)

	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		// Format: filter_key, summary
		return newMockRows([][]any{
			{"k1", string(sum1Bytes)},
			{"k2", string(sum2Bytes)},
		}), nil
	}

	summaries, err := s.getSummary(ctx, key, createdAt)
	assert.NoError(t, err)
	assert.Len(t, summaries, 2)
	assert.Equal(t, "sum1", summaries["k1"].Summary)
	assert.Equal(t, "sum2", summaries["k2"].Summary)
}

func TestService_GetSummary_Empty(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{}), nil
	}

	summaries, err := s.getSummary(ctx, key, time.Now())
	assert.NoError(t, err)
	assert.Len(t, summaries, 0)
}

func TestService_GetSummary_Error(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// Case 1: Query error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	_, err := s.getSummary(ctx, key, time.Now())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get summaries failed")

	// Case 2: Malformed JSON
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{
			{"k1", "{malformed"},
		}), nil
	}
	_, err = s.getSummary(ctx, key, time.Now())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal summary failed")
}

func TestService_GetSummariesList_MultiRow(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	createdAt := time.Now().Add(-time.Hour)

	// Prepare multi-row summaries
	sum1 := session.Summary{Summary: "sum1"}
	sum1Bytes, _ := json.Marshal(sum1)
	sum2 := session.Summary{Summary: "sum2"}
	sum2Bytes, _ := json.Marshal(sum2)

	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		// Format: session_id, filter_key, summary, updated_at
		return newMockRows([][]any{
			{"sess", "k1", string(sum1Bytes), time.Now()},
			{"sess", "k2", string(sum2Bytes), time.Now()},
		}), nil
	}

	summariesList, err := s.getSummariesList(ctx, []session.Key{key}, []time.Time{createdAt})
	assert.NoError(t, err)
	assert.Len(t, summariesList, 1)
	summaries := summariesList[0]
	assert.Len(t, summaries, 2)
	assert.Equal(t, "sum1", summaries["k1"].Summary)
	assert.Equal(t, "sum2", summaries["k2"].Summary)
}

func TestService_GetSummariesList_Empty(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()

	// Empty input
	result, err := s.getSummariesList(ctx, []session.Key{}, []time.Time{})
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestService_GetSummariesList_LengthMismatch(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// Length mismatch
	_, err := s.getSummariesList(ctx, []session.Key{key}, []time.Time{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "length mismatch")
}

func TestService_GetSummariesList_FilterOldSummary(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	// Session created 1 hour ago
	sessionCreatedAt := time.Now().Add(-time.Hour)

	// Prepare summaries: one fresh, one stale (from previous session instance)
	freshSum := session.Summary{Summary: "fresh"}
	freshBytes, _ := json.Marshal(freshSum)
	staleSum := session.Summary{Summary: "stale"}
	staleBytes, _ := json.Marshal(staleSum)

	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		// Format: session_id, filter_key, summary, updated_at
		return newMockRows([][]any{
			// Fresh summary: updated_at is after session createdAt
			{"sess", "k1", string(freshBytes), time.Now()},
			// Stale summary: updated_at is before session createdAt (from old session)
			{"sess", "k2", string(staleBytes), sessionCreatedAt.Add(-time.Minute)},
		}), nil
	}

	summariesList, err := s.getSummariesList(ctx, []session.Key{key}, []time.Time{sessionCreatedAt})
	assert.NoError(t, err)
	assert.Len(t, summariesList, 1)
	summaries := summariesList[0]
	// Only fresh summary should be included, stale one filtered out
	assert.Len(t, summaries, 1)
	assert.Equal(t, "fresh", summaries["k1"].Summary)
	assert.Nil(t, summaries["k2"])
}

func TestService_GetSummariesList_MultipleSessions(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key1 := session.Key{AppName: "app", UserID: "user", SessionID: "sess1"}
	key2 := session.Key{AppName: "app", UserID: "user", SessionID: "sess2"}
	createdAt1 := time.Now().Add(-2 * time.Hour)
	createdAt2 := time.Now().Add(-time.Hour)

	sum1 := session.Summary{Summary: "sum1"}
	sum1Bytes, _ := json.Marshal(sum1)
	sum2 := session.Summary{Summary: "sum2"}
	sum2Bytes, _ := json.Marshal(sum2)

	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		// Format: session_id, filter_key, summary, updated_at
		return newMockRows([][]any{
			{"sess1", "k1", string(sum1Bytes), time.Now()},
			{"sess2", "k1", string(sum2Bytes), time.Now()},
		}), nil
	}

	summariesList, err := s.getSummariesList(ctx, []session.Key{key1, key2}, []time.Time{createdAt1, createdAt2})
	assert.NoError(t, err)
	assert.Len(t, summariesList, 2)
	assert.Equal(t, "sum1", summariesList[0]["k1"].Summary)
	assert.Equal(t, "sum2", summariesList[1]["k1"].Summary)
}

func TestService_GetSummariesList_UnknownSessionFiltered(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess1"}
	createdAt := time.Now().Add(-time.Hour)

	sum1 := session.Summary{Summary: "sum1"}
	sum1Bytes, _ := json.Marshal(sum1)
	sumUnknown := session.Summary{Summary: "unknown"}
	sumUnknownBytes, _ := json.Marshal(sumUnknown)

	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		// Format: session_id, filter_key, summary, updated_at
		return newMockRows([][]any{
			{"sess1", "k1", string(sum1Bytes), time.Now()},
			// Summary for unknown session (not in sessionKeys) should be filtered
			{"sess_unknown", "k1", string(sumUnknownBytes), time.Now()},
		}), nil
	}

	summariesList, err := s.getSummariesList(ctx, []session.Key{key}, []time.Time{createdAt})
	assert.NoError(t, err)
	assert.Len(t, summariesList, 1)
	assert.Len(t, summariesList[0], 1)
	assert.Equal(t, "sum1", summariesList[0]["k1"].Summary)
}

func TestService_GetEventsList_Error(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		tableSessionEvents: "session_events",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// Case 1: Query error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	_, err := s.getEventsList(ctx, []session.Key{key}, []time.Time{time.Now()}, 0, time.Time{})
	assert.Error(t, err)

	// Case 2: Scan error (not easily mockable with mockRows, but can try malformed JSON)
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{
			{"app", "user", "sess", "{malformed-json"},
		}), nil
	}
	_, err = s.getEventsList(ctx, []session.Key{key}, []time.Time{time.Now()}, 0, time.Time{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal event failed")
}

func TestService_GetSummariesList_Error(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	createdAt := time.Now().Add(-time.Hour)

	// Case 1: Query error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	_, err := s.getSummariesList(ctx, []session.Key{key}, []time.Time{createdAt})
	assert.Error(t, err)

	// Case 2: Malformed JSON
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		// New format: session_id, filter_key, summary, updated_at
		return newMockRows([][]any{
			{"sess", "k1", "{malformed", time.Now()},
		}), nil
	}
	_, err = s.getSummariesList(ctx, []session.Key{key}, []time.Time{createdAt})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal summary failed")
}

func TestService_GetSummary_ScanError(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// Force scan error using scanFunc
	rows := newMockRows([][]any{{"k1", "summary"}})
	rows.scanFunc = func(dest ...any) error {
		return assert.AnError
	}
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return rows, nil
	}

	_, err := s.getSummary(ctx, key, time.Now())
	assert.Error(t, err)
}

func TestService_GetSummariesList_ScanError(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	createdAt := time.Now().Add(-time.Hour)

	// Force scan error using scanFunc
	rows := newMockRows([][]any{{"sess", "k1", "summary", time.Now()}})
	rows.scanFunc = func(dest ...any) error {
		return assert.AnError
	}
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return rows, nil
	}

	_, err := s.getSummariesList(ctx, []session.Key{key}, []time.Time{createdAt})
	assert.Error(t, err)
}

func TestService_GetSummariesList_SessionWithNoSummary(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key1 := session.Key{AppName: "app", UserID: "user", SessionID: "sess1"}
	key2 := session.Key{AppName: "app", UserID: "user", SessionID: "sess2"}
	createdAt1 := time.Now().Add(-2 * time.Hour)
	createdAt2 := time.Now().Add(-time.Hour)

	sum1 := session.Summary{Summary: "sum1"}
	sum1Bytes, _ := json.Marshal(sum1)

	// Only sess1 has summary, sess2 has none
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{
			{"sess1", "k1", string(sum1Bytes), time.Now()},
		}), nil
	}

	summariesList, err := s.getSummariesList(ctx, []session.Key{key1, key2}, []time.Time{createdAt1, createdAt2})
	assert.NoError(t, err)
	assert.Len(t, summariesList, 2)
	// sess1 has summary
	assert.Len(t, summariesList[0], 1)
	assert.Equal(t, "sum1", summariesList[0]["k1"].Summary)
	// sess2 has empty map (not nil)
	assert.NotNil(t, summariesList[1])
	assert.Len(t, summariesList[1], 0)
}

func TestService_RefreshSessionTTL_MoreErrors(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		tableSessionStates: "session_states",
		opts:               ServiceOpts{sessionTTL: time.Hour},
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// Case 1: Scan error
	// Force scan error using scanFunc
	rows := newMockRows([][]any{{"state_json"}})
	rows.scanFunc = func(dest ...any) error {
		return assert.AnError
	}
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return rows, nil
	}
	err := s.refreshSessionTTL(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scan session state failed")

	// Case 2: Insert error
	// Reset queryFunc to return valid rows
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{
			{"{}", time.Now()},
		}), nil
	}
	// Set execFunc to return error
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return assert.AnError
	}
	err = s.refreshSessionTTL(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "refresh session TTL failed")
}

func TestService_AddEvent_MoreErrors(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		tableSessionStates: "session_states",
		tableSessionEvents: "session_events",
		opts:               ServiceOpts{sessionTTL: time.Hour},
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	evt := &event.Event{ID: "evt1", Response: &model.Response{}}

	// Case 1: Session not found
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{}), nil
	}
	err := s.addEvent(ctx, key, evt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")

	// Case 2: Malformed session state JSON
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{
			{"{bad-json", time.Now()},
		}), nil
	}
	err = s.addEvent(ctx, key, evt)
	assert.Error(t, err)

	// Case 3: Update session state failed
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{
			{"{}", time.Now()},
		}), nil
	}
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		if strings.Contains(query, "INSERT INTO session_states") {
			return assert.AnError
		}
		return nil
	}
	err = s.addEvent(ctx, key, evt)
	assert.Error(t, err)
}
