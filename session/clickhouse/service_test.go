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
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

func TestNewService(t *testing.T) {
	// Register a mock instance
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockClient{}, nil
	})

	tests := []struct {
		name    string
		opts    []ServiceOpt
		wantErr bool
	}{
		{
			name: "success with DSN",
			opts: []ServiceOpt{
				WithClickHouseDSN("clickhouse://localhost:9000"),
				WithTablePrefix("test_"),
				WithSkipDBInit(true),
			},
			wantErr: false,
		},
		{
			name: "success with Instance",
			opts: []ServiceOpt{
				WithClickHouseInstance("test-instance"),
				WithSkipDBInit(true),
			},
			wantErr: false,
		},
	}

	// Register test instance
	storage.RegisterClickHouseInstance("test-instance", storage.WithClientBuilderDSN("clickhouse://localhost:9000"))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewService(tt.opts...)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewService() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && s == nil {
				t.Error("NewService() returned nil service")
			}
			if s != nil {
				s.Close()
			}
		})
	}
}

func TestService_GetSession(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		opts:               ServiceOpts{sessionTTL: time.Hour},
		tableSessionStates: "session_states",
		tableAppStates:     "app_states",
		tableUserStates:    "user_states",
	}

	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "test-user", SessionID: "test-session"}
	state := session.StateMap{"key1": []byte("value1")}

	// Mock query for existing session (not found)
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{}), nil
	}

	// Mock exec for insert
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return nil
	}

	// Create session
	sess, err := s.CreateSession(ctx, key, state)
	assert.NoError(t, err)
	assert.NotNil(t, sess)
	assert.Equal(t, key.AppName, sess.AppName)
	assert.Equal(t, key.UserID, sess.UserID)
	assert.Equal(t, key.SessionID, sess.ID)
	assert.Equal(t, state, sess.State)
}

func TestService_CreateSession_Exists(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		opts:               ServiceOpts{sessionTTL: time.Hour},
		tableSessionStates: "session_states",
	}

	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "test-user", SessionID: "test-session"}
	state := session.StateMap{"key1": []byte("value1")}
	now := time.Now()
	expiresAt := now.Add(time.Hour)

	// Case 1: Session exists and not expired
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{&expiresAt}}), nil
	}

	sess, err := s.CreateSession(ctx, key, state)
	assert.Error(t, err)
	assert.Nil(t, sess)
	assert.Contains(t, err.Error(), "session already exists")

	// Case 2: Session exists but expired
	expiredAt := now.Add(-time.Hour)
	callCount := 0
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		callCount++
		if callCount == 1 { // Check existing
			return newMockRows([][]any{{&expiredAt}}), nil
		}
		// Subsequent calls for ListAppStates etc.
		return newMockRows([][]any{}), nil
	}
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return nil
	}

	sess, err = s.CreateSession(ctx, key, state)
	assert.NoError(t, err)
	assert.NotNil(t, sess)
}

func TestService_GetSession_Error(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		tableSessionStates: "session_states",
	}
	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "test-user", SessionID: "test-session"}

	// Case 1: Query error
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return nil, assert.AnError
	}
	sess, err := s.GetSession(ctx, key)
	assert.Error(t, err)
	assert.Nil(t, sess)

	// Case 2: Session not found
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{}), nil
	}
	sess, err = s.GetSession(ctx, key)
	assert.NoError(t, err)
	assert.Nil(t, sess)
}

func TestService_GetSession_NoTTLRefresh(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		opts:                  ServiceOpts{sessionTTL: 0}, // No TTL refresh
		tableSessionStates:    "session_states",
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
		tableAppStates:        "app_states",
		tableUserStates:       "user_states",
	}

	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "test-user", SessionID: "test-session"}

	// Mock queries for getSession
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if strings.Contains(query, "FROM session_states") {
			// Create proper SessionState JSON with ID
			sessState := SessionState{
				ID:    key.SessionID,
				State: make(session.StateMap),
			}
			stateBytes, _ := json.Marshal(sessState)
			return newMockRows([][]any{{string(stateBytes), time.Now(), time.Now()}}), nil
		}
		if strings.Contains(query, "FROM app_states") {
			return newMockRows([][]any{}), nil
		}
		if strings.Contains(query, "FROM user_states") {
			return newMockRows([][]any{}), nil
		}
		return newMockRows([][]any{}), nil
	}

	// Get existing session - should not refresh TTL since sessionTTL is 0
	sess, err := s.GetSession(ctx, key)
	assert.NoError(t, err)
	assert.NotNil(t, sess)
	assert.Equal(t, key.AppName, sess.AppName)
	assert.Equal(t, key.UserID, sess.UserID)
	assert.Equal(t, key.SessionID, sess.ID)
}

func TestService_ListSessions(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		opts:                  ServiceOpts{sessionTTL: time.Hour},
		tableSessionStates:    "session_states",
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
		tableAppStates:        "app_states",
		tableUserStates:       "user_states",
	}

	ctx := context.Background()
	userKey := session.UserKey{AppName: "test-app", UserID: "test-user"}
	now := time.Now()

	// Mock responses
	// 1. ListAppStates
	// 2. ListUserStates
	// 3. Query all session states
	// 4. getEventsList
	// 5. getSummariesList

	sessState1 := SessionState{
		ID:        "sess1",
		State:     session.StateMap{"k1": []byte("v1")},
		CreatedAt: now,
		UpdatedAt: now,
	}
	stateBytes1, _ := json.Marshal(sessState1)

	sessState2 := SessionState{
		ID:        "sess2",
		State:     session.StateMap{"k2": []byte("v2")},
		CreatedAt: now,
		UpdatedAt: now,
	}
	stateBytes2, _ := json.Marshal(sessState2)

	callCount := 0
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		callCount++
		if callCount == 1 { // app state
			return newMockRows([][]any{}), nil
		}
		if callCount == 2 { // user state
			return newMockRows([][]any{}), nil
		}
		if callCount == 3 { // list session states
			return newMockRows([][]any{
				{sessState1.ID, string(stateBytes1), now, now},
				{sessState2.ID, string(stateBytes2), now, now},
			}), nil
		}
		if callCount == 4 { // events
			return newMockRows([][]any{}), nil
		}
		if callCount == 5 { // summaries
			return newMockRows([][]any{}), nil
		}
		return newMockRows([][]any{}), nil
	}

	sessions, err := s.ListSessions(ctx, userKey)
	assert.NoError(t, err)
	assert.Len(t, sessions, 2)
	assert.Equal(t, "sess1", sessions[0].ID)
	assert.Equal(t, "sess2", sessions[1].ID)
}

func TestService_ListSessions_Complex(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		opts:                  ServiceOpts{sessionTTL: time.Hour},
		tableSessionStates:    "session_states",
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
	}

	ctx := context.Background()
	userKey := session.UserKey{AppName: "test-app", UserID: "test-user"}
	now := time.Now()

	sessState1 := SessionState{
		ID:        "sess1",
		State:     session.StateMap{"k1": []byte("v1")},
		CreatedAt: now,
		UpdatedAt: now,
	}
	stateBytes1, _ := json.Marshal(sessState1)

	callCount := 0
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		callCount++
		// Sequence:
		// 1. ListAppStates
		// 2. ListUserStates
		// 3. ListSessions (session_states)
		// 4. getEventsList
		// 5. getSummariesList

		if callCount == 1 || callCount == 2 { // AppState, UserState
			return newMockRows([][]any{}), nil
		}
		if callCount == 3 { // SessionStates
			return newMockRows([][]any{
				{sessState1.ID, string(stateBytes1), now, now},
			}), nil
		}
		// Events and Summaries
		return newMockRows([][]any{}), nil
	}

	// Test with TimeRange, Limit, Offset, Reverse
	// Note: Currently ListSessions interface in session package does not support these options directly.
	// We are mocking them to verify if we were to support them in future or if we used internal methods.
	// But since the interface signature is fixed, we can only test what's available.
	// The Service implementation of ListSessions only takes generic Option which maps to Options struct.
	// Options struct currently only has EventNum and EventTime.
	// So we cannot pass WithStartTime, WithLimit etc. unless we update session.Options.

	// Let's test what IS supported: EventNum (limit) and EventTime (afterTime)
	sessions, err := s.ListSessions(ctx, userKey,
		session.WithEventTime(now.Add(-time.Hour)),
		session.WithEventNum(10),
	)
	assert.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "sess1", sessions[0].ID)
}

func TestService_DeleteSession(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:              mockCli,
		tableSessionStates:    "session_states",
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
	}
	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "test-user", SessionID: "test-session"}

	// Mock responses for deleteSessionState
	// 1. query session state
	// 2. query events
	// 3. query summaries

	now := time.Now()
	callCount := 0
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		callCount++
		if callCount == 1 { // session state
			return newMockRows([][]any{{"{}", now, now, &now}}), nil
		}
		return newMockRows([][]any{}), nil
	}

	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return nil
	}

	err := s.DeleteSession(ctx, key)
	assert.NoError(t, err)
}

func TestService_Options(t *testing.T) {
	// Test all options
	opts := []ServiceOpt{
		WithClickHouseDSN("clickhouse://localhost:9000"),
		WithTablePrefix("prefix_"),
		WithSessionTTL(time.Hour),
		WithAppStateTTL(time.Hour),
		WithUserStateTTL(time.Hour),
		WithDeletedRetention(time.Hour),
		WithBatchSize(100),
		WithBatchTimeout(time.Second),
		WithAsyncPersisterNum(5),
		WithCleanupInterval(time.Minute),
		WithSkipDBInit(true),
		WithExtraOptions("foo", "bar"),
	}

	s, err := NewService(opts...)
	assert.NoError(t, err)
	assert.NotNil(t, s)
	s.Close()
}

func TestService_AppState(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:       mockCli,
		tableAppStates: "app_states",
	}
	ctx := context.Background()
	appName := "test-app"

	// Test UpdateAppState
	err := s.UpdateAppState(ctx, appName, session.StateMap{"k1": []byte("v1")})
	assert.NoError(t, err)

	// Test ListAppStates
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"k1", "v1"}}), nil
	}
	state, err := s.ListAppStates(ctx, appName)
	assert.NoError(t, err)
	assert.Equal(t, []byte("v1"), state["k1"])

	// Test DeleteAppState
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		now := time.Now()
		return newMockRows([][]any{{"v1", &now}}), nil
	}
	err = s.DeleteAppState(ctx, appName, "k1")
	assert.NoError(t, err)
}

func TestService_UserState(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:        mockCli,
		tableUserStates: "user_states",
	}
	ctx := context.Background()
	userKey := session.UserKey{AppName: "test-app", UserID: "test-user"}

	// Test UpdateUserState
	err := s.UpdateUserState(ctx, userKey, session.StateMap{"k1": []byte("v1")})
	assert.NoError(t, err)

	// Test ListUserStates
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"k1", "v1"}}), nil
	}
	state, err := s.ListUserStates(ctx, userKey)
	assert.NoError(t, err)
	assert.Equal(t, []byte("v1"), state["k1"])

	// Test DeleteUserState
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		now := time.Now()
		return newMockRows([][]any{{"v1", &now}}), nil
	}
	err = s.DeleteUserState(ctx, userKey, "k1")
	assert.NoError(t, err)
}

func TestService_UpdateSessionState(t *testing.T) {
	mockCli := &mockClient{}
	s := &Service{
		chClient:           mockCli,
		tableSessionStates: "session_states",
	}
	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "test-user", SessionID: "test-session"}

	// Mock query for existing session
	mockCli.queryFunc = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"{}"}}), nil
	}

	err := s.UpdateSessionState(ctx, key, session.StateMap{"k1": []byte("v1")})
	assert.NoError(t, err)
}
