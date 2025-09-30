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
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	isession "trpc.group/trpc-go/trpc-agent-go/internal/session"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func setupTestRedis(t testing.TB) (string, func()) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	cleanup := func() {
		mr.Close()
	}
	return "redis://" + mr.Addr(), cleanup
}

func buildRedisClient(t *testing.T, redisURL string) *redis.Client {
	opts, err := redis.ParseURL(redisURL)
	require.NoError(t, err)
	return redis.NewClient(opts)
}

// createTestEvent creates a test event with the given parameters
func createTestEvent(id, author, content string, timestamp time.Time, done bool) *event.Event {
	return &event.Event{
		ID:        id,
		Timestamp: timestamp,
		Response: &model.Response{
			Object: fmt.Sprintf("message_%s", id),
			Done:   done,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: content,
					},
				},
			},
		},
		InvocationID: fmt.Sprintf("inv_%s", id),
		Author:       author,
	}
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
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClientURL(redisURL))
			require.NoError(t, err)
			defer service.Close()

			key, state := tt.setup(t)
			sess, err := service.CreateSession(context.Background(), key, state)

			tt.validate(t, sess, err, key, state)
		})
	}
}

func TestService_AppendEvent_UpdateTime(t *testing.T) {
	tests := []struct {
		name                   string
		enableAsyncPersistence bool
		setupEvents            func() []*event.Event
		validate               func(t *testing.T, initialTime time.Time, finalSess *session.Session, events []*event.Event)
	}{
		{
			name: "single_event_updates_time",
			setupEvents: func() []*event.Event {
				return []*event.Event{
					createTestEvent("event123", "test-agent", "Test message for append event test", time.Now(), false),
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
			name:                   "single_event_updates_time_async_persistence",
			enableAsyncPersistence: true,
			setupEvents: func() []*event.Event {
				return []*event.Event{
					createTestEvent("event123", "test-agent", "Test message for append event test", time.Now(), false),
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
					events[i] = createTestEvent(
						fmt.Sprintf("event%d", i),
						"test-agent",
						fmt.Sprintf("Test message %d", i),
						time.Now().Add(time.Duration(i)*time.Millisecond),
						false,
					)
				}
				return events
			},
			validate: func(t *testing.T, initialTime time.Time, finalSess *session.Session, events []*event.Event) {
				assert.True(t, finalSess.UpdatedAt.After(initialTime))
				assert.Equal(t, len(events), len(finalSess.Events))

				// Redis returns events in chronological order (oldest first)
				// event0 (oldest), event1, event2 (newest)
				assert.Equal(t, "event0", finalSess.Events[0].ID)
				assert.Equal(t, "event1", finalSess.Events[1].ID)
				assert.Equal(t, "event2", finalSess.Events[2].ID)
			},
		},
		{
			name: "events_with_different_timestamps",
			setupEvents: func() []*event.Event {
				baseTime := time.Now()
				return []*event.Event{
					createTestEvent("event1", "agent1", "Test message 1", baseTime.Add(-2*time.Hour), false),
					createTestEvent("event2", "agent2", "Test message 2", baseTime.Add(-1*time.Hour), true),
				}
			},
			validate: func(t *testing.T, initialTime time.Time, finalSess *session.Session, events []*event.Event) {
				assert.Equal(t, 2, len(finalSess.Events))
				// Redis returns events in chronological order (oldest first)
				// event1 (older timestamp) should come before event2
				assert.Equal(t, "event1", finalSess.Events[0].ID)
				assert.Equal(t, "event2", finalSess.Events[1].ID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClientURL(redisURL),
				WithEnableAsyncPersist(tt.enableAsyncPersistence))
			require.NoError(t, err)
			defer service.Close()

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
			event:         createTestEvent("event123", "agent", "Test error case message", time.Now(), false),
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
			event:         createTestEvent("event123", "agent", "Test error case message", time.Now(), false),
			expectedError: "redis: nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClientURL(redisURL))
			require.NoError(t, err)
			defer service.Close()

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
			createTestEvent("event1", "test-author", "Test message from event1", baseTime.Add(-4*time.Hour), false),
			createTestEvent("event2", "test-author", "Test message from event2", baseTime.Add(-3*time.Hour), false),
			createTestEvent("event3", "test-author", "Test message from event3", baseTime.Add(-2*time.Hour), false),
			createTestEvent("event4", "test-author", "Test message from event4", baseTime.Add(-1*time.Hour), false),
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
				// With limit, get latest 2 events but return in chronological order (oldest first)
				assert.Equal(t, "event3", sess.Events[0].ID)
				assert.Equal(t, "event4", sess.Events[1].ID)
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
				// Redis returns events after the specified time in chronological order (oldest first)
				assert.Equal(t, "event3", sess.Events[0].ID)
				assert.Equal(t, "event4", sess.Events[1].ID)
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
				// With limit=1, returns the newest event after the time filter
				assert.Equal(t, "event4", sess.Events[0].ID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClientURL(redisURL))
			require.NoError(t, err)
			defer service.Close()

			// Setup initial data for all test cases.
			baseTime := setup(t, service)

			sess, err := tt.setup(service, baseTime)
			tt.validate(t, sess, err, baseTime)
		})
	}
}

func TestService_AppendEvent_EventOrder(t *testing.T) {
	tests := []struct {
		name          string
		setupEvents   func() []*event.Event
		expectedOrder []string
		description   string
	}{
		{
			name: "single_event_order",
			setupEvents: func() []*event.Event {
				return []*event.Event{
					createTestEvent("event1", "agent1", "Test message 1", time.Now(), false),
				}
			},
			expectedOrder: []string{"event1"},
			description:   "single event should be returned correctly",
		},
		{
			name: "multiple_events_chronological_order",
			setupEvents: func() []*event.Event {
				baseTime := time.Now()
				return []*event.Event{
					createTestEvent("event1", "agent1", "Test message 1", baseTime.Add(-3*time.Hour), false),
					createTestEvent("event2", "agent2", "Test message 2", baseTime.Add(-2*time.Hour), false),
					createTestEvent("event3", "agent3", "Test message 3", baseTime.Add(-1*time.Hour), true),
				}
			},
			expectedOrder: []string{"event1", "event2", "event3"},
			description:   "multiple events should be returned in chronological order (oldest first)",
		},
		{
			name: "events_added_out_of_order",
			setupEvents: func() []*event.Event {
				baseTime := time.Now()
				// Add events in non-chronological order
				return []*event.Event{
					createTestEvent("event_newest", "agent_newest", "Test newest message", baseTime.Add(-1*time.Hour), false),
					createTestEvent("event_oldest", "agent_oldest", "Test oldest message", baseTime.Add(-5*time.Hour), false),
					createTestEvent("event_middle", "agent_middle", "Test middle message", baseTime.Add(-3*time.Hour), true),
				}
			},
			expectedOrder: []string{"event_oldest", "event_middle", "event_newest"},
			description:   "events should be returned in chronological order even when added out of order",
		},
		{
			name: "events_with_same_timestamp",
			setupEvents: func() []*event.Event {
				sameTime := time.Now().Add(-2 * time.Hour)
				return []*event.Event{
					createTestEvent("event_a", "agent_a", "Test message A", sameTime, false),
					createTestEvent("event_b", "agent_b", "Test message B", sameTime, true),
				}
			},
			expectedOrder: []string{"event_a", "event_b"},
			description:   "events with same timestamp should be returned in insertion order",
		},
		{
			name: "large_number_of_events",
			setupEvents: func() []*event.Event {
				baseTime := time.Now()
				events := make([]*event.Event, 10)
				for i := 0; i < 10; i++ {
					events[i] = createTestEvent(
						fmt.Sprintf("event_%02d", i),
						fmt.Sprintf("agent_%d", i),
						fmt.Sprintf("Test message %d", i),
						baseTime.Add(time.Duration(-10+i)*time.Hour),
						i%2 == 0,
					)
				}
				return events
			},
			expectedOrder: []string{"event_00", "event_01", "event_02", "event_03", "event_04", "event_05", "event_06", "event_07", "event_08", "event_09"},
			description:   "large number of events should be returned in correct chronological order",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClientURL(redisURL))
			require.NoError(t, err)
			defer service.Close()

			// Create session
			sessionKey := session.Key{
				AppName:   "testapp",
				UserID:    "user123",
				SessionID: "session123",
			}

			initialState := session.StateMap{
				"test_key": []byte("test_value"),
			}

			sess, err := service.CreateSession(context.Background(), sessionKey, initialState)
			require.NoError(t, err)
			require.NotNil(t, sess)

			// Add events
			events := tt.setupEvents()
			for _, evt := range events {
				err = service.AppendEvent(context.Background(), sess, evt, session.WithEventTime(evt.Timestamp))
				require.NoError(t, err, "Failed to append event %s: %v", evt.ID, err)
			}

			// Get session and verify event order
			finalSess, err := service.GetSession(context.Background(), sessionKey)
			require.NoError(t, err)
			require.NotNil(t, finalSess)

			// Verify event count
			assert.Equal(t, len(tt.expectedOrder), len(finalSess.Events),
				"Expected %d events, got %d. Description: %s",
				len(tt.expectedOrder), len(finalSess.Events), tt.description)

			// Verify event order
			actualOrder := make([]string, len(finalSess.Events))
			for i, evt := range finalSess.Events {
				actualOrder[i] = evt.ID
			}

			assert.Equal(t, tt.expectedOrder, actualOrder,
				"Event order mismatch. Description: %s\nExpected: %v\nActual: %v",
				tt.description, tt.expectedOrder, actualOrder)

			// Verify event timestamp order
			for i := 1; i < len(finalSess.Events); i++ {
				assert.True(t,
					finalSess.Events[i-1].Timestamp.Before(finalSess.Events[i].Timestamp) ||
						finalSess.Events[i-1].Timestamp.Equal(finalSess.Events[i].Timestamp),
					"Events should be in chronological order. Event %s (index %d) should come before or equal to event %s (index %d)",
					finalSess.Events[i-1].ID, i-1, finalSess.Events[i].ID, i)
			}
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
				testEvent := createTestEvent("event123", "agent", "Test atomicity event", time.Now(), false)
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
				testEvent := createTestEvent("event123", "agent", "Test atomicity event", time.Now(), false)
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
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(WithRedisClientURL(redisURL))
			require.NoError(t, err)
			defer service.Close()

			sessionKey := session.Key{
				AppName: "testapp", UserID: "user123", SessionID: "session123",
			}
			// Create session is common for both tests.
			_, err = service.CreateSession(context.Background(), sessionKey, session.StateMap{})
			require.NoError(t, err)

			err = tt.setup(t, service, sessionKey)
			client := buildRedisClient(t, redisURL)

			tt.validate(t, client, service, sessionKey, err)
		})
	}
}

// TestService_SessionTTL tests session TTL functionality
func TestService_SessionTTL(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, service *Service) session.Key
		validate func(t *testing.T, client *redis.Client, sessionKey session.Key)
	}{
		{
			name: "session_ttl_set_correctly",
			setup: func(t *testing.T, service *Service) session.Key {
				sessionKey := session.Key{
					AppName:   "testapp",
					UserID:    "user123",
					SessionID: "session123",
				}
				sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{"key": []byte("value")})
				require.NoError(t, err)

				// Add an event to create the event list
				testEvent := event.New("test-invocation", "test-author")
				testEvent.Response = &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "Test message for TTL test",
							},
						},
					},
				}
				err = service.AppendEvent(context.Background(), sess, testEvent)
				require.NoError(t, err)

				return sessionKey
			},
			validate: func(t *testing.T, client *redis.Client, sessionKey session.Key) {
				// Check session state TTL
				sessionStateKey := getSessionStateKey(sessionKey)
				ttl := client.TTL(context.Background(), sessionStateKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 0 && ttl.Val() <= 5*time.Second, "Session state TTL should be set and close to 5 seconds, got: %v", ttl.Val())

				// Check event list TTL
				eventKey := getEventKey(sessionKey)
				ttl = client.TTL(context.Background(), eventKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 0 && ttl.Val() <= 5*time.Second, "Event list TTL should be set and close to 5 seconds, got: %v", ttl.Val())
			},
		},
		{
			name: "session_ttl_refreshed_on_get",
			setup: func(t *testing.T, service *Service) session.Key {
				sessionKey := session.Key{
					AppName:   "testapp",
					UserID:    "user123",
					SessionID: "session456",
				}
				sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{"key": []byte("value")})
				require.NoError(t, err)

				// Add an event to create the event list
				testEvent := event.New("test-invocation", "test-author")
				testEvent.Response = &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "Test message for TTL test",
							},
						},
					},
				}
				err = service.AppendEvent(context.Background(), sess, testEvent)
				require.NoError(t, err)

				// Wait a bit to let TTL decrease
				time.Sleep(2 * time.Second)

				// Get session to refresh TTL
				_, err = service.GetSession(context.Background(), sessionKey)
				require.NoError(t, err)

				return sessionKey
			},
			validate: func(t *testing.T, client *redis.Client, sessionKey session.Key) {
				// Check that TTL was refreshed (should be close to 5 seconds again)
				sessionStateKey := getSessionStateKey(sessionKey)
				ttl := client.TTL(context.Background(), sessionStateKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 3*time.Second, "Session state TTL should be refreshed, got: %v", ttl.Val())

				eventKey := getEventKey(sessionKey)
				ttl = client.TTL(context.Background(), eventKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 3*time.Second, "Event list TTL should be refreshed, got: %v", ttl.Val())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			// Create service with 5 second session TTL
			service, err := NewService(
				WithRedisClientURL(redisURL),
				WithSessionTTL(5*time.Second),
			)
			require.NoError(t, err)
			defer service.Close()

			client := buildRedisClient(t, redisURL)
			sessionKey := tt.setup(t, service)
			tt.validate(t, client, sessionKey)
		})
	}
}

// TestService_AppStateTTL tests app state TTL functionality
func TestService_AppStateTTL(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, service *Service) string
		validate func(t *testing.T, client *redis.Client, appName string)
	}{
		{
			name: "app_state_ttl_set_correctly",
			setup: func(t *testing.T, service *Service) string {
				appName := "testapp"
				err := service.UpdateAppState(context.Background(), appName, session.StateMap{"key": []byte("value")})
				require.NoError(t, err)
				return appName
			},
			validate: func(t *testing.T, client *redis.Client, appName string) {
				appStateKey := getAppStateKey(appName)
				ttl := client.TTL(context.Background(), appStateKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 0 && ttl.Val() <= 5*time.Second, "App state TTL should be set and close to 5 seconds, got: %v", ttl.Val())
			},
		},
		{
			name: "app_state_ttl_refreshed_on_get",
			setup: func(t *testing.T, service *Service) string {
				appName := "testapp2"
				err := service.UpdateAppState(context.Background(), appName, session.StateMap{"key": []byte("value")})
				require.NoError(t, err)

				// Wait a bit to let TTL decrease
				time.Sleep(2 * time.Second)

				// Create a session to trigger app state TTL refresh
				sessionKey := session.Key{
					AppName:   appName,
					UserID:    "user123",
					SessionID: "session123",
				}
				_, err = service.CreateSession(context.Background(), sessionKey, session.StateMap{})
				require.NoError(t, err)

				// Get session to refresh app state TTL
				_, err = service.GetSession(context.Background(), sessionKey)
				require.NoError(t, err)

				return appName
			},
			validate: func(t *testing.T, client *redis.Client, appName string) {
				// Check that TTL was refreshed (should be close to 5 seconds again)
				appStateKey := getAppStateKey(appName)
				ttl := client.TTL(context.Background(), appStateKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 3*time.Second, "App state TTL should be refreshed, got: %v", ttl.Val())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			// Create service with 5 second app state TTL
			service, err := NewService(
				WithRedisClientURL(redisURL),
				WithAppStateTTL(5*time.Second),
			)
			require.NoError(t, err)
			defer service.Close()

			client := buildRedisClient(t, redisURL)
			appName := tt.setup(t, service)
			tt.validate(t, client, appName)
		})
	}
}

// TestService_UserStateTTL tests user state TTL functionality
func TestService_UserStateTTL(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, service *Service) session.UserKey
		validate func(t *testing.T, client *redis.Client, userKey session.UserKey)
	}{
		{
			name: "user_state_ttl_set_correctly",
			setup: func(t *testing.T, service *Service) session.UserKey {
				userKey := session.UserKey{
					AppName: "testapp",
					UserID:  "user123",
				}
				err := service.UpdateUserState(context.Background(), userKey, session.StateMap{"key": []byte("value")})
				require.NoError(t, err)
				return userKey
			},
			validate: func(t *testing.T, client *redis.Client, userKey session.UserKey) {
				userStateKey := getUserStateKey(session.Key{AppName: userKey.AppName, UserID: userKey.UserID})
				ttl := client.TTL(context.Background(), userStateKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 0 && ttl.Val() <= 5*time.Second, "User state TTL should be set and close to 5 seconds, got: %v", ttl.Val())
			},
		},
		{
			name: "user_state_ttl_refreshed_on_get",
			setup: func(t *testing.T, service *Service) session.UserKey {
				userKey := session.UserKey{
					AppName: "testapp2",
					UserID:  "user456",
				}
				err := service.UpdateUserState(context.Background(), userKey, session.StateMap{"key": []byte("value")})
				require.NoError(t, err)

				// Wait a bit to let TTL decrease
				time.Sleep(2 * time.Second)

				// Create a session to trigger user state TTL refresh
				sessionKey := session.Key{
					AppName:   userKey.AppName,
					UserID:    userKey.UserID,
					SessionID: "session456",
				}
				_, err = service.CreateSession(context.Background(), sessionKey, session.StateMap{})
				require.NoError(t, err)

				// Get session to refresh user state TTL
				_, err = service.GetSession(context.Background(), sessionKey)
				require.NoError(t, err)

				return userKey
			},
			validate: func(t *testing.T, client *redis.Client, userKey session.UserKey) {
				// Check that TTL was refreshed (should be close to 5 seconds again)
				userStateKey := getUserStateKey(session.Key{AppName: userKey.AppName, UserID: userKey.UserID})
				ttl := client.TTL(context.Background(), userStateKey)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 3*time.Second, "User state TTL should be refreshed, got: %v", ttl.Val())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			// Create service with 5 second user state TTL
			service, err := NewService(
				WithRedisClientURL(redisURL),
				WithUserStateTTL(5*time.Second),
			)
			require.NoError(t, err)
			defer service.Close()

			client := buildRedisClient(t, redisURL)
			userKey := tt.setup(t, service)
			tt.validate(t, client, userKey)
		})
	}
}

func TestService_AsyncPersisterNum_DefaultClamp(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(0),
	)
	require.NoError(t, err)
	defer service.Close()

	assert.Equal(t, defaultAsyncPersisterNum, len(service.eventPairChans))
}

func TestService_AppendEvent_NoPanic_AfterClose(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(2),
	)
	require.NoError(t, err)

	// Create a session.
	sessionKey := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}
	sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{})
	require.NoError(t, err)

	// Close service to close channels.
	service.Close()

	// Append after close should not panic due to recover in AppendEvent.
	evt := createTestEvent("event-after-close", "agent", "msg", time.Now(), false)
	assert.NotPanics(t, func() {
		_ = service.AppendEvent(context.Background(), sess, evt)
	})
}

func TestService_SessionEventLimit_TrimsOldest(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	limit := 3
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithSessionEventLimit(limit),
	)
	require.NoError(t, err)
	defer service.Close()

	// Create session.
	sessionKey := session.Key{AppName: "testapp", UserID: "user123", SessionID: "session123"}
	sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{})
	require.NoError(t, err)

	// Append 5 events with increasing timestamps.
	base := time.Now().Add(-5 * time.Minute)
	ids := []string{"e1", "e2", "e3", "e4", "e5"}
	for i, id := range ids {
		evt := createTestEvent(id, "agent", "content", base.Add(time.Duration(i)*time.Second), false)
		err := service.AppendEvent(context.Background(), sess, evt, session.WithEventTime(evt.Timestamp))
		require.NoError(t, err)
	}

	// Get session and verify only latest 'limit' events remain in chronological order.
	got, err := service.GetSession(context.Background(), sessionKey, session.WithEventNum(0))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Events, limit)
	// Should keep e3, e4, e5 in chronological order.
	assert.Equal(t, []string{"e3", "e4", "e5"}, []string{got.Events[0].ID, got.Events[1].ID, got.Events[2].ID})
}

func TestEnsureEventStartWithUser(t *testing.T) {
	tests := []struct {
		name           string
		setupEvents    func() []event.Event
		expectedLength int
		expectFirst    bool // true if first event should be from user
	}{
		{
			name: "empty_events",
			setupEvents: func() []event.Event {
				return []event.Event{}
			},
			expectedLength: 0,
			expectFirst:    false,
		},
		{
			name: "events_already_start_with_user",
			setupEvents: func() []event.Event {
				evt1 := event.New("test1", "user")
				evt1.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "User message 1"}}},
				}
				evt2 := event.New("test2", "assistant")
				evt2.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Assistant message"}}},
				}
				return []event.Event{*evt1, *evt2}
			},
			expectedLength: 2,
			expectFirst:    true,
		},
		{
			name: "events_start_with_assistant_then_user",
			setupEvents: func() []event.Event {
				evt1 := event.New("test1", "assistant")
				evt1.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Assistant message 1"}}},
				}
				evt2 := event.New("test2", "assistant")
				evt2.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Assistant message 2"}}},
				}
				evt3 := event.New("test3", "user")
				evt3.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "User message"}}},
				}
				evt4 := event.New("test4", "assistant")
				evt4.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Assistant message 3"}}},
				}
				return []event.Event{*evt1, *evt2, *evt3, *evt4}
			},
			expectedLength: 2, // Should keep events from index 2 onwards
			expectFirst:    true,
		},
		{
			name: "all_events_from_assistant",
			setupEvents: func() []event.Event {
				evt1 := event.New("test1", "assistant")
				evt1.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Assistant message 1"}}},
				}
				evt2 := event.New("test2", "assistant")
				evt2.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Assistant message 2"}}},
				}
				return []event.Event{*evt1, *evt2}
			},
			expectedLength: 0, // Should clear all events
			expectFirst:    false,
		},
		{
			name: "events_with_no_response",
			setupEvents: func() []event.Event {
				evt1 := event.New("test1", "unknown")
				// No response set
				evt2 := event.New("test2", "user")
				evt2.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "User message"}}},
				}
				return []event.Event{*evt1, *evt2}
			},
			expectedLength: 1, // Should keep from first user event
			expectFirst:    true,
		},
		{
			name: "events_with_empty_choices",
			setupEvents: func() []event.Event {
				evt1 := event.New("test1", "unknown")
				evt1.Response = &model.Response{
					Choices: []model.Choice{}, // Empty choices
				}
				evt2 := event.New("test2", "user")
				evt2.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "User message"}}},
				}
				return []event.Event{*evt1, *evt2}
			},
			expectedLength: 1, // Should keep from first user event
			expectFirst:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := &session.Session{
				ID:      "test-session",
				AppName: "test-app",
				UserID:  "test-user",
				Events:  tt.setupEvents(),
			}

			isession.EnsureEventStartWithUser(sess)

			assert.Equal(t, tt.expectedLength, len(sess.Events), "Event length should match expected")

			if tt.expectFirst && len(sess.Events) > 0 {
				assert.NotNil(t, sess.Events[0].Response, "First event should have response")
				assert.Greater(t, len(sess.Events[0].Response.Choices), 0, "First event should have choices")
				assert.Equal(t, model.RoleUser, sess.Events[0].Response.Choices[0].Message.Role, "First event should be from user")
			}
		})
	}
}

func TestGetSession_EventFiltering_Integration(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	// Create a session
	sessionKey := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{})
	require.NoError(t, err)

	// Add events starting with assistant messages
	baseTime := time.Now()
	events := []*event.Event{
		{
			ID:        "event1",
			Timestamp: baseTime.Add(-5 * time.Hour),
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "Assistant message 1",
						},
					},
				},
			},
		},
		{
			ID:        "event2",
			Timestamp: baseTime.Add(-4 * time.Hour),
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "Assistant message 2",
						},
					},
				},
			},
		},
		{
			ID:        "event3",
			Timestamp: baseTime.Add(-3 * time.Hour),
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleUser,
							Content: "User message 1",
						},
					},
				},
			},
		},
		{
			ID:        "event4",
			Timestamp: baseTime.Add(-2 * time.Hour),
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "Assistant message 3",
						},
					},
				},
			},
		},
	}

	// Add events to session
	for _, evt := range events {
		err := service.AppendEvent(context.Background(), sess, evt)
		require.NoError(t, err)
	}

	// Test GetSession - should only return events starting from first user message
	retrievedSess, err := service.GetSession(context.Background(), sessionKey)
	require.NoError(t, err)
	require.NotNil(t, retrievedSess)

	// Should have 2 events (from event3 onwards)
	assert.Equal(t, 2, len(retrievedSess.Events), "Should filter out assistant events before first user event")
	assert.Equal(t, "event3", retrievedSess.Events[0].ID, "First event should be the user event")
	assert.Equal(t, model.RoleUser, retrievedSess.Events[0].Response.Choices[0].Message.Role)
	assert.Equal(t, "event4", retrievedSess.Events[1].ID, "Second event should be the subsequent assistant event")

	// Test ListSessions - should apply same filtering
	sessionList, err := service.ListSessions(context.Background(), session.UserKey{
		AppName: "testapp",
		UserID:  "user123",
	})
	require.NoError(t, err)
	require.Len(t, sessionList, 1)

	// Should have same filtering as GetSession
	assert.Equal(t, 2, len(sessionList[0].Events), "ListSessions should also filter events")
	assert.Equal(t, "event3", sessionList[0].Events[0].ID, "First event should be the user event")
	assert.Equal(t, model.RoleUser, sessionList[0].Events[0].Response.Choices[0].Message.Role)
}

func TestGetSession_AllAssistantEvents_Integration(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	// Create a session
	sessionKey := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{})
	require.NoError(t, err)

	// Add only assistant events
	baseTime := time.Now()
	events := []*event.Event{
		{
			ID:        "event1",
			Timestamp: baseTime.Add(-3 * time.Hour),
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "Assistant message 1",
						},
					},
				},
			},
		},
		{
			ID:        "event2",
			Timestamp: baseTime.Add(-2 * time.Hour),
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "Assistant message 2",
						},
					},
				},
			},
		},
	}

	// Add events to session
	for _, evt := range events {
		err := service.AppendEvent(context.Background(), sess, evt)
		require.NoError(t, err)
	}

	// Test GetSession - should return empty events
	retrievedSess, err := service.GetSession(context.Background(), sessionKey)
	require.NoError(t, err)
	require.NotNil(t, retrievedSess)

	// Should have no events since all are from assistant
	assert.Equal(t, 0, len(retrievedSess.Events), "Should filter out all assistant events when no user events exist")
}
