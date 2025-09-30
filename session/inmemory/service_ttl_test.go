//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestSessionTTLBehavior(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *SessionService
		test  func(t *testing.T, service *SessionService)
	}{
		{
			name: "session_never_expires_when_ttl_zero",
			setup: func() *SessionService {
				return NewSessionService(WithSessionTTL(0))
			},
			test: func(t *testing.T, service *SessionService) {
				key := session.Key{
					AppName:   "test-app",
					UserID:    "test-user",
					SessionID: "test-session",
				}
				state := session.StateMap{"key": []byte("value")}

				// Create session
				sess, err := service.CreateSession(context.Background(), key, state)
				require.NoError(t, err)
				require.NotNil(t, sess)

				// Session should be accessible immediately
				retrievedSess, err := service.GetSession(context.Background(), key)
				require.NoError(t, err)
				require.NotNil(t, retrievedSess)

				// Wait some time to ensure session doesn't expire
				time.Sleep(200 * time.Millisecond)

				// Session should still be accessible (never expires when TTL is 0)
				stillValidSess, err := service.GetSession(context.Background(), key)
				require.NoError(t, err)
				assert.NotNil(t, stillValidSess)
				assert.Equal(t, []byte("value"), stillValidSess.State["key"])

				// Verify session is in memory and not expired
				app := service.getOrCreateAppSessions(key.AppName)
				app.mu.RLock()
				userSessions := app.sessions[key.UserID]
				assert.NotNil(t, userSessions)
				sessionWithTTL := userSessions[key.SessionID]
				assert.NotNil(t, sessionWithTTL)
				// When TTL is 0, expiredAt should be zero time (never expires)
				assert.True(t, sessionWithTTL.expiredAt.IsZero())
				app.mu.RUnlock()
			},
		},
		{
			name: "session_expires_after_ttl",
			setup: func() *SessionService {
				return NewSessionService(WithSessionTTL(100 * time.Millisecond))
			},
			test: func(t *testing.T, service *SessionService) {
				key := session.Key{
					AppName:   "test-app",
					UserID:    "test-user",
					SessionID: "test-session",
				}
				state := session.StateMap{"key": []byte("value")}

				// Create session
				sess, err := service.CreateSession(context.Background(), key, state)
				require.NoError(t, err)
				require.NotNil(t, sess)

				// Session should be accessible immediately
				retrievedSess, err := service.GetSession(context.Background(), key)
				require.NoError(t, err)
				require.NotNil(t, retrievedSess)

				// Wait for TTL to expire
				time.Sleep(150 * time.Millisecond)

				// Session should be expired and return nil
				expiredSess, err := service.GetSession(context.Background(), key)
				require.NoError(t, err)
				assert.Nil(t, expiredSess)

				// Verify session is still in memory but expired
				app := service.getOrCreateAppSessions(key.AppName)
				app.mu.RLock()
				userSessions := app.sessions[key.UserID]
				assert.NotNil(t, userSessions)
				sessionWithTTL := userSessions[key.SessionID]
				assert.NotNil(t, sessionWithTTL)
				assert.True(t, isExpired(sessionWithTTL.expiredAt))
				app.mu.RUnlock()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := tt.setup()
			tt.test(t, service)
		})
	}
}

func TestStateTTLBehavior(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *SessionService
		test  func(t *testing.T, service *SessionService)
	}{
		{
			name: "app_state_expires_after_ttl",
			setup: func() *SessionService {
				return NewSessionService(WithAppStateTTL(100 * time.Millisecond))
			},
			test: func(t *testing.T, service *SessionService) {
				appName := "test-app"
				state := session.StateMap{"config": []byte("production")}

				// Update app state
				err := service.UpdateAppState(context.Background(), appName, state)
				require.NoError(t, err)

				// App state should be accessible immediately
				retrievedState, err := service.ListAppStates(context.Background(), appName)
				require.NoError(t, err)
				assert.Equal(t, []byte("production"), retrievedState["config"])

				// Wait for TTL to expire
				time.Sleep(150 * time.Millisecond)

				// App state should be expired and return empty
				expiredState, err := service.ListAppStates(context.Background(), appName)
				require.NoError(t, err)
				assert.Empty(t, expiredState)

				// Verify app state is still in memory but expired
				app := service.getOrCreateAppSessions(appName)
				app.mu.RLock()
				assert.NotNil(t, app.appState)
				assert.True(t, isExpired(app.appState.expiredAt))
				app.mu.RUnlock()
			},
		},
		{
			name: "user_state_expires_after_ttl",
			setup: func() *SessionService {
				return NewSessionService(WithUserStateTTL(100 * time.Millisecond))
			},
			test: func(t *testing.T, service *SessionService) {
				userKey := session.UserKey{
					AppName: "test-app",
					UserID:  "test-user",
				}
				state := session.StateMap{"preference": []byte("dark_mode")}

				// Update user state
				err := service.UpdateUserState(context.Background(), userKey, state)
				require.NoError(t, err)

				// User state should be accessible immediately
				retrievedState, err := service.ListUserStates(context.Background(), userKey)
				require.NoError(t, err)
				assert.Equal(t, []byte("dark_mode"), retrievedState["preference"])

				// Wait for TTL to expire
				time.Sleep(150 * time.Millisecond)

				// User state should be expired and return empty
				expiredState, err := service.ListUserStates(context.Background(), userKey)
				require.NoError(t, err)
				assert.Empty(t, expiredState)

				// Verify user state is still in memory but expired
				app := service.getOrCreateAppSessions(userKey.AppName)
				app.mu.RLock()
				userStateWithTTL := app.userState[userKey.UserID]
				assert.NotNil(t, userStateWithTTL)
				assert.True(t, isExpired(userStateWithTTL.expiredAt))
				app.mu.RUnlock()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := tt.setup()
			tt.test(t, service)
		})
	}
}

func TestTTLCleanupBehavior(t *testing.T) {
	service := NewSessionService(
		WithSessionTTL(50*time.Millisecond),
		WithAppStateTTL(50*time.Millisecond),
		WithUserStateTTL(50*time.Millisecond),
	)

	// Create session and states
	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}
	sessionState := session.StateMap{"key": []byte("value")}
	appState := session.StateMap{"config": []byte("production")}
	userState := session.StateMap{"preference": []byte("dark_mode")}

	_, err := service.CreateSession(context.Background(), key, sessionState)
	require.NoError(t, err)

	err = service.UpdateAppState(context.Background(), "test-app", appState)
	require.NoError(t, err)

	userKey := session.UserKey{AppName: "test-app", UserID: "test-user"}
	err = service.UpdateUserState(context.Background(), userKey, userState)
	require.NoError(t, err)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Verify data is expired but still in memory
	app := service.getOrCreateAppSessions("test-app")
	app.mu.RLock()
	assert.NotEmpty(t, app.sessions)
	assert.NotEmpty(t, app.userState)
	app.mu.RUnlock()

	// Run cleanup
	service.cleanupExpired()

	// Verify expired data is removed
	app.mu.RLock()
	assert.Empty(t, app.sessions)
	assert.Empty(t, app.userState)
	assert.Empty(t, app.appState.data)
	app.mu.RUnlock()
}

func TestTTLRefresh(t *testing.T) {
	service := NewSessionService(WithSessionTTL(200 * time.Millisecond))
	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}
	state := session.StateMap{"key": []byte("value")}

	// Create session
	_, err := service.CreateSession(context.Background(), key, state)
	require.NoError(t, err)

	// Wait half the TTL time
	time.Sleep(100 * time.Millisecond)

	// Access session to refresh TTL
	_, err = service.GetSession(context.Background(), key)
	require.NoError(t, err)

	// Wait another half TTL time, total time is now > original TTL
	time.Sleep(150 * time.Millisecond)

	// Session should still be accessible due to TTL refresh
	stillValidSess, err := service.GetSession(context.Background(), key)
	require.NoError(t, err)
	assert.NotNil(t, stillValidSess, "Session should be valid after TTL refresh")
}

func TestTTLNoRefresh(t *testing.T) {
	service := NewSessionService(WithSessionTTL(100 * time.Millisecond))
	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session-no-refresh",
	}
	state := session.StateMap{"key": []byte("value")}

	// Create session
	_, err := service.CreateSession(context.Background(), key, state)
	require.NoError(t, err)

	// Wait for TTL to expire without accessing the session
	time.Sleep(150 * time.Millisecond)

	// Session should be expired
	expiredSess, err := service.GetSession(context.Background(), key)
	require.NoError(t, err)
	assert.Nil(t, expiredSess, "Session should be expired as it was not refreshed")
}

func TestAutoCleanupNegativeIntervalBehavior(t *testing.T) {
	// Test negative cleanup interval
	service := NewSessionService(
		WithCleanupInterval(-time.Second),
		WithSessionTTL(time.Hour), // Configure TTL to trigger auto cleanup logic
	)
	defer service.Close()

	// Verify cleanup interval is set to default when negative and TTL is configured
	if service.opts.cleanupInterval != 5*time.Minute {
		t.Errorf("Expected cleanup interval to be default (5m), got %v", service.opts.cleanupInterval)
	}

	// Verify cleanup ticker is running when TTL is configured
	if service.cleanupTicker == nil {
		t.Error("Expected cleanup ticker to be running when TTL is configured")
	}
}

func TestAutoCleanupDefault(t *testing.T) {
	// Test that disabling auto cleanup overrides cleanup interval
	service := NewSessionService()
	defer service.Close()

	// Verify cleanup interval remains as set when auto cleanup is disabled
	if service.opts.cleanupInterval != 0 {
		t.Errorf("Expected cleanup interval to remain 0, got %v", service.opts.cleanupInterval)
	}

	// Verify cleanup ticker is not running when auto cleanup is disabled
	if service.cleanupTicker != nil {
		t.Errorf("Expected cleanup ticker to be nil when auto cleanup is disabled, got %v", service.cleanupTicker)
	}

	if service.opts.sessionTTL != 0 {
		t.Errorf("Expected session TTL to remain 0, got %v", service.opts.sessionTTL)
	}

	if service.opts.appStateTTL != 0 {
		t.Errorf("Expected app state TTL to remain 0, got %v", service.opts.appStateTTL)
	}

	if service.opts.userStateTTL != 0 {
		t.Errorf("Expected user state TTL to remain 0, got %v", service.opts.userStateTTL)
	}
}

func TestAutoCleanupWithDefaultInterval(t *testing.T) {
	// Create service with TTL but no explicit cleanup interval
	// Should use default 5 minute interval
	service := NewSessionService(
		WithSessionTTL(100*time.Millisecond),
		WithAppStateTTL(100*time.Millisecond),
		WithUserStateTTL(100*time.Millisecond),
	)
	defer service.Close()

	ctx := context.Background()

	// Create session
	sessionKey := session.Key{
		AppName:   "test-app",
		UserID:    "user1",
		SessionID: "session1",
	}
	sessionState := session.StateMap{
		"key1": []byte("value1"),
	}
	service.CreateSession(ctx, sessionKey, sessionState)

	// Create app state
	appState := session.StateMap{
		"app_key": []byte("app_value"),
	}
	service.UpdateAppState(ctx, "test-app", appState)

	// Create user state
	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user1",
	}
	userState := session.StateMap{
		"user_key": []byte("user_value"),
	}
	service.UpdateUserState(ctx, userKey, userState)

	// Verify cleanup ticker is running
	if service.cleanupTicker == nil {
		t.Error("Expected cleanup ticker to be running")
	}

	// Verify default cleanup interval is set
	expectedInterval := 5 * time.Minute
	if service.opts.cleanupInterval != expectedInterval {
		t.Errorf("Expected cleanup interval %v, got %v", expectedInterval, service.opts.cleanupInterval)
	}

	// Wait for data to expire
	time.Sleep(150 * time.Millisecond)

	// Data should still exist (not cleaned up yet by background routine)
	sess, _ := service.GetSession(ctx, sessionKey)
	if sess != nil {
		t.Error("Expected session to be expired when accessed")
	}

	// But expired data should still be in memory until cleanup
	service.mu.RLock()
	app, exists := service.apps["test-app"]
	service.mu.RUnlock()
	if !exists {
		t.Error("Expected app to exist in memory before cleanup")
		return
	}

	app.mu.RLock()
	sessionExists := len(app.sessions["user1"]) > 0
	app.mu.RUnlock()
	if !sessionExists {
		t.Error("Expected expired session to still be in memory before cleanup")
	}
}

func TestAutoCleanupWithCustomInterval(t *testing.T) {
	// Create service with custom cleanup interval
	service := NewSessionService(
		WithSessionTTL(100*time.Millisecond),
		WithCleanupInterval(200*time.Millisecond),
	)
	defer service.Close()

	ctx := context.Background()

	// Create session
	sessionKey := session.Key{
		AppName:   "test-app",
		UserID:    "user1",
		SessionID: "session1",
	}
	sessionState := session.StateMap{
		"key1": []byte("value1"),
	}
	service.CreateSession(ctx, sessionKey, sessionState)

	// Verify custom cleanup interval is set
	expectedInterval := 200 * time.Millisecond
	if service.opts.cleanupInterval != expectedInterval {
		t.Errorf("Expected cleanup interval %v, got %v", expectedInterval, service.opts.cleanupInterval)
	}

	// Wait for data to expire and cleanup to run
	time.Sleep(350 * time.Millisecond)

	// Data should be cleaned up from memory
	service.mu.RLock()
	app, exists := service.apps["test-app"]
	service.mu.RUnlock()

	if exists {
		app.mu.RLock()
		sessionExists := len(app.sessions) > 0 && len(app.sessions["user1"]) > 0
		app.mu.RUnlock()
		if sessionExists {
			t.Error("Expected expired session to be cleaned up from memory")
		}
	}

	// Verify session is not accessible
	sess, _ := service.GetSession(ctx, sessionKey)
	if sess != nil {
		t.Error("Expected session to be nil after cleanup")
	}
}

func TestCleanupRoutineLifecycle(t *testing.T) {
	service := NewSessionService(
		WithSessionTTL(1*time.Minute),
		WithCleanupInterval(100*time.Millisecond),
	)

	// Verify cleanup routine is started
	if service.cleanupTicker == nil {
		t.Error("Expected cleanup ticker to be running")
	}

	// Close service
	err := service.Close()
	if err != nil {
		t.Errorf("Unexpected error closing service: %v", err)
	}

	// Verify cleanup routine is stopped
	if service.cleanupTicker != nil {
		t.Error("Expected cleanup ticker to be stopped after close")
	}

	// Calling close again should be safe
	err = service.Close()
	if err != nil {
		t.Errorf("Unexpected error closing service twice: %v", err)
	}
}
