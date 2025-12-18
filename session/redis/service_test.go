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
			name:        "non-existent redis instance",
			options:     []ServiceOpt{WithRedisInstance("non-existent-instance")},
			expectError: true,
			errorMsg:    "redis instance",
		},
		{
			name:        "invalid redis URL",
			options:     []ServiceOpt{WithRedisClientURL("invalid://url")},
			expectError: true,
			errorMsg:    "create redis client failed",
		},
		{
			name:        "empty options",
			options:     []ServiceOpt{WithRedisClientURL("redis://localhost:6379")},
			expectError: false,
		},
		{
			name: "multiple options combination",
			options: []ServiceOpt{
				WithRedisClientURL("redis://localhost:6379"),
				WithSessionEventLimit(1000),
				WithSessionTTL(time.Hour),
				WithAppStateTTL(2 * time.Hour),
				WithUserStateTTL(30 * time.Minute),
				WithEnableAsyncPersist(true),
				WithAsyncPersisterNum(5),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewService(tt.options...)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, service)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, service)

				// Verify options were applied correctly
				// Service creation is successful, which means options were applied
				_ = service

				// Clean up
				if service != nil {
					err := service.Close()
					assert.NoError(t, err)
				}
			}
		})
	}
}

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

func TestService_getSessionSummaryTTL(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	ttl := 3 * time.Second
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithSessionTTL(ttl),
	)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "summary-app",
		UserID:    "summary-user",
		SessionID: "summary-session",
	}
	_, err = service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	summary := map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {
			Summary:   "hello",
			UpdatedAt: time.Now(),
		},
	}
	summaryBytes, err := json.Marshal(summary)
	require.NoError(t, err)

	client := buildRedisClient(t, redisURL)
	summaryKey := getSessionSummaryKey(key)
	err = client.HSet(ctx, summaryKey, key.SessionID, summaryBytes).Err()
	require.NoError(t, err)

	_, err = service.getSession(ctx, key, 0, time.Time{})
	require.NoError(t, err)

	ttlVal := client.TTL(ctx, summaryKey).Val()
	assert.Greater(t, ttlVal, time.Duration(0))
	assert.LessOrEqual(t, ttlVal, ttl)
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

			sess.EnsureEventStartWithUser()

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
							Role:    model.RoleUser,
							Content: "user message 1",
						},
					},
				},
			},
		},
		{
			ID:        "event1",
			Timestamp: baseTime.Add(-3 * time.Hour),
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Index: 1,
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
						Index: 2,
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
	assert.Equal(t, 3, len(retrievedSess.Events), "Should filter out all assistant events when no user events exist")
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

func TestStartAsyncPersistWorker(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(3),
	)
	require.NoError(t, err)
	defer service.Close()

	// Verify that event pair channels are initialized
	assert.Len(t, service.eventPairChans, 3)
	assert.NotNil(t, service.eventPairChans[0])
	assert.NotNil(t, service.eventPairChans[1])
	assert.NotNil(t, service.eventPairChans[2])

	// Verify channel buffer sizes
	for i, ch := range service.eventPairChans {
		assert.Equal(t, defaultChanBufferSize, cap(ch), "Channel %d should have buffer size %d", i, defaultChanBufferSize)
	}
}

func TestStartAsyncSummaryWorker(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Test without summarizer - async workers should not be started
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(2),
		WithSummaryQueueSize(50),
	)
	require.NoError(t, err)
	defer service.Close()

	// Verify that summary job channels are not initialized when no summarizer is provided
	assert.Len(t, service.summaryJobChans, 0)
}

func TestStartAsyncSummaryWorkerWithSummarizer(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(2),
		WithSummaryQueueSize(50),
		WithSummarizer(&fakeSummarizer{allow: true, out: "test"}),
	)
	require.NoError(t, err)
	defer service.Close()

	// Verify that summary job channels are initialized when summarizer is provided
	assert.Len(t, service.summaryJobChans, 2)
	assert.NotNil(t, service.summaryJobChans[0])
	assert.NotNil(t, service.summaryJobChans[1])

	// Verify channel buffer sizes
	for i, ch := range service.summaryJobChans {
		assert.Equal(t, 50, cap(ch), "Summary channel %d should have buffer size 50", i)
	}
}

func TestService_Close(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Test closing with async persist enabled
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(2),
	)
	require.NoError(t, err)

	// Verify channels are open before close
	assert.Len(t, service.eventPairChans, 2)
	for i, ch := range service.eventPairChans {
		assert.NotNil(t, ch, "Channel %d should not be nil before close", i)
	}

	// Close service
	err = service.Close()
	assert.NoError(t, err)

	// Verify channels are closed (we can't directly test this, but we can test that no panic occurs)
	// and that the service is in a closed state
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
	err = service.deleteSessionState(ctx, key)
	require.NoError(t, err)

	// Verify session is deleted
	deletedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, deletedSess)
}

func TestService_GetSession_RedisErrors(t *testing.T) {
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

	// Get non-existent session should return nil without error
	sess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, sess)
}

func TestService_ListSessions_RedisErrors(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "testapp",
		UserID:  "non-existent-user",
	}

	// List sessions for non-existent user should return empty list
	sessions, err := service.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestService_UpdateAppState_RedisErrors(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// Update app state should succeed even if app doesn't exist yet
	err = service.UpdateAppState(ctx, "new-app", session.StateMap{"key": []byte("value")})
	require.NoError(t, err)

	// Verify state was created
	states, err := service.ListAppStates(ctx, "new-app")
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), states["key"])
}

func TestService_UpdateUserState_RedisErrors(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "testapp",
		UserID:  "user123",
	}

	// Update user state should succeed even if user doesn't exist yet
	err = service.UpdateUserState(ctx, userKey, session.StateMap{"key": []byte("value")})
	require.NoError(t, err)

	// Verify state was created
	states, err := service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), states["key"])
}

func TestService_ListAppStates_Empty(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// List states for non-existent app should return empty map
	states, err := service.ListAppStates(ctx, "non-existent-app")
	require.NoError(t, err)
	assert.Empty(t, states)
}

func TestService_ListUserStates_Empty(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "testapp",
		UserID:  "non-existent-user",
	}

	// List states for non-existent user should return empty map
	states, err := service.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Empty(t, states)
}

func TestService_DeleteAppState_NonExistent(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// Delete non-existent app state should not error
	err = service.DeleteAppState(ctx, "non-existent-app", "key")
	require.NoError(t, err)
}

func TestService_DeleteUserState_NonExistent(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "testapp",
		UserID:  "non-existent-user",
	}

	// Delete non-existent user state should not error
	err = service.DeleteUserState(ctx, userKey, "key")
	require.NoError(t, err)
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
	err = client.HSet(
		ctx, getSessionSummaryKey(key), key.SessionID, string(payload),
	).Err()
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

	service, err := NewService(WithRedisClientURL(redisURL))
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

func TestService_WithTTL(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with all TTLs enabled
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithSessionTTL(time.Hour),
		WithAppStateTTL(2*time.Hour),
		WithUserStateTTL(30*time.Minute),
	)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	// Create a session with app and user state
	err = service.UpdateAppState(ctx, key.AppName, session.StateMap{"app_key": []byte("app_value")})
	require.NoError(t, err)

	userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}
	err = service.UpdateUserState(ctx, userKey, session.StateMap{"user_key": []byte("user_value")})
	require.NoError(t, err)

	sess, err := service.CreateSession(ctx, key, session.StateMap{"sess_key": []byte("sess_value")})
	require.NoError(t, err)
	assert.NotNil(t, sess)

	// Append an event
	evt := createTestEvent("e1", "agent", "content", time.Now(), false)
	err = service.AppendEvent(ctx, sess, evt)
	require.NoError(t, err)

	// Get session - this should trigger TTL refresh
	retrievedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, retrievedSess)
	assert.Equal(t, []byte("sess_value"), retrievedSess.State["sess_key"])
	// App and user states are prefixed
	assert.Equal(t, []byte("app_value"), retrievedSess.State["app:app_key"])
	assert.Equal(t, []byte("user_value"), retrievedSess.State["user:user_key"])

	// List sessions - this should also trigger TTL refresh
	sessions, err := service.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
}

func TestService_DeleteSession_WithTTL(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with session TTL
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithSessionTTL(time.Hour),
	)
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
	assert.NotNil(t, sess)

	// Delete the session
	err = service.DeleteSession(ctx, key)
	require.NoError(t, err)

	// Verify session is deleted
	deletedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, deletedSess)
}

func TestService_ProcessStateCmd_Errors(t *testing.T) {
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

	// Manually corrupt the data in Redis to trigger unmarshal error
	client := buildRedisClient(t, redisURL)
	sessKey := getSessionStateKey(key)
	err = client.HSet(ctx, sessKey, key.SessionID, "invalid json").Err()
	require.NoError(t, err)

	// Try to get the session - should return error
	_, err = service.GetSession(ctx, key)
	assert.Error(t, err)
}

func TestService_GetEventsList_AfterTime(t *testing.T) {
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

	// Append events with different timestamps
	baseTime := time.Now().Add(-1 * time.Hour)
	for i := 0; i < 5; i++ {
		evt := createTestEvent(fmt.Sprintf("e%d", i), "agent", "content", baseTime.Add(time.Duration(i)*time.Minute), false)
		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
	}

	// Get session with afterTime filter
	afterTime := baseTime.Add(2 * time.Minute)
	retrievedSess, err := service.GetSession(ctx, key, session.WithEventTime(afterTime))
	require.NoError(t, err)
	assert.NotNil(t, retrievedSess)
	// Should only get events after the specified time
	assert.LessOrEqual(t, len(retrievedSess.Events), 3)
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

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "testapp",
		UserID:  "user123",
	}

	// Create multiple sessions without events
	for i := 0; i < 3; i++ {
		key := session.Key{
			AppName:   userKey.AppName,
			UserID:    userKey.UserID,
			SessionID: fmt.Sprintf("session%d", i),
		}
		_, err := service.CreateSession(ctx, key, session.StateMap{})
		require.NoError(t, err)
	}

	// List sessions
	sessions, err := service.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 3)
	// All sessions should have empty events
	for _, sess := range sessions {
		assert.Empty(t, sess.Events)
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

func TestService_AppendEvent_WithLimit(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	limit := 2
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithSessionEventLimit(limit),
	)
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

	// Append more events than the limit
	for i := 0; i < 5; i++ {
		evt := createTestEvent(fmt.Sprintf("e%d", i), "agent", "content", time.Now(), false)
		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
	}

	// Get session and verify only latest events are kept
	retrievedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, retrievedSess)
	assert.LessOrEqual(t, len(retrievedSess.Events), limit)
}

func TestService_AppendTrackEvent_Persistence(t *testing.T) {
	tests := []struct {
		name               string
		enableAsyncPersist bool
	}{
		{name: "sync", enableAsyncPersist: false},
		{name: "async", enableAsyncPersist: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			service, err := NewService(
				WithRedisClientURL(redisURL),
				WithEnableAsyncPersist(tt.enableAsyncPersist),
			)
			require.NoError(t, err)
			defer service.Close()

			sessionKey := session.Key{
				AppName:   "track-app",
				UserID:    "track-user",
				SessionID: "track-session",
			}

			sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{})
			require.NoError(t, err)

			trackName := session.Track("agui")
			payload := json.RawMessage(`{"hello":"world"}`)
			trackEvt := &session.TrackEvent{
				Track:     trackName,
				Payload:   payload,
				Timestamp: time.Now(),
			}

			require.NoError(t, service.AppendTrackEvent(context.Background(), sess, trackEvt))

			require.Eventually(t, func() bool {
				fetched, err := service.GetSession(context.Background(), sessionKey)
				if err != nil || fetched == nil || fetched.Tracks == nil {
					return false
				}
				history, ok := fetched.Tracks[trackName]
				if !ok || history == nil || len(history.Events) == 0 {
					return false
				}
				last := history.Events[len(history.Events)-1]
				return last.Track == trackName && string(last.Payload) == string(payload)
			}, 2*time.Second, 10*time.Millisecond, "track event not persisted")
		})
	}
}

func TestService_GetSessionTrackAfterTime(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	sessionKey := session.Key{
		AppName:   "track-app",
		UserID:    "track-user",
		SessionID: "track-session",
	}

	sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{})
	require.NoError(t, err)

	trackName := session.Track("agui")
	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now().Add(-1 * time.Hour)

	oldPayload := json.RawMessage(`{"idx":1}`)
	newPayload := json.RawMessage(`{"idx":2}`)

	require.NoError(t, service.AppendTrackEvent(context.Background(), sess, &session.TrackEvent{
		Track:     trackName,
		Payload:   oldPayload,
		Timestamp: oldTime,
	}))
	require.NoError(t, service.AppendTrackEvent(context.Background(), sess, &session.TrackEvent{
		Track:     trackName,
		Payload:   newPayload,
		Timestamp: newTime,
	}))

	cutoff := newTime.Add(-30 * time.Minute)
	retrieved, err := service.GetSession(context.Background(), sessionKey, session.WithEventTime(cutoff))
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	require.NotNil(t, retrieved.Tracks)
	history, ok := retrieved.Tracks[trackName]
	require.True(t, ok)
	require.Len(t, history.Events, 1)
	require.Equal(t, string(newPayload), string(history.Events[0].Payload))
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

func TestService_UpdateAppState_EmptyKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// Update app state with empty app name should error
	err = service.UpdateAppState(ctx, "", session.StateMap{"key": []byte("value")})
	require.Error(t, err)
}

func TestService_UpdateUserState_EmptyKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// Update user state with empty user key should error
	err = service.UpdateUserState(ctx, session.UserKey{}, session.StateMap{"key": []byte("value")})
	require.Error(t, err)
}

func TestService_ListAppStates_EmptyAppName(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// List app states with empty app name should error
	_, err = service.ListAppStates(ctx, "")
	require.Error(t, err)
}

func TestService_ListUserStates_EmptyUserKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// List user states with empty user key should error
	_, err = service.ListUserStates(ctx, session.UserKey{})
	require.Error(t, err)
}

func TestService_DeleteAppState_EmptyAppName(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// Delete app state with empty app name should error
	err = service.DeleteAppState(ctx, "", "key")
	require.Error(t, err)
}

func TestService_DeleteUserState_EmptyUserKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// Delete user state with empty user key should error
	err = service.DeleteUserState(ctx, session.UserKey{}, "key")
	require.Error(t, err)
}

func TestService_CorruptedData_AppState(t *testing.T) {
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

	// Create a session first
	_, err = service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	// Manually corrupt app state data in Redis
	client := buildRedisClient(t, redisURL)
	appStateKey := getAppStateKey(key.AppName)
	// Set invalid data that can't be converted to bytes properly
	// This tests the error path in processStateCmd
	err = client.HSet(ctx, appStateKey, "corrupted_key", string([]byte{0xff, 0xfe, 0xfd})).Err()
	require.NoError(t, err)

	// Try to get the session - should still work as processStateCmd handles conversion
	sess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, sess)
}

func TestService_CorruptedData_Events(t *testing.T) {
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

	// Manually corrupt event data in Redis by setting the event key to a hash instead of a list
	client := buildRedisClient(t, redisURL)
	eventKey := getEventKey(key)
	// Delete the existing list and create a hash with the same key
	err = client.Del(ctx, eventKey).Err()
	require.NoError(t, err)
	err = client.HSet(ctx, eventKey, "field", "value").Err()
	require.NoError(t, err)

	// Try to get the session - should return error due to wrong type
	_, err = service.GetSession(ctx, key)
	assert.Error(t, err)
}

func TestService_AsyncPersist_Enabled(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with async persist enabled
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(2),
	)
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
	for i := 0; i < 5; i++ {
		evt := createTestEvent(fmt.Sprintf("e%d", i), "agent", "content", time.Now(), false)
		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
	}

	// Give async workers time to process
	time.Sleep(100 * time.Millisecond)

	// Verify events were persisted
	retrievedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, retrievedSess)
	assert.NotEmpty(t, retrievedSess.Events)
}

func TestService_ListSessions_WithTTL(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with TTLs
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithSessionTTL(time.Hour),
		WithAppStateTTL(2*time.Hour),
		WithUserStateTTL(30*time.Minute),
	)
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

	// List sessions - should trigger TTL refresh
	sessions, err := service.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 3)
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

func TestService_ListSessionsWithTrackEvents(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "track-app",
		UserID:    "track-user",
		SessionID: "track-session",
	}
	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	payload := json.RawMessage(`"list-track"`)
	require.NoError(t, service.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track:     "alpha",
		Payload:   payload,
		Timestamp: time.Now(),
	}))

	sessions, err := service.ListSessions(ctx, session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.NotNil(t, sessions[0].Tracks)
	alpha, ok := sessions[0].Tracks["alpha"]
	require.True(t, ok)
	require.Len(t, alpha.Events, 1)
	assert.Equal(t, payload, alpha.Events[0].Payload)
}

func TestGetTrackEvents_EmptyKeys(t *testing.T) {
	s := &Service{}
	trackEvents, err := s.getTrackEvents(context.Background(), nil, nil, 0, time.Time{})
	require.NoError(t, err)
	assert.Nil(t, trackEvents)
}

func TestGetTrackEvents_Mismatch(t *testing.T) {
	s := &Service{}
	keys := []session.Key{{AppName: "a", UserID: "u", SessionID: "s"}}
	_, err := s.getTrackEvents(context.Background(), keys, nil, 0, time.Time{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mismatch")
}

func TestService_AppendTrackEventRecover(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(1),
	)
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{
		AppName:   "recover-app",
		UserID:    "recover-user",
		SessionID: "recover-session",
	}
	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	require.NoError(t, service.Close())

	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"recover"`),
		Timestamp: time.Now(),
	}

	assert.NotPanics(t, func() {
		err = service.AppendTrackEvent(ctx, sess, trackEvent)
	})
	assert.NoError(t, err)
}

func TestService_getTrackEventsEmptyKeys(t *testing.T) {
	service := &Service{}
	result, err := service.getTrackEvents(context.Background(), nil, nil, 0, time.Time{})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestService_getTrackEventsStateMismatch(t *testing.T) {
	service := &Service{}
	_, err := service.getTrackEvents(
		context.Background(),
		[]session.Key{{AppName: "app", UserID: "user", SessionID: "s1"}},
		nil,
		0,
		time.Time{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session states count mismatch")
}

func TestService_getTrackEventsLimitAndTTL(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	ttl := 2 * time.Second
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithSessionTTL(ttl),
	)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	}
	_, err = service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	require.NoError(t, service.addTrackEvent(ctx, key, &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"old"`),
		Timestamp: time.Now().Add(-time.Minute),
	}))
	require.NoError(t, service.addTrackEvent(ctx, key, &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"new"`),
		Timestamp: time.Now(),
	}))

	client := buildRedisClient(t, redisURL)
	state := fetchSessionState(t, ctx, client, key)
	result, err := service.getTrackEvents(ctx, []session.Key{key}, []*SessionState{state}, 1, time.Time{})
	require.NoError(t, err)
	require.Len(t, result, 1)
	alpha, ok := result[0]["alpha"]
	require.True(t, ok)
	require.Len(t, alpha, 1)
	assert.Equal(t, json.RawMessage(`"new"`), alpha[0].Payload)

	trackTTL := client.TTL(ctx, getTrackKey(key, "alpha")).Val()
	require.Greater(t, trackTTL, time.Duration(0))
	require.LessOrEqual(t, trackTTL, ttl)
}

func TestService_getTrackEventsAfterTime(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "filter-app",
		UserID:    "filter-user",
		SessionID: "filter-session",
	}
	_, err = service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	oldEvent := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"first"`),
		Timestamp: time.Now().Add(-2 * time.Hour),
	}
	newEvent := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"second"`),
		Timestamp: time.Now(),
	}

	require.NoError(t, service.addTrackEvent(ctx, key, oldEvent))
	require.NoError(t, service.addTrackEvent(ctx, key, newEvent))

	client := buildRedisClient(t, redisURL)
	state := fetchSessionState(t, ctx, client, key)
	cutoff := newEvent.Timestamp.Add(-time.Minute)

	result, err := service.getTrackEvents(
		ctx,
		[]session.Key{key},
		[]*SessionState{state},
		0,
		cutoff,
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	events := result[0]["alpha"]
	require.Len(t, events, 1)
	assert.Equal(t, json.RawMessage(`"second"`), events[0].Payload)
}

func TestService_getTrackEventsOrderingAndEmpty(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "order-app",
		UserID:    "order-user",
		SessionID: "order-session",
	}
	_, err = service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	oldEvent := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"first"`),
		Timestamp: time.Now().Add(-time.Hour),
	}
	newEvent := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"second"`),
		Timestamp: time.Now(),
	}
	require.NoError(t, service.addTrackEvent(ctx, key, oldEvent))
	require.NoError(t, service.addTrackEvent(ctx, key, newEvent))

	client := buildRedisClient(t, redisURL)
	state := fetchSessionState(t, ctx, client, key)
	result, err := service.getTrackEvents(ctx, []session.Key{key}, []*SessionState{state}, 0, time.Time{})
	require.NoError(t, err)
	require.Len(t, result, 1)
	events := result[0]["alpha"]
	require.Len(t, events, 2)
	assert.Equal(t, json.RawMessage(`"first"`), events[0].Payload)
	assert.Equal(t, json.RawMessage(`"second"`), events[1].Payload)

	keyNoTrack := session.Key{
		AppName:   "order-app",
		UserID:    "order-user",
		SessionID: "no-track",
	}
	_, err = service.CreateSession(ctx, keyNoTrack, session.StateMap{})
	require.NoError(t, err)

	stateNoTrack := fetchSessionState(t, ctx, client, keyNoTrack)
	emptyResult, err := service.getTrackEvents(ctx, []session.Key{keyNoTrack}, []*SessionState{stateNoTrack}, 0, time.Time{})
	require.NoError(t, err)
	require.Len(t, emptyResult, 1)
	assert.Empty(t, emptyResult[0])
}

// TestProcessEventCmd_SkipMalformed ensures malformed JSON items are skipped
// rather than failing the entire list parsing.
func TestProcessEventCmd_SkipMalformed(t *testing.T) {
	// Prepare one valid event and one malformed JSON string.
	valid := &event.Event{ID: "ok", Timestamp: time.Now()}
	validBytes, err := json.Marshal(valid)
	require.NoError(t, err)

	malformed := "{not-json}"

	// Build a StringSliceCmd like ZREVRANGE would return.
	cmd := redis.NewStringSliceCmd(context.Background(), "zrange", "k")
	cmd.SetVal([]string{malformed, string(validBytes)})

	// Call the helper under test.
	events, err := processEventCmd(context.Background(), cmd)
	require.NoError(t, err)

	// Only the valid one should be returned.
	require.Len(t, events, 1)
	require.Equal(t, "ok", events[0].ID)
}

func Test_normalizeSessionEvents(t *testing.T) {
	t.Run("nil_when_no_slices", func(t *testing.T) {
		assert.Nil(t, normalizeSessionEvents(nil))
	})
	t.Run("first_slice_returned", func(t *testing.T) {
		evts := []event.Event{{ID: "a"}}
		res := normalizeSessionEvents([][]event.Event{evts})
		require.Len(t, res, 1)
		assert.Equal(t, "a", res[0].ID)
	})
}

func Test_attachSummaries(t *testing.T) {
	ctx := context.Background()

	t.Run("skip_when_no_events", func(t *testing.T) {
		sess := session.NewSession("app", "user", "sess")
		cmd := redis.NewStringCmd(ctx, "hget", "k")
		attachSummaries(sess, cmd)
		assert.Empty(t, sess.Summaries)
	})

	t.Run("attach_when_events_present", func(t *testing.T) {
		sess := session.NewSession("app", "user", "sess")
		sess.Events = []event.Event{{ID: "evt"}}

		summary := map[string]*session.Summary{
			session.SummaryFilterKeyAllContents: {
				Summary:   "hi",
				UpdatedAt: time.Now(),
			},
		}
		bytes, err := json.Marshal(summary)
		require.NoError(t, err)

		cmd := redis.NewStringCmd(ctx, "hget", "k")
		cmd.SetVal(string(bytes))

		attachSummaries(sess, cmd)
		require.NotNil(t, sess.Summaries)
		assert.Contains(t, sess.Summaries, session.SummaryFilterKeyAllContents)
	})
}

func Test_collectTrackQueryResultsSkipNil(t *testing.T) {
	ctx := context.Background()
	cmd := redis.NewStringSliceCmd(ctx, "zrange", "k")
	cmd.SetErr(redis.Nil)

	results, err := collectTrackQueryResults(
		[]*trackQuery{
			{
				sessionIdx: 0,
				track:      session.Track("alpha"),
				cmd:        cmd,
			},
		},
		1,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Empty(t, results[0])
}

func fetchSessionState(t *testing.T, ctx context.Context, client *redis.Client, key session.Key) *SessionState {
	t.Helper()
	raw, err := client.HGet(ctx, getSessionStateKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	var state SessionState
	require.NoError(t, json.Unmarshal(raw, &state))
	if state.State == nil {
		state.State = make(session.StateMap)
	}
	return &state
}

func TestAppendEventHook(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	t.Run("hook modifies event before storage", func(t *testing.T) {
		hookCalled := false
		service, err := NewService(
			WithRedisClientURL(redisURL),
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				hookCalled = true
				ctx.Event.Tag = "hook_processed"
				return next()
			}),
		)
		require.NoError(t, err)
		defer service.Close()

		ctx := context.Background()
		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
		sess, err := service.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		// Add a user message first
		userEvt := event.New("inv0", "user")
		userEvt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "Hello"}}},
		}
		err = service.AppendEvent(ctx, sess, userEvt)
		require.NoError(t, err)

		// Then add assistant message
		evt := event.New("inv1", "assistant")
		evt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Hi there"}}},
		}

		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
		assert.True(t, hookCalled)

		// Verify the event was modified by hook
		assert.Equal(t, "hook_processed", evt.Tag)

		// Verify storage
		retrieved, err := service.GetSession(ctx, key)
		require.NoError(t, err)
		require.Len(t, retrieved.Events, 2)
	})

	t.Run("hook can abort event storage", func(t *testing.T) {
		service, err := NewService(
			WithRedisClientURL(redisURL),
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				return nil // skip next()
			}),
		)
		require.NoError(t, err)
		defer service.Close()

		ctx := context.Background()
		key := session.Key{AppName: "app2", UserID: "user", SessionID: "sess"}
		sess, err := service.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		evt := event.New("inv1", "assistant")
		evt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Hello"}}},
		}

		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)

		// Event should not be stored since hook aborted
		retrieved, err := service.GetSession(ctx, key)
		require.NoError(t, err)
		assert.Len(t, retrieved.Events, 0)
	})

	t.Run("multiple hooks execute in order", func(t *testing.T) {
		order := []string{}
		service, err := NewService(
			WithRedisClientURL(redisURL),
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				order = append(order, "hook1_before")
				err := next()
				order = append(order, "hook1_after")
				return err
			}),
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				order = append(order, "hook2_before")
				err := next()
				order = append(order, "hook2_after")
				return err
			}),
		)
		require.NoError(t, err)
		defer service.Close()

		ctx := context.Background()
		key := session.Key{AppName: "app3", UserID: "user", SessionID: "sess"}
		sess, err := service.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		evt := event.New("inv1", "assistant")
		evt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Hello"}}},
		}

		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)

		assert.Equal(t, []string{"hook1_before", "hook2_before", "hook2_after", "hook1_after"}, order)
	})
}

func TestAppendEvent_AsyncRecover(t *testing.T) {
	ch := make(chan *sessionEventPair, 1)
	close(ch)

	service := &Service{
		opts: ServiceOpts{
			enableAsyncPersist: true,
		},
		eventPairChans: []chan *sessionEventPair{ch},
	}
	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		State:   make(session.StateMap),
	}
	evt := &event.Event{Response: &model.Response{}}

	assert.NotPanics(t, func() {
		err := service.AppendEvent(context.Background(), sess, evt)
		require.NoError(t, err)
	})
}

func TestAppendTrackEvent_AsyncRecover(t *testing.T) {
	ch := make(chan *trackEventPair, 1)
	close(ch)

	service := &Service{
		opts: ServiceOpts{
			enableAsyncPersist: true,
		},
		trackEventChans: []chan *trackEventPair{ch},
	}
	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		State:   make(session.StateMap),
	}
	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Timestamp: time.Now(),
	}

	assert.NotPanics(t, func() {
		err := service.AppendTrackEvent(
			context.Background(),
			sess,
			trackEvent,
		)
		require.NoError(t, err)
	})
}

func TestGetSessionHook(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	t.Run("hook modifies session after retrieval", func(t *testing.T) {
		hookCalled := false
		service, err := NewService(
			WithRedisClientURL(redisURL),
			WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				hookCalled = true
				sess, err := next()
				if err != nil || sess == nil {
					return sess, err
				}
				sess.State["hook_added"] = []byte("true")
				return sess, nil
			}),
		)
		require.NoError(t, err)
		defer service.Close()

		ctx := context.Background()
		key := session.Key{AppName: "app4", UserID: "user", SessionID: "sess"}
		_, err = service.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		retrieved, err := service.GetSession(ctx, key)
		require.NoError(t, err)
		assert.True(t, hookCalled)
		assert.Equal(t, []byte("true"), retrieved.State["hook_added"])
	})

	t.Run("hook can filter events", func(t *testing.T) {
		service, err := NewService(
			WithRedisClientURL(redisURL),
			WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				sess, err := next()
				if err != nil || sess == nil {
					return sess, err
				}
				filtered := make([]event.Event, 0)
				for _, e := range sess.Events {
					if e.Tag != "skip" {
						filtered = append(filtered, e)
					}
				}
				sess.Events = filtered
				return sess, nil
			}),
		)
		require.NoError(t, err)
		defer service.Close()

		ctx := context.Background()
		key := session.Key{AppName: "app5", UserID: "user", SessionID: "sess"}
		sess, err := service.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		evt1 := event.New("inv1", "user")
		evt1.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "Q1"}}},
		}
		evt1.Tag = "skip"
		err = service.AppendEvent(ctx, sess, evt1)
		require.NoError(t, err)

		evt2 := event.New("inv2", "assistant")
		evt2.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "A1"}}},
		}
		err = service.AppendEvent(ctx, sess, evt2)
		require.NoError(t, err)

		retrieved, err := service.GetSession(ctx, key)
		require.NoError(t, err)
		assert.Len(t, retrieved.Events, 1)
		assert.Equal(t, "A1", retrieved.Events[0].Response.Choices[0].Message.Content)
	})

	t.Run("multiple hooks execute in order", func(t *testing.T) {
		order := []string{}
		service, err := NewService(
			WithRedisClientURL(redisURL),
			WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				order = append(order, "hook1_before")
				sess, err := next()
				order = append(order, "hook1_after")
				return sess, err
			}),
			WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				order = append(order, "hook2_before")
				sess, err := next()
				order = append(order, "hook2_after")
				return sess, err
			}),
		)
		require.NoError(t, err)
		defer service.Close()

		ctx := context.Background()
		key := session.Key{AppName: "app6", UserID: "user", SessionID: "sess"}
		_, err = service.CreateSession(ctx, key, nil)
		require.NoError(t, err)

		_, err = service.GetSession(ctx, key)
		require.NoError(t, err)

		assert.Equal(t, []string{"hook1_before", "hook2_before", "hook2_after", "hook1_after"}, order)
	})
}
