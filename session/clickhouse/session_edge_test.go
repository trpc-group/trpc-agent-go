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

func TestService_CreateSession_Errors(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		tableSessionStates: "session_states",
		tableAppStates:     "app_states",
		tableUserStates:    "user_states",
		opts:               ServiceOpts{sessionTTL: time.Hour},
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	state := session.StateMap{"k": []byte("v")}

	// Case 1: Check existing session query fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "SELECT expires_at") {
			return nil, assert.AnError
		}
		return newMockRows([][]any{}), nil
	}
	_, err := s.CreateSession(ctx, key, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "check existing session failed")

	// Case 2: Scan existing session fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "SELECT expires_at") {
			return &mockRows{
				data:    [][]any{{"invalid"}},
				current: -1,
				scanFunc: func(dest ...any) error {
					return assert.AnError
				},
			}, nil
		}
		return newMockRows([][]any{}), nil
	}
	_, err = s.CreateSession(ctx, key, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scan expires_at failed")

	// Case 3: Insert session fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{}), nil // No existing session
	}
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return assert.AnError
	}
	_, err = s.CreateSession(ctx, key, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create session failed")

	// Case 4: ListAppStates fails
	mockCli.execFunc = nil
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "SELECT expires_at") {
			return newMockRows([][]any{}), nil
		}
		if strings.Contains(query, "FROM "+s.tableAppStates) {
			return nil, assert.AnError
		}
		return newMockRows([][]any{}), nil
	}
	_, err = s.CreateSession(ctx, key, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list app states failed")

	// Case 5: ListUserStates fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "SELECT expires_at") {
			return newMockRows([][]any{}), nil
		}
		if strings.Contains(query, "FROM "+s.tableAppStates) {
			return newMockRows([][]any{}), nil
		}
		if strings.Contains(query, "FROM "+s.tableUserStates) {
			return nil, assert.AnError
		}
		return newMockRows([][]any{}), nil
	}
	_, err = s.CreateSession(ctx, key, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list user states failed")
}

func TestService_UpdateSessionState_Errors(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		tableSessionStates: "session_states",
		opts:               ServiceOpts{sessionTTL: time.Hour},
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	state := session.StateMap{"k": []byte("v")}

	// Case 1: Query fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	err := s.UpdateSessionState(ctx, key, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "update session state failed")

	// Case 2: Scan fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return &mockRows{
			data:    [][]any{{"{}"}},
			current: -1,
			scanFunc: func(dest ...any) error {
				return assert.AnError
			},
		}, nil
	}
	err = s.UpdateSessionState(ctx, key, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "update session state failed")

	// Case 3: Unmarshal fails (bad json in DB)
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"{bad-json"}}), nil
	}
	err = s.UpdateSessionState(ctx, key, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal state")

	// Case 4: Exec fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"{}"}}), nil
	}
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return assert.AnError
	}
	err = s.UpdateSessionState(ctx, key, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "update session state failed")
}

func TestService_ListSessions_Errors(t *testing.T) {
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
	key := session.UserKey{AppName: "app", UserID: "user"}

	// Case 1: ListAppStates fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "FROM "+s.tableAppStates) {
			return nil, assert.AnError
		}
		return newMockRows([][]any{}), nil
	}
	_, err := s.ListSessions(ctx, key)
	assert.Error(t, err)

	// Case 2: ListUserStates fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "FROM "+s.tableAppStates) {
			return newMockRows([][]any{}), nil
		}
		if strings.Contains(query, "FROM "+s.tableUserStates) {
			return nil, assert.AnError
		}
		return newMockRows([][]any{}), nil
	}
	_, err = s.ListSessions(ctx, key)
	assert.Error(t, err)

	// Case 3: Query sessions fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "FROM "+s.tableSessionStates) {
			return nil, assert.AnError
		}
		return newMockRows([][]any{}), nil
	}
	_, err = s.ListSessions(ctx, key)
	assert.Error(t, err)

	// Case 4: Scan session fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "FROM "+s.tableSessionStates) {
			return &mockRows{
				data:    [][]any{{"sess1", "{}", time.Now(), time.Now()}},
				current: -1,
				scanFunc: func(dest ...any) error {
					return assert.AnError
				},
			}, nil
		}
		return newMockRows([][]any{}), nil
	}
	_, err = s.ListSessions(ctx, key)
	assert.Error(t, err)

	// Case 5: Unmarshal session state fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "FROM "+s.tableSessionStates) {
			return newMockRows([][]any{{"sess1", "{bad-json", time.Now(), time.Now()}}), nil
		}
		return newMockRows([][]any{}), nil
	}
	_, err = s.ListSessions(ctx, key)
	assert.Error(t, err)

	// Case 6: GetEventsList fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "FROM "+s.tableSessionStates) {
			return newMockRows([][]any{{"sess1", "{}", time.Now(), time.Now()}}), nil
		}
		if strings.Contains(query, "FROM "+s.tableSessionEvents) {
			return nil, assert.AnError
		}
		return newMockRows([][]any{}), nil
	}
	_, err = s.ListSessions(ctx, key)
	assert.Error(t, err)

	// Case 7: GetSummariesList fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "FROM "+s.tableSessionStates) {
			return newMockRows([][]any{{"sess1", "{}", time.Now(), time.Now()}}), nil
		}
		if strings.Contains(query, "FROM "+s.tableSessionEvents) {
			return newMockRows([][]any{}), nil
		}
		if strings.Contains(query, "FROM "+s.tableSessionSummaries) {
			return nil, assert.AnError
		}
		return newMockRows([][]any{}), nil
	}
	_, err = s.ListSessions(ctx, key)
	assert.Error(t, err)
}

func TestService_AppendEventInternal_Errors(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		tableSessionStates: "session_states",
		opts:               ServiceOpts{sessionTTL: time.Hour},
	}
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess")
	evt := &event.Event{ID: "evt1"}
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// Case: addEvent fails (e.g. session not found)
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{}), nil
	}
	err := s.appendEventInternal(ctx, sess, evt, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestService_SoftDelete_Errors_Coverage(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
		tableAppStates:        "app_states",
		tableUserStates:       "user_states",
	}
	ctx := context.Background()
	now := time.Now()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// 1. softDeleteSessionEvents
	// Query fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	s.softDeleteSessionEvents(ctx, key, now) // Should log error

	// Scan fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return &mockRows{
			data:    [][]any{{"evt1", "{}", time.Now(), time.Now()}},
			current: -1,
			scanFunc: func(dest ...any) error {
				return assert.AnError
			},
		}, nil
	}
	s.softDeleteSessionEvents(ctx, key, now) // Should log error

	// 2. softDeleteSessionSummaries
	// Query fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	s.softDeleteSessionSummaries(ctx, key, now)

	// Scan fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return &mockRows{
			data:    [][]any{{"key", "{}", time.Now(), time.Now()}},
			current: -1,
			scanFunc: func(dest ...any) error {
				return assert.AnError
			},
		}, nil
	}
	s.softDeleteSessionSummaries(ctx, key, now)

	// Batch fails (covered in service_extra_test but good to double check)
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"key", "{}", time.Now(), time.Now()}}), nil
	}
	mockCli.batchInsertFunc = func(ctx context.Context, query string, fn storage.BatchFn, opts ...driver.PrepareBatchOption) error {
		return assert.AnError
	}
	s.softDeleteSessionSummaries(ctx, key, now)

	// 3. softDeleteExpiredAppStates
	// Query fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	s.softDeleteExpiredAppStates(ctx, now)

	// Scan fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		expAt := now
		return &mockRows{
			data:    [][]any{{"app", "k", "v", &expAt}},
			current: -1,
			scanFunc: func(dest ...any) error {
				return assert.AnError
			},
		}, nil
	}
	s.softDeleteExpiredAppStates(ctx, now)

	// 4. softDeleteExpiredUserStates
	// Query fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	s.softDeleteExpiredUserStates(ctx, now)

	// Scan fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		expAt := now
		return &mockRows{
			data:    [][]any{{"app", "user", "k", "v", &expAt}},
			current: -1,
			scanFunc: func(dest ...any) error {
				return assert.AnError
			},
		}, nil
	}
	s.softDeleteExpiredUserStates(ctx, now)

	// Batch fails
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		expAt := now
		return newMockRows([][]any{{"app", "user", "k", "v", &expAt}}), nil
	}
	mockCli.batchInsertFunc = func(ctx context.Context, query string, fn storage.BatchFn, opts ...driver.PrepareBatchOption) error {
		return assert.AnError
	}
	s.softDeleteExpiredUserStates(ctx, now)
}

func TestService_AppendEvent_Async_ContextCanceled(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient: mockCli,
		opts: ServiceOpts{
			enableAsyncPersist: true,
			asyncPersisterNum:  1,
		},
	}
	// Manually init channels since we didn't call StartAsyncPersistWorker
	s.eventPairChans = make([]chan *sessionEventPair, 1)
	s.eventPairChans[0] = make(chan *sessionEventPair) // Unbuffered, blocks until read

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	sess := session.NewSession("app", "user", "sess")
	evt := &event.Event{ID: "evt1"}
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	err := s.appendEventInternal(ctx, sess, evt, key)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestService_GetSession_RefreshTTL_Fail(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		tableSessionStates: "session_states",
		opts:               ServiceOpts{sessionTTL: time.Hour},
	}
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	// Mock queries
	// Better approach: use a call counter in the mock
	callCount := 0
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		callCount++
		if callCount == 1 {
			// getSession -> query session state
			return newMockRows([][]any{{"{}", time.Now(), time.Now()}}), nil
		}
		if callCount == 2 {
			// getSession -> list app states (success)
			return newMockRows([][]any{}), nil
		}
		if callCount == 3 {
			// getSession -> list user states (success)
			return newMockRows([][]any{}), nil
		}
		if callCount == 4 {
			// getSession -> get events (success)
			return newMockRows([][]any{}), nil
		}
		if callCount == 5 {
			// refreshSessionTTL -> query session state (FAIL)
			return nil, assert.AnError
		}
		return newMockRows([][]any{}), nil
	}

	// It should return the session successfully despite refresh failure
	sess, err := s.GetSession(ctx, key)
	assert.NoError(t, err)
	assert.NotNil(t, sess)
}
