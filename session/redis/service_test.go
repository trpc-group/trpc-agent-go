//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/hashidx"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/util"
)

func TestNewService(t *testing.T) {
	tests := []struct {
		name        string
		options     []ServiceOpt
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid redis URL",
			options:     []ServiceOpt{WithRedisClientURL("redis://localhost:6379")},
			expectError: false,
		},
		{
			name:        "missing redis URL and client",
			options:     []ServiceOpt{},
			expectError: true,
			errorMsg:    "redis",
		},
		{
			name: "with prefix",
			options: []ServiceOpt{
				WithRedisClientURL("redis://localhost:6379"),
				WithKeyPrefix("myprefix"),
			},
			expectError: false,
		},
		{
			name: "with session TTL",
			options: []ServiceOpt{
				WithRedisClientURL("redis://localhost:6379"),
				WithSessionTTL(time.Hour),
			},
			expectError: false,
		},
		{
			name: "with app state TTL",
			options: []ServiceOpt{
				WithRedisClientURL("redis://localhost:6379"),
				WithAppStateTTL(2 * time.Hour),
			},
			expectError: false,
		},
		{
			name: "with user state TTL",
			options: []ServiceOpt{
				WithRedisClientURL("redis://localhost:6379"),
				WithUserStateTTL(30 * time.Minute),
			},
			expectError: false,
		},
		{
			name: "with async persist",
			options: []ServiceOpt{
				WithRedisClientURL("redis://localhost:6379"),
				WithEnableAsyncPersist(true),
				WithAsyncPersisterNum(4),
			},
			expectError: false,
		},
		{
			name: "with summary options",
			options: []ServiceOpt{
				WithRedisClientURL("redis://localhost:6379"),
				WithAsyncSummaryNum(2),
				WithSummaryQueueSize(100),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewService(tt.options...)
			if tt.expectError {
				require.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, service)
				if service != nil {
					service.Close()
				}
			}
		})
	}
}

func TestService_CreateSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	t.Run("basic creation", func(t *testing.T) {
		key := session.Key{
			AppName: "testapp",
			UserID:  "user123",
		}
		sess, err := service.CreateSession(ctx, key, session.StateMap{
			"key1": []byte("value1"),
		})
		require.NoError(t, err)
		assert.NotNil(t, sess)
		assert.NotEmpty(t, sess.ID)
		assert.Equal(t, "testapp", sess.AppName)
		assert.Equal(t, "user123", sess.UserID)
		assert.Equal(t, []byte("value1"), sess.State["key1"])
	})

	t.Run("with nil state", func(t *testing.T) {
		key := session.Key{
			AppName: "testapp",
			UserID:  "user123",
		}
		sess, err := service.CreateSession(ctx, key, nil)
		require.NoError(t, err)
		assert.NotNil(t, sess)
		assert.Empty(t, sess.State)
	})

	t.Run("empty app name", func(t *testing.T) {
		key := session.Key{
			AppName: "",
			UserID:  "user123",
		}
		_, err := service.CreateSession(ctx, key, session.StateMap{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "appName")
	})

	t.Run("empty user id", func(t *testing.T) {
		key := session.Key{
			AppName: "testapp",
			UserID:  "",
		}
		_, err := service.CreateSession(ctx, key, session.StateMap{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "userID")
	})

	t.Run("with session ID", func(t *testing.T) {
		key := session.Key{
			AppName:   "testapp",
			UserID:    "user123",
			SessionID: "custom-session-id",
		}
		sess, err := service.CreateSession(ctx, key, session.StateMap{})
		require.NoError(t, err)
		assert.Equal(t, "custom-session-id", sess.ID)
	})

	t.Run("generates session ID if empty", func(t *testing.T) {
		key := session.Key{
			AppName: "testapp",
			UserID:  "user123",
		}
		sess, err := service.CreateSession(ctx, key, session.StateMap{})
		require.NoError(t, err)
		assert.NotEmpty(t, sess.ID)
	})

	t.Run("creation with prefix", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()

		prefixService, err := NewService(
			WithRedisClientURL(redisURL),
			WithKeyPrefix("testprefix"),
		)
		require.NoError(t, err)
		defer prefixService.Close()

		key := session.Key{
			AppName:   "testapp",
			UserID:    "user123",
			SessionID: "prefix-session",
		}
		sess, err := prefixService.CreateSession(ctx, key, session.StateMap{"test": []byte("data")})
		require.NoError(t, err)
		assert.Equal(t, "prefix-session", sess.ID)

		// Verify session can be retrieved with the same prefix
		retrieved, err := prefixService.GetSession(ctx, key)
		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.Equal(t, "prefix-session", retrieved.ID)
		assert.Equal(t, []byte("data"), retrieved.State["test"])
	})
}

func TestService_UpdateSessionState_NilValue(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	// Create a session with initial state
	sess, err := service.CreateSession(ctx, key, session.StateMap{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
	})
	require.NoError(t, err)

	// Add an event with state delta that sets key1 to nil.
	// In V2 (hashidx), nil values in StateDelta are no-ops because cjson.null
	// does not overwrite existing values when re-encoded. The key is preserved.
	evt := createTestEvent("e1", "agent", "content", time.Now(), false)
	evt.StateDelta = session.StateMap{
		"key1": nil,
	}
	err = service.AppendEvent(ctx, sess, evt)
	require.NoError(t, err)

	// Get session and verify state — key1 remains because V2 Lua script
	// treats cjson.null as a no-op for state merge.
	retrieved, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), retrieved.State["key1"])
	assert.Equal(t, []byte("value2"), retrieved.State["key2"])
}

func TestService_UpdateSessionState_CopiesValue(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	// Create a session with initial state
	initialState := session.StateMap{
		"counter": []byte("0"),
	}
	sess, err := service.CreateSession(ctx, key, initialState)
	require.NoError(t, err)

	// Update counter via event state delta
	evt := createTestEvent("e1", "agent", "content", time.Now(), false)
	evt.StateDelta = session.StateMap{
		"counter": []byte("1"),
	}
	err = service.AppendEvent(ctx, sess, evt)
	require.NoError(t, err)

	// Verify original state map was not modified
	assert.Equal(t, []byte("0"), initialState["counter"])

	// Verify stored state was updated
	retrieved, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, []byte("1"), retrieved.State["counter"])
}

func TestService_GetSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	// Create a session with state
	_, err = service.CreateSession(ctx, key, session.StateMap{
		"key1": []byte("value1"),
	})
	require.NoError(t, err)

	t.Run("basic get", func(t *testing.T) {
		sess, err := service.GetSession(ctx, key)
		require.NoError(t, err)
		assert.NotNil(t, sess)
		assert.Equal(t, "session123", sess.ID)
		assert.Equal(t, "testapp", sess.AppName)
		assert.Equal(t, "user123", sess.UserID)
		assert.Equal(t, []byte("value1"), sess.State["key1"])
	})

	t.Run("non-existent session", func(t *testing.T) {
		sess, err := service.GetSession(ctx, session.Key{
			AppName:   "testapp",
			UserID:    "user123",
			SessionID: "non-existent",
		})
		require.NoError(t, err)
		assert.Nil(t, sess)
	})

	t.Run("with events", func(t *testing.T) {
		eventKey := session.Key{
			AppName:   "testapp",
			UserID:    "user123",
			SessionID: "session-with-events",
		}
		sess, err := service.CreateSession(ctx, eventKey, session.StateMap{})
		require.NoError(t, err)

		// Append some events
		base := time.Now()
		for i := 0; i < 3; i++ {
			evt := createTestEvent(fmt.Sprintf("e%d", i), "agent", fmt.Sprintf("content%d", i), base.Add(time.Duration(i)*time.Second), false)
			err = service.AppendEvent(ctx, sess, evt)
			require.NoError(t, err)
		}

		// Get session
		retrieved, err := service.GetSession(ctx, eventKey)
		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.Len(t, retrieved.Events, 3)
	})

	t.Run("with event num", func(t *testing.T) {
		eventKey := session.Key{
			AppName:   "testapp",
			UserID:    "user123",
			SessionID: "session-eventnum",
		}
		sess, err := service.CreateSession(ctx, eventKey, session.StateMap{})
		require.NoError(t, err)

		base := time.Now()
		for i := 0; i < 5; i++ {
			evt := createTestEvent(fmt.Sprintf("en%d", i), "agent", fmt.Sprintf("content%d", i), base.Add(time.Duration(i)*time.Second), false)
			err = service.AppendEvent(ctx, sess, evt)
			require.NoError(t, err)
		}

		// Get session with event num = 3 (latest 3)
		retrieved, err := service.GetSession(ctx, eventKey, session.WithEventNum(3))
		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.LessOrEqual(t, len(retrieved.Events), 5)
	})

	t.Run("empty app name", func(t *testing.T) {
		_, err := service.GetSession(ctx, session.Key{
			AppName:   "",
			UserID:    "user123",
			SessionID: "session123",
		})
		assert.Error(t, err)
	})

	t.Run("empty user id", func(t *testing.T) {
		_, err := service.GetSession(ctx, session.Key{
			AppName:   "testapp",
			UserID:    "",
			SessionID: "session123",
		})
		assert.Error(t, err)
	})

	t.Run("empty session id", func(t *testing.T) {
		_, err := service.GetSession(ctx, session.Key{
			AppName:   "testapp",
			UserID:    "user123",
			SessionID: "",
		})
		assert.Error(t, err)
	})
}

func TestService_Atomicity(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	// Create session
	sess, err := service.CreateSession(ctx, key, session.StateMap{
		"counter": []byte("0"),
	})
	require.NoError(t, err)

	// Concurrent event appends
	const numGoroutines = 10
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			evt := createTestEvent(fmt.Sprintf("e%d", idx), "agent", fmt.Sprintf("content%d", idx), time.Now(), false)
			evt.StateDelta = session.StateMap{
				fmt.Sprintf("key_%d", idx): []byte(fmt.Sprintf("value_%d", idx)),
			}
			_ = service.AppendEvent(ctx, sess, evt)
		}(i)
	}

	wg.Wait()

	// Verify session state has all keys
	retrieved, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	// All goroutines should have written their keys
	for i := 0; i < numGoroutines; i++ {
		k := fmt.Sprintf("key_%d", i)
		assert.Equal(t, []byte(fmt.Sprintf("value_%d", i)), retrieved.State[k], "Missing key %s", k)
	}
}

func TestService_Close_MultipleTimes(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)

	// Close multiple times should not panic
	err1 := service.Close()
	assert.NoError(t, err1)

	err2 := service.Close()
	assert.NoError(t, err2)
}

func TestService_ConcurrentSessions(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	const numSessions = 10

	// Create multiple sessions concurrently
	var wg sync.WaitGroup
	sessions := make([]*session.Session, numSessions)
	keys := make([]session.Key, numSessions)

	for i := 0; i < numSessions; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := session.Key{
				AppName:   "testapp",
				UserID:    fmt.Sprintf("user%d", idx),
				SessionID: fmt.Sprintf("session%d", idx),
			}
			keys[idx] = key

			sess, err := service.CreateSession(ctx, key, session.StateMap{
				"user_id": []byte(fmt.Sprintf("user%d", idx)),
			})
			require.NoError(t, err)
			sessions[idx] = sess
		}(i)
	}

	wg.Wait()

	// Verify all sessions were created
	for i, sess := range sessions {
		require.NotNil(t, sess)
		assert.Equal(t, fmt.Sprintf("session%d", i), sess.ID)
		assert.Equal(t, fmt.Sprintf("user%d", i), sess.UserID)
		assert.Equal(t, "testapp", sess.AppName)
		assert.Equal(t, fmt.Sprintf("user%d", i), string(sess.State["user_id"]))
	}

	// Verify all sessions can be retrieved
	for i, key := range keys {
		retrieved, err := service.GetSession(ctx, key)
		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.Equal(t, fmt.Sprintf("session%d", i), retrieved.ID)
	}
}

func TestService_SessionStateConsistency(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	// Create session with initial state
	initialState := session.StateMap{
		"counter": []byte("0"),
		"status":  []byte("active"),
	}
	sess, err := service.CreateSession(ctx, key, initialState)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "0", string(sess.State["counter"]))
	assert.Equal(t, "active", string(sess.State["status"]))

	// Update app state
	appState := session.StateMap{
		"global_config": []byte("enabled"),
	}
	err = service.UpdateAppState(ctx, key.AppName, appState)
	require.NoError(t, err)

	// Update user state
	userState := session.StateMap{
		"user_pref": []byte("dark_mode"),
	}
	err = service.UpdateUserState(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID}, userState)
	require.NoError(t, err)

	// Retrieve session and verify merged state
	retrievedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, retrievedSess)

	// Check session-specific state
	assert.Equal(t, "0", string(retrievedSess.State["counter"]))
	assert.Equal(t, "active", string(retrievedSess.State["status"]))

	// Check app state (prefixed)
	assert.Equal(t, "enabled", string(retrievedSess.State[session.StateAppPrefix+"global_config"]))

	// Check user state (prefixed)
	assert.Equal(t, "dark_mode", string(retrievedSess.State[session.StateUserPrefix+"user_pref"]))
}

func TestDeleteSessionState(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "test-session",
	}

	// Create a session first
	sess, err := service.CreateSession(ctx, key, session.StateMap{"test": []byte("data")})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Verify session exists
	retrievedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, retrievedSess)

	// Delete session state
	err = service.DeleteSession(ctx, key)
	require.NoError(t, err)

	// Verify session is deleted
	deletedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, deletedSess)
}

func TestService_CreateSession_WithOptions(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName: "testapp",
		UserID:  "user123",
	}

	// Create session with event num option
	sess, err := service.CreateSession(ctx, key, session.StateMap{}, session.WithEventNum(10))
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.NotEmpty(t, sess.ID)

	// Create session with event time option
	sess2, err := service.CreateSession(ctx, key, session.StateMap{}, session.WithEventTime(time.Now()))
	require.NoError(t, err)
	assert.NotNil(t, sess2)
	assert.NotEmpty(t, sess2.ID)
}

func TestService_GetSession_WithOptions(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	// Create a session
	_, err = service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	// Append some events
	for i := 0; i < 5; i++ {
		evt := createTestEvent(fmt.Sprintf("e%d", i), "agent", "content", time.Now(), false)
		sess, _ := service.GetSession(ctx, key)
		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
	}

	// Get session with event num option
	sess, err := service.GetSession(ctx, key, session.WithEventNum(3))
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.LessOrEqual(t, len(sess.Events), 5)

	// Get session with event time option
	sess2, err := service.GetSession(ctx, key, session.WithEventTime(time.Now().Add(-1*time.Hour)))
	require.NoError(t, err)
	assert.NotNil(t, sess2)
}

func TestService_GetSession_AttachSummaries(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session-with-summary",
	}

	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	evt := createTestEvent("evt-summary", "agent", "content", time.Now(), false)
	err = service.AppendEvent(ctx, sess, evt)
	require.NoError(t, err)

	sumMap := map[string]*session.Summary{
		"": {
			Summary:   "cached-summary",
			UpdatedAt: time.Now().UTC(),
		},
	}
	payload, err := json.Marshal(sumMap)
	require.NoError(t, err)
	client := buildRedisClient(t, redisURL)
	// Use String key format
	v2SummaryKey := hashidx.GetSessionSummaryKey("", key)
	err = client.Set(ctx, v2SummaryKey, string(payload), 0).Err()
	require.NoError(t, err)

	got, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.Summaries)
	sum, ok := got.Summaries[""]
	require.True(t, ok)
	assert.Equal(t, "cached-summary", sum.Summary)
}

func TestService_ListSessions_WithOptions(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "testapp",
		UserID:  "user123",
	}

	// Create multiple sessions
	for i := 0; i < 3; i++ {
		key := session.Key{
			AppName:   userKey.AppName,
			UserID:    userKey.UserID,
			SessionID: fmt.Sprintf("session%d", i),
		}
		_, err := service.CreateSession(ctx, key, session.StateMap{})
		require.NoError(t, err)
	}

	// List sessions with event num option
	sessions, err := service.ListSessions(ctx, userKey, session.WithEventNum(10))
	require.NoError(t, err)
	assert.Len(t, sessions, 3)

	// List sessions with event time option
	sessions2, err := service.ListSessions(ctx, userKey, session.WithEventTime(time.Now().Add(-1*time.Hour)))
	require.NoError(t, err)
	assert.Len(t, sessions2, 3)
}

func TestService_ListSessions_WithListSessionOnlyMeta(t *testing.T) {
	tests := []struct {
		name        string
		options     []ServiceOpt
		storageType string
	}{
		{
			name: "hashidx",
			options: []ServiceOpt{
				WithEnableUserSessionIndex(true),
			},
			storageType: "hashidx",
		},
		{
			name: "zset transition",
			options: []ServiceOpt{
				WithEnableUserSessionIndex(true),
				WithCompatMode(CompatModeTransition),
			},
			storageType: "zset",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			options := append([]ServiceOpt{WithRedisClientURL(redisURL)}, tt.options...)
			service, err := NewService(options...)
			require.NoError(t, err)
			defer service.Close()

			ctx := context.Background()
			key := session.Key{AppName: "testapp", UserID: "user123", SessionID: "session123"}
			userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}

			err = service.UpdateAppState(ctx, key.AppName, session.StateMap{"app_key": []byte("app_value")})
			require.NoError(t, err)
			err = service.UpdateUserState(ctx, userKey, session.StateMap{"user_key": []byte("user_value")})
			require.NoError(t, err)

			sess, err := service.CreateSession(ctx, key, session.StateMap{"session_key": []byte("session_value")})
			require.NoError(t, err)

			evt := event.New("test-invocation", "author")
			evt.Response = &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "hello",
					},
				}},
			}
			require.NoError(t, service.AppendEvent(ctx, sess, evt))
			require.NoError(t, service.AppendTrackEvent(ctx, sess, &session.TrackEvent{
				Track:     "alpha",
				Payload:   json.RawMessage(`"track-payload"`),
				Timestamp: time.Now(),
			}))

			sessions, err := service.ListSessions(ctx, userKey, session.WithListSessionOnlyMeta())
			require.NoError(t, err)
			require.Len(t, sessions, 1)

			got := sessions[0]
			assert.Empty(t, got.Events)
			assert.Nil(t, got.Tracks)
			assert.Equal(t, []byte("session_value"), got.State["session_key"])
			assert.Equal(t, []byte("app_value"), got.State[session.StateAppPrefix+"app_key"])
			assert.Equal(t, []byte("user_value"), got.State[session.StateUserPrefix+"user_key"])
			assert.Equal(t, key.SessionID, got.ID)
			assert.Equal(t, tt.storageType, got.ServiceMeta[util.ServiceMetaStorageTypeKey])
		})
	}
}

func TestService_MergeState_Priority(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	// Set the same key at different levels with different values
	err = service.UpdateAppState(ctx, key.AppName, session.StateMap{"shared_key": []byte("app_value")})
	require.NoError(t, err)

	userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}
	err = service.UpdateUserState(ctx, userKey, session.StateMap{"shared_key": []byte("user_value")})
	require.NoError(t, err)

	// Create session with the same key
	sess, err := service.CreateSession(ctx, key, session.StateMap{"shared_key": []byte("session_value")})
	require.NoError(t, err)

	// Session-level state should take priority
	assert.Equal(t, []byte("session_value"), sess.State["shared_key"])

	// Get session to verify state merging
	retrievedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	// Session-level state should still take priority
	assert.Equal(t, []byte("session_value"), retrievedSess.State["shared_key"])
}

func TestService_ListSessions_EmptyEvents(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	client := buildRedisClient(t, redisURL)
	userKey := session.UserKey{
		AppName: "testapp",
		UserID:  "user123",
	}

	// Create sessions with staggered UpdatedAt via direct Redis overwrite
	// so that the ordering is deterministic: session4 (newest) > ... > session0 (oldest).
	numSessions := 5
	baseTime := time.Now()
	for i := 0; i < numSessions; i++ {
		key := session.Key{
			AppName:   userKey.AppName,
			UserID:    userKey.UserID,
			SessionID: fmt.Sprintf("session%d", i),
		}
		_, err := service.CreateSession(ctx, key, session.StateMap{})
		require.NoError(t, err)

		// Overwrite the session meta with a distinct updatedAt (HashIdx storage: String key + JSON).
		meta := fetchSessionState(t, ctx, client, key)
		meta["updatedAt"] = baseTime.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		stateBytes, err := json.Marshal(meta)
		require.NoError(t, err)
		err = client.Set(ctx, getExpectedSessionStateKey(key), stateBytes, 0).Err()
		require.NoError(t, err)
	}

	// List sessions
	sessions, err := service.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, numSessions)
	// All sessions should have empty events
	for _, sess := range sessions {
		assert.Empty(t, sess.Events)
	}
	// Verify descending UpdatedAt order: session4, session3, session2, session1, session0.
	for i := 0; i < numSessions; i++ {
		assert.Equal(t, fmt.Sprintf("session%d", numSessions-1-i), sessions[i].ID)
	}
	for i := 1; i < len(sessions); i++ {
		assert.False(t, sessions[i].UpdatedAt.After(sessions[i-1].UpdatedAt))
	}
}

func TestService_CreateSession_EmptyState(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName: "testapp",
		UserID:  "user123",
	}

	// Create session with empty state
	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.Empty(t, sess.State)
	assert.NotEmpty(t, sess.ID)
}

func TestService_DeleteSession_NonExistent(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "non-existent",
	}

	// Delete non-existent session should not error
	err = service.DeleteSession(ctx, key)
	require.NoError(t, err)
}

func TestService_DeleteSession_WithEvents(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	// Create a session
	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	// Append events
	for i := 0; i < 3; i++ {
		evt := createTestEvent(fmt.Sprintf("e%d", i), "agent", "content", time.Now(), false)
		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
	}

	// Delete the session
	err = service.DeleteSession(ctx, key)
	require.NoError(t, err)

	// Verify session is deleted
	deletedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, deletedSess)
}

// setupTestRedis creates a miniredis instance and returns its URL and cleanup function.
func setupTestRedis(t *testing.T) (string, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	return "redis://" + mr.Addr(), func() { mr.Close() }
}

// buildRedisClient creates a real Redis client connected to the given URL.
func buildRedisClient(t *testing.T, redisURL string) *redis.Client {
	t.Helper()
	addr := strings.TrimPrefix(redisURL, "redis://")
	client := redis.NewClient(&redis.Options{Addr: addr})
	return client
}

// createTestEvent creates a test event with reasonable defaults.
func createTestEvent(id, agent, content string, ts time.Time, done bool) *event.Event {
	return &event.Event{
		ID:        id,
		Timestamp: ts,
		Response: &model.Response{
			Done: done,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: content,
					},
				},
			},
		},
	}
}

// getExpectedSessionStateKey returns the hashidx meta key for a session (v2 default storage).
func getExpectedSessionStateKey(key session.Key) string {
	return hashidx.GetSessionMetaKey("", key)
}

// fetchSessionState reads the session meta JSON from hashidx storage as a generic map.
func fetchSessionState(t *testing.T, ctx context.Context, client *redis.Client, key session.Key) map[string]any {
	t.Helper()
	raw, err := client.Get(ctx, getExpectedSessionStateKey(key)).Bytes()
	require.NoError(t, err)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(raw, &meta))
	return meta
}
