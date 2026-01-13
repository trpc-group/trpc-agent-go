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
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

// ... existing tests (I must include them all or append) ...
// Since Write overwrites, I must include EVERYTHING.
// I will copy previous content and append.

func TestService_RefreshSessionTTL(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		opts:               ServiceOpts{sessionTTL: time.Hour},
		tableSessionStates: "session_states",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// Mock Query
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"{}", time.Now()}}), nil
	}

	// Mock Exec
	execCalled := false
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		execCalled = true
		return nil
	}

	err := s.refreshSessionTTL(ctx, key)
	assert.NoError(t, err)
	assert.True(t, execCalled)
}

func TestService_RefreshSessionTTL_Error(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		opts:               ServiceOpts{sessionTTL: time.Hour},
		tableSessionStates: "session_states",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// Case 1: Query Error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	err := s.refreshSessionTTL(ctx, key)
	assert.Error(t, err)

	// Case 2: Session Not Found
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{}), nil
	}
	err = s.refreshSessionTTL(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")

	// Case 3: Exec Error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"{}", time.Now()}}), nil
	}
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return assert.AnError
	}
	err = s.refreshSessionTTL(ctx, key)
	assert.Error(t, err)
}

func TestService_MergeState(t *testing.T) {
	// Unit test for mergeState helper
	appState := session.StateMap{"k1": []byte("v1")}
	userState := session.StateMap{"k2": []byte("v2")}
	sess := session.NewSession("app", "user", "sess")
	sess.State = session.StateMap{"k3": []byte("v3")}

	merged := mergeState(appState, userState, sess)

	// Expect merged state to contain app:, user: and original keys
	assert.Equal(t, []byte("v1"), merged.State[session.StateAppPrefix+"k1"])
	assert.Equal(t, []byte("v2"), merged.State[session.StateUserPrefix+"k2"])
	assert.Equal(t, []byte("v3"), merged.State["k3"])

	// Test nil session
	assert.Nil(t, mergeState(nil, nil, nil))

	// Test nil state in session
	sess2 := session.NewSession("app", "user", "sess")
	sess2.State = nil
	merged2 := mergeState(appState, userState, sess2)
	assert.NotNil(t, merged2.State)
	assert.Equal(t, []byte("v1"), merged2.State[session.StateAppPrefix+"k1"])
}

func TestService_EnqueueSummaryJob_Validation(t *testing.T) {
	// Register mock client to avoid real connection attempt
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockClient{}, nil
	})

	mockSum := &mockSummarizer{}
	s, err := NewService(WithSummarizer(mockSum), WithSkipDBInit(true), WithClickHouseDSN("clickhouse://localhost:9000"))
	assert.NoError(t, err)

	// Nil session
	err = s.EnqueueSummaryJob(context.Background(), nil, "key", false)
	assert.Error(t, err)
	assert.Equal(t, "session is nil", err.Error())

	// Invalid key
	sess := session.NewSession("", "", "")
	err = s.EnqueueSummaryJob(context.Background(), sess, "key", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "check session key failed")
}

func TestService_GetSession_Detailed(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionStates:    "session_states",
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// Case: Scan error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return &mockRows{
			data:    [][]any{{"invalid"}},
			current: -1,
			scanFunc: func(dest ...any) error {
				return assert.AnError
			},
		}, nil
	}

	sess, err := s.GetSession(ctx, key)
	assert.Error(t, err)
	assert.Nil(t, sess)
	if err != nil {
		assert.Contains(t, err.Error(), "clickhouse session service get session state failed")
	}
}

func TestService_GetSession_MoreErrors(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionStates:    "session_states",
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
		tableAppStates:        "app_states",
		tableUserStates:       "user_states",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// Case: ListAppStates error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "FROM "+s.tableSessionStates) {
			return newMockRows([][]any{{"{}", time.Now(), time.Now()}}), nil
		}
		if strings.Contains(query, "FROM "+s.tableAppStates) {
			return nil, assert.AnError
		}
		return newMockRows([][]any{}), nil
	}
	sess, err := s.GetSession(ctx, key)
	assert.Error(t, err)
	assert.Nil(t, sess)
	assert.Contains(t, err.Error(), "clickhouse session service get session state failed")
}

func TestService_Cleanup_SoftDelete_Errors(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		opts:               ServiceOpts{sessionTTL: time.Hour},
		tableSessionStates: "session_states",
	}
	ctx := context.Background()
	now := time.Now()

	// Case 1: Query Error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	s.softDeleteExpiredSessions(ctx, now)
	// Should log error, not panic

	// Case 2: Batch Error
	expAt := now.Add(-time.Hour)
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{
			{"app", "user", "sess", "{}", now, &expAt},
		}), nil
	}
	mockCli.batchInsertFunc = func(ctx context.Context, query string, fn storage.BatchFn, opts ...driver.PrepareBatchOption) error {
		return assert.AnError
	}
	s.softDeleteExpiredSessions(ctx, now)
	// Should log error
}

func TestNewService_InitDB_Error(t *testing.T) {
	// Register mock client that fails exec
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockClient{
			execFunc: func(ctx context.Context, query string, args ...any) error {
				return assert.AnError
			},
		}, nil
	})

	// Do NOT skip DB init
	s, err := NewService(WithClickHouseDSN("clickhouse://localhost:9000"), WithSkipDBInit(false))
	assert.Error(t, err)
	assert.Nil(t, s)
	assert.Contains(t, err.Error(), "init database failed")
}

func TestService_DeleteAppState_Error(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:       mockCli,
		tableAppStates: "app_states",
	}
	ctx := context.Background()

	// Case 1: Query Error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	err := s.DeleteAppState(ctx, "app", "key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get app state for delete failed")

	// Case 2: Scan Error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return &mockRows{
			data:    [][]any{{"invalid"}}, // Mismatch
			current: -1,
			scanFunc: func(dest ...any) error {
				return assert.AnError
			},
		}, nil
	}
	err = s.DeleteAppState(ctx, "app", "key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scan app state failed")

	// Case 3: Exec Error (soft delete)
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"val", nil}}), nil
	}
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return assert.AnError
	}
	err = s.DeleteAppState(ctx, "app", "key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "soft delete app state failed")
}

func TestService_Hooks(t *testing.T) {
	// Register mock client
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockClient{}, nil
	})

	appendCalled := false
	getCalled := false

	appendHook := func(ctx *session.AppendEventContext, next func() error) error {
		appendCalled = true
		return next()
	}
	getHook := func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
		getCalled = true
		return next()
	}

	mockCli := &mockClient{}
	s, err := NewService(
		WithAppendEventHook(appendHook),
		WithGetSessionHook(getHook),
		WithSkipDBInit(true),
		WithClickHouseDSN("clickhouse://localhost:9000"),
	)
	assert.NoError(t, err)
	s.chClient = mockCli

	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess")
	evt := &event.Event{ID: "evt1"}

	// Mock for AppendEvent
	// Mock Query for addEvent (get createdAt)
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"{}", time.Now(), time.Now()}}), nil
	}
	// Mock Exec for insert event
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return nil
	}

	err = s.AppendEvent(ctx, sess, evt)
	assert.NoError(t, err)
	assert.True(t, appendCalled)

	// Mock for GetSession
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "FROM "+s.tableSessionStates) {
			// Session State
			return newMockRows([][]any{{"{}", time.Now(), time.Now()}}), nil
		}
		// Events, Summaries, App/User States
		return newMockRows([][]any{}), nil
	}

	_, err = s.GetSession(ctx, session.Key{AppName: "app", UserID: "user", SessionID: "sess"})
	assert.NoError(t, err)
	assert.True(t, getCalled)
}

func TestService_DeleteSession_Error(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		tableSessionStates: "session_states",
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// Case 1: Query (Check keys) Error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	err := s.DeleteSession(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get session state for delete failed")

	// Case 2: Scan Error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return &mockRows{
			data:     [][]any{{"invalid"}},
			current:  -1,
			scanFunc: func(dest ...any) error { return assert.AnError },
		}, nil
	}
	err = s.DeleteSession(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scan session state failed")

	// Case 3: Exec (Soft delete) Error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"{}", time.Now(), time.Now()}}), nil
	}
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return assert.AnError
	}
	err = s.DeleteSession(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "soft delete session state failed")
}

func TestService_Cleanup_Deleted_Error(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient: mockCli,
		opts: ServiceOpts{
			deletedRetention: time.Hour,
		},
		tableSessionStates: "session_states",
	}

	// Mock Exec Error
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return assert.AnError
	}

	// Should log error
	s.cleanupDeletedData(context.Background(), time.Now())
}

func TestService_Cleanup_MoreErrors(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
		tableAppStates:        "app_states",
		tableUserStates:       "user_states",
		opts: ServiceOpts{
			appStateTTL:  time.Hour,
			userStateTTL: time.Hour,
		},
	}
	ctx := context.Background()
	now := time.Now()

	// Test softDeleteSessionEvents Batch Error
	// Need to trigger it via softDeleteExpiredSessions but with specific mock sequence
	// Or call private methods via reflection/export? No.
	// But softDeleteExpiredSessions calls them.

	// Case: softDeleteExpiredSessions -> Query events success -> Batch insert fail
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		expAt := now.Add(-time.Hour)
		if strings.Contains(query, "FROM "+s.tableSessionStates) {
			return newMockRows([][]any{
				{"app", "user", "sess", "{}", now, &expAt},
			}), nil
		}
		if strings.Contains(query, "FROM "+s.tableSessionEvents) {
			return newMockRows([][]any{{"evt1", "{}", now, now}}), nil
		}
		// summaries
		return newMockRows([][]any{}), nil
	}

	batchCount := 0
	mockCli.batchInsertFunc = func(ctx context.Context, query string, fn storage.BatchFn, opts ...driver.PrepareBatchOption) error {
		batchCount++
		if batchCount == 2 { // batch insert events
			return assert.AnError
		}
		return fn(&mockBatch{})
	}

	s.softDeleteExpiredSessions(ctx, now)
	// Should log error for events batch

	// Case: softDeleteExpiredAppStates -> Query success -> Batch fail
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		expAt := now.Add(-time.Hour)
		return newMockRows([][]any{{"app", "k", "v", &expAt}}), nil
	}
	mockCli.batchInsertFunc = func(ctx context.Context, query string, fn storage.BatchFn, opts ...driver.PrepareBatchOption) error {
		return assert.AnError
	}
	s.softDeleteExpiredAppStates(ctx, now)
	// Should log error
}

func TestService_DeleteUserState_Error(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:        mockCli,
		tableUserStates: "user_states",
	}
	ctx := context.Background()

	// Case 1: Query Error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	err := s.DeleteUserState(ctx, session.UserKey{AppName: "app", UserID: "user"}, "key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get user state for delete failed")
}
