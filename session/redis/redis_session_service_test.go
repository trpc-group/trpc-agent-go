//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func setupTestRedis(t testing.TB) (*redis.Client, func()) {
	mr, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	cleanup := func() {
		client.Close()
		mr.Close()
	}

	return client, cleanup
}

func TestService_CreateSession(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T) (session.Key, session.StateMap)
		validate func(t *testing.T, sess *session.Session, err error, key session.Key, state session.StateMap)
	}{
		{
			name: "valid_session_creation",
			setup: func(t *testing.T) (session.Key, session.StateMap) {
				key := session.Key{
					AppName: "testapp",
					UserID:  "user123",
				}
				state := session.StateMap{
					"key1": []byte("value1"),
					"key2": []byte("value2"),
				}
				return key, state
			},
			validate: func(t *testing.T, sess *session.Session, err error, key session.Key, state session.StateMap) {
				require.NoError(t, err)
				assert.NotNil(t, sess)
				assert.Equal(t, key.AppName, sess.AppName)
				assert.Equal(t, key.UserID, sess.UserID)
				assert.NotEmpty(t, sess.ID)
				assert.NotZero(t, sess.CreatedAt)
				assert.NotZero(t, sess.UpdatedAt)
				assert.Equal(t, 0, len(sess.Events))
				for k, v := range state {
					assert.Equal(t, v, sess.State[k])
				}
			},
		},
		{
			name: "session_with_predefined_id",
			setup: func(t *testing.T) (session.Key, session.StateMap) {
				key := session.Key{
					AppName:   "testapp",
					UserID:    "user123",
					SessionID: "predefined-session-id",
				}
				return key, session.StateMap{}
			},
			validate: func(t *testing.T, sess *session.Session, err error, key session.Key, state session.StateMap) {
				require.NoError(t, err)
				assert.NotNil(t, sess)
				assert.Equal(t, key.SessionID, sess.ID)
				assert.Equal(t, key.AppName, sess.AppName)
				assert.Equal(t, key.UserID, sess.UserID)
			},
		},
		{
			name: "empty_state_creation",
			setup: func(t *testing.T) (session.Key, session.StateMap) {
				key := session.Key{
					AppName: "testapp",
					UserID:  "user456",
				}
				return key, session.StateMap{}
			},
			validate: func(t *testing.T, sess *session.Session, err error, key session.Key, state session.StateMap) {
				require.NoError(t, err)
				assert.NotNil(t, sess)
				assert.Equal(t, 0, len(sess.State))
			},
		},
		{
			name: "invalid_key_missing_app_name",
			setup: func(t *testing.T) (session.Key, session.StateMap) {
				key := session.Key{
					UserID: "user123",
				}
				return key, session.StateMap{}
			},
			validate: func(t *testing.T, sess *session.Session, err error, key session.Key, state session.StateMap) {
				assert.Error(t, err)
				assert.Nil(t, sess)
			},
		},
		{
			name: "invalid_key_missing_user_id",
			setup: func(t *testing.T) (session.Key, session.StateMap) {
				key := session.Key{
					AppName: "testapp",
				}
				return key, session.StateMap{}
			},
			validate: func(t *testing.T, sess *session.Session, err error, key session.Key, state session.StateMap) {
				assert.Error(t, err)
				assert.Nil(t, sess)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClient(client))
			require.NoError(t, err)

			key, state := tt.setup(t)
			sess, err := service.CreateSession(context.Background(), key, state)

			tt.validate(t, sess, err, key, state)
		})
	}
}

func TestService_AppendEvent_UpdateTime(t *testing.T) {
	tests := []struct {
		name        string
		setupEvents func() []*event.Event
		validate    func(t *testing.T, initialTime time.Time, finalSess *session.Session, events []*event.Event)
	}{
		{
			name: "single_event_updates_time",
			setupEvents: func() []*event.Event {
				return []*event.Event{
					{
						Response: &model.Response{
							Object: "test_message",
							Done:   false,
						},
						InvocationID: "invocation123",
						Author:       "test-agent",
						ID:           "event123",
						Timestamp:    time.Now(),
					},
				}
			},
			validate: func(t *testing.T, initialTime time.Time, finalSess *session.Session, events []*event.Event) {
				assert.True(t, finalSess.UpdatedAt.After(initialTime),
					"UpdatedAt should be updated after appending event. Initial: %v, Updated: %v",
					initialTime, finalSess.UpdatedAt)
				assert.Equal(t, 1, len(finalSess.Events))
				assert.Equal(t, events[0].ID, finalSess.Events[0].ID)
			},
		},
		{
			name: "multiple_events_update_time_progressively",
			setupEvents: func() []*event.Event {
				events := make([]*event.Event, 3)
				for i := 0; i < 3; i++ {
					events[i] = &event.Event{
						Response: &model.Response{
							Object: "test_message",
							Done:   false,
						},
						InvocationID: "invocation123",
						Author:       "test-agent",
						ID:           fmt.Sprintf("event%d", i),
						Timestamp:    time.Now().Add(time.Duration(i) * time.Millisecond),
					}
				}
				return events
			},
			validate: func(t *testing.T, initialTime time.Time, finalSess *session.Session, events []*event.Event) {
				assert.True(t, finalSess.UpdatedAt.After(initialTime))
				assert.Equal(t, len(events), len(finalSess.Events))

				// Redis returns events in reverse chronological order (newest first)
				// event2 (newest), event1, event0 (oldest)
				assert.Equal(t, "event2", finalSess.Events[0].ID)
				assert.Equal(t, "event1", finalSess.Events[1].ID)
				assert.Equal(t, "event0", finalSess.Events[2].ID)
			},
		},
		{
			name: "events_with_different_timestamps",
			setupEvents: func() []*event.Event {
				baseTime := time.Now()
				return []*event.Event{
					{
						Response:     &model.Response{Object: "message1", Done: false},
						InvocationID: "inv1", Author: "agent1", ID: "event1",
						Timestamp: baseTime.Add(-2 * time.Hour),
					},
					{
						Response:     &model.Response{Object: "message2", Done: true},
						InvocationID: "inv2", Author: "agent2", ID: "event2",
						Timestamp: baseTime.Add(-1 * time.Hour),
					},
				}
			},
			validate: func(t *testing.T, initialTime time.Time, finalSess *session.Session, events []*event.Event) {
				assert.Equal(t, 2, len(finalSess.Events))
				// Redis returns events in reverse chronological order (newest first)
				// event2 (newer timestamp) should come before event1
				assert.Equal(t, "event2", finalSess.Events[0].ID)
				assert.Equal(t, "event1", finalSess.Events[1].ID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClient(client))
			require.NoError(t, err)

			// Create a session first
			sessionKey := session.Key{
				AppName:   "testapp",
				UserID:    "user123",
				SessionID: "session123",
			}

			initialState := session.StateMap{
				"initial_key": []byte("initial_value"),
			}

			sess, err := service.CreateSession(context.Background(), sessionKey, initialState)
			require.NoError(t, err)

			initialUpdateTime := sess.UpdatedAt

			// Wait a bit to ensure timestamp difference
			time.Sleep(10 * time.Millisecond)

			// Setup and append events
			events := tt.setupEvents()
			for _, evt := range events {
				err = service.AppendEvent(context.Background(), sess, evt)
				require.NoError(t, err)

				// Small delay between events for timestamp differences
				time.Sleep(1 * time.Millisecond)
			}

			// Retrieve final session state
			finalSess, err := service.GetSession(context.Background(), sessionKey)
			require.NoError(t, err)
			assert.NotNil(t, finalSess)

			// Verify CreatedAt remains unchanged
			assert.Equal(t, sess.CreatedAt.Unix(), finalSess.CreatedAt.Unix())

			// Run custom validation
			tt.validate(t, initialUpdateTime, finalSess, events)
		})
	}
}

func TestService_AppendEvent_ErrorCases(t *testing.T) {
	tests := []struct {
		name          string
		setup         func(t *testing.T, service *Service) *session.Session
		event         *event.Event
		expectedError string
	}{
		{
			name: "invalid_session_key_in_session_object",
			setup: func(t *testing.T, service *Service) *session.Session {
				// Create a session object with an invalid key (missing SessionID)
				return &session.Session{
					AppName: "testapp",
					UserID:  "user123",
					// Missing SessionID
				}
			},
			event: &event.Event{
				Response:     &model.Response{Object: "test", Done: false},
				InvocationID: "inv123",
				Author:       "agent",
				ID:           "event123",
				Timestamp:    time.Now(),
			},
			expectedError: "sessionID is required",
		},
		{
			name: "append_to_non_existent_session_in_redis",
			setup: func(t *testing.T, service *Service) *session.Session {
				// This session object is valid, but it hasn't been created in Redis.
				return &session.Session{
					AppName: "testapp",
					UserID:  "user123",
					ID:      "nonexistent",
				}
			},
			event: &event.Event{
				Response:     &model.Response{Object: "test", Done: false},
				InvocationID: "inv123",
				Author:       "agent",
				ID:           "event123",
				Timestamp:    time.Now(),
			},
			expectedError: "redis: nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClient(client))
			require.NoError(t, err)

			sess := tt.setup(t, service)

			err = service.AppendEvent(context.Background(), sess, tt.event)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}

func TestService_GetSession(t *testing.T) {
	// setup function to create test data for each test case
	setup := func(t *testing.T, service *Service) time.Time {
		ctx := context.Background()
		baseTime := time.Now()

		// Session 1: with events for filtering tests
		sess1Key := session.Key{AppName: "testapp", UserID: "user1", SessionID: "session1"}
		sess1, err := service.CreateSession(ctx, sess1Key, session.StateMap{"key": []byte("val")})
		require.NoError(t, err)

		events := []*event.Event{
			{ID: "event1", Timestamp: baseTime.Add(-4 * time.Hour)},
			{ID: "event2", Timestamp: baseTime.Add(-3 * time.Hour)},
			{ID: "event3", Timestamp: baseTime.Add(-2 * time.Hour)},
			{ID: "event4", Timestamp: baseTime.Add(-1 * time.Hour)},
		}
		for _, evt := range events {
			err := service.AppendEvent(ctx, sess1, evt, session.WithEventTime(evt.Timestamp))
			require.NoError(t, err)
		}

		// Session 2: simple session without extra events from this setup
		sess2Key := session.Key{AppName: "testapp", UserID: "user1", SessionID: "session2"}
		_, err = service.CreateSession(ctx, sess2Key, session.StateMap{})
		require.NoError(t, err)

		return baseTime
	}

	tests := []struct {
		name     string
		setup    func(service *Service, baseTime time.Time) (*session.Session, error)
		validate func(t *testing.T, sess *session.Session, err error, baseTime time.Time)
	}{
		{
			name: "get existing session",
			setup: func(service *Service, baseTime time.Time) (*session.Session, error) {
				return service.GetSession(
					context.Background(),
					session.Key{AppName: "testapp", UserID: "user1", SessionID: "session2"})
			},
			validate: func(t *testing.T, sess *session.Session, err error, baseTime time.Time) {
				require.NoError(t, err)
				assert.NotNil(t, sess)
				assert.Equal(t, "session2", sess.ID)
			},
		},
		{
			name: "get nonexistent session",
			setup: func(service *Service, baseTime time.Time) (*session.Session, error) {
				return service.GetSession(
					context.Background(),
					session.Key{AppName: "testapp", UserID: "user1", SessionID: "nonexistent"},
				)
			},
			validate: func(t *testing.T, sess *session.Session, err error, baseTime time.Time) {
				// Redis returns nil session without error for nonexistent sessions
				assert.NoError(t, err)
				assert.Nil(t, sess)
			},
		},
		{
			name: "get session with events",
			setup: func(service *Service, baseTime time.Time) (*session.Session, error) {
				return service.GetSession(
					context.Background(),
					session.Key{AppName: "testapp", UserID: "user1", SessionID: "session1"},
				)
			},
			validate: func(t *testing.T, sess *session.Session, err error, baseTime time.Time) {
				require.NoError(t, err)
				assert.NotNil(t, sess)
				assert.Len(t, sess.Events, 4)
			},
		},
		{
			name: "get session with EventNum option",
			setup: func(service *Service, baseTime time.Time) (*session.Session, error) {
				return service.GetSession(
					context.Background(),
					session.Key{AppName: "testapp", UserID: "user1", SessionID: "session1"},
					session.WithEventNum(2),
				)
			},
			validate: func(t *testing.T, sess *session.Session, err error, baseTime time.Time) {
				require.NoError(t, err)
				require.NotNil(t, sess)
				require.Len(t, sess.Events, 2)
				// Redis uses ZRevRangeByScore, returning newest events first
				assert.Equal(t, "event4", sess.Events[0].ID)
				assert.Equal(t, "event3", sess.Events[1].ID)
			},
		},
		{
			name: "get session with EventTime option",
			setup: func(service *Service, baseTime time.Time) (*session.Session, error) {
				return service.GetSession(
					context.Background(),
					session.Key{AppName: "testapp", UserID: "user1", SessionID: "session1"},
					session.WithEventTime(baseTime.Add(-2*time.Hour-30*time.Minute)),
				)
			},
			validate: func(t *testing.T, sess *session.Session, err error, baseTime time.Time) {
				require.NoError(t, err)
				require.NotNil(t, sess)
				require.Len(t, sess.Events, 2)
				// Redis returns events after the specified time in reverse chronological order (newest first)
				assert.Equal(t, "event4", sess.Events[0].ID)
				assert.Equal(t, "event3", sess.Events[1].ID)
			},
		},
		{
			name: "get session with both EventNum and EventTime",
			setup: func(service *Service, baseTime time.Time) (*session.Session, error) {
				return service.GetSession(
					context.Background(),
					session.Key{AppName: "testapp", UserID: "user1", SessionID: "session1"},
					session.WithEventNum(1),
					session.WithEventTime(baseTime.Add(-3*time.Hour-30*time.Minute)),
				)
			},
			validate: func(t *testing.T, sess *session.Session, err error, baseTime time.Time) {
				require.NoError(t, err)
				require.NotNil(t, sess)
				require.Len(t, sess.Events, 1)
				// Redis returns the newest event after the time filter
				assert.Equal(t, "event4", sess.Events[0].ID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClient(client))
			require.NoError(t, err)

			// Setup initial data for all test cases.
			baseTime := setup(t, service)

			sess, err := tt.setup(service, baseTime)
			tt.validate(t, sess, err, baseTime)
		})
	}
}

func TestService_Atomicity(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, service *Service, sessionKey session.Key) error
		validate func(t *testing.T, client *redis.Client, service *Service, sessionKey session.Key, err error)
	}{
		{
			name: "append_event_atomicity",
			setup: func(t *testing.T, service *Service, sessionKey session.Key) error {
				testEvent := &event.Event{
					Response:     &model.Response{Object: "test", Done: false},
					InvocationID: "inv123", Author: "agent", ID: "event123", Timestamp: time.Now(),
				}
				return service.addEvent(context.Background(), sessionKey, testEvent)
			},
			validate: func(t *testing.T, client *redis.Client, service *Service, sessionKey session.Key, err error) {
				require.NoError(t, err)

				finalSess, err := service.GetSession(context.Background(), sessionKey)
				require.NoError(t, err)

				assert.Equal(t, 1, len(finalSess.Events))
				assert.Equal(t, "event123", finalSess.Events[0].ID)

				// Verify Redis state consistency
				sessStateKey := getSessionStateKey(sessionKey)
				eventKey := getEventKey(sessionKey)

				// Check session state in Redis
				sessStateData, err := client.HGet(context.Background(), sessStateKey, sessionKey.SessionID).Result()
				require.NoError(t, err)

				var redisSessionState SessionState
				err = json.Unmarshal([]byte(sessStateData), &redisSessionState)
				require.NoError(t, err)
				assert.True(t, redisSessionState.UpdatedAt.After(finalSess.CreatedAt))

				// Check event in Redis
				eventCount, err := client.ZCard(context.Background(), eventKey).Result()
				require.NoError(t, err)
				assert.Equal(t, int64(1), eventCount)
			},
		},
		{
			name: "delete_session_atomicity",
			setup: func(t *testing.T, service *Service, sessionKey session.Key) error {
				testEvent := &event.Event{
					Response:     &model.Response{Object: "test", Done: false},
					InvocationID: "inv123", Author: "agent", ID: "event123", Timestamp: time.Now(),
				}
				sess, err := service.GetSession(context.Background(), sessionKey)
				require.NoError(t, err)
				err = service.AppendEvent(context.Background(), sess, testEvent)
				require.NoError(t, err)
				return service.DeleteSession(context.Background(), sessionKey)
			},
			validate: func(t *testing.T, client *redis.Client, service *Service, sessionKey session.Key, err error) {
				require.NoError(t, err)

				// Verify both session and events are deleted
				sess, getErr := service.GetSession(context.Background(), sessionKey)

				// Handle known miniredis pipeline limitation when accessing deleted sessions
				if getErr != nil && strings.Contains(getErr.Error(), "redis: nil") {
					sess = nil
					getErr = nil
				}
				require.NoError(t, getErr)
				assert.Nil(t, sess)

				// Verify events are also deleted
				eventKey := getEventKey(sessionKey)
				count, err := client.ZCard(context.Background(), eventKey).Result()
				require.NoError(t, err)
				assert.Equal(t, int64(0), count)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClient(client))
			require.NoError(t, err)

			sessionKey := session.Key{
				AppName: "testapp", UserID: "user123", SessionID: "session123",
			}
			// Create session is common for both tests.
			_, err = service.CreateSession(context.Background(), sessionKey, session.StateMap{})
			require.NoError(t, err)

			err = tt.setup(t, service, sessionKey)
			tt.validate(t, client, service, sessionKey, err)
		})
	}
}
