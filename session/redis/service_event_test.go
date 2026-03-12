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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

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
				assert.Equal(t, len(events), len(finalSess.Events))
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

			time.Sleep(10 * time.Millisecond)

			events := tt.setupEvents()
			for _, evt := range events {
				err = service.AppendEvent(context.Background(), sess, evt)
				require.NoError(t, err)
				time.Sleep(1 * time.Millisecond)
			}

			finalSess, err := service.GetSession(context.Background(), sessionKey)
			require.NoError(t, err)
			assert.NotNil(t, finalSess)

			assert.Equal(t, sess.CreatedAt.Unix(), finalSess.CreatedAt.Unix())

			tt.validate(t, initialUpdateTime, finalSess, events)
		})
	}
}

func TestService_AppendEvent_ErrorCases(t *testing.T) {
	t.Run("nil_event_panics", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()

		service, err := NewService(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer service.Close()

		key := session.Key{AppName: "testapp", UserID: "user123", SessionID: "session123"}
		sess, err := service.CreateSession(context.Background(), key, session.StateMap{})
		require.NoError(t, err)

		assert.Panics(t, func() {
			_ = service.AppendEvent(context.Background(), sess, nil)
		})
	})
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

			events := tt.setupEvents()
			for _, evt := range events {
				err = service.AppendEvent(context.Background(), sess, evt, session.WithEventTime(evt.Timestamp))
				require.NoError(t, err, "Failed to append event %s: %v", evt.ID, err)
			}

			finalSess, err := service.GetSession(context.Background(), sessionKey)
			require.NoError(t, err)
			require.NotNil(t, finalSess)

			assert.Equal(t, len(tt.expectedOrder), len(finalSess.Events),
				"Expected %d events, got %d. Description: %s",
				len(tt.expectedOrder), len(finalSess.Events), tt.description)

			actualOrder := make([]string, len(finalSess.Events))
			for i, evt := range finalSess.Events {
				actualOrder[i] = evt.ID
			}

			assert.Equal(t, tt.expectedOrder, actualOrder,
				"Event order mismatch. Description: %s\nExpected: %v\nActual: %v",
				tt.description, tt.expectedOrder, actualOrder)

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

	sessionKey := session.Key{AppName: "testapp", UserID: "user123", SessionID: "session123"}
	sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{})
	require.NoError(t, err)

	base := time.Now().Add(-5 * time.Minute)
	ids := []string{"e1", "e2", "e3", "e4", "e5"}
	for i, id := range ids {
		evt := createTestEvent(id, "agent", "content", base.Add(time.Duration(i)*time.Second), false)
		err := service.AppendEvent(context.Background(), sess, evt, session.WithEventTime(evt.Timestamp))
		require.NoError(t, err)
	}

	got, err := service.GetSession(context.Background(), sessionKey, session.WithEventNum(0))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Events, limit)
	assert.Equal(t, []string{"e3", "e4", "e5"}, []string{got.Events[0].ID, got.Events[1].ID, got.Events[2].ID})
}

func TestEnsureEventStartWithUser(t *testing.T) {
	tests := []struct {
		name           string
		setupEvents    func() []event.Event
		expectedLength int
		expectFirst    bool
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
			expectedLength: 2,
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
			expectedLength: 0,
			expectFirst:    false,
		},
		{
			name: "events_with_no_response",
			setupEvents: func() []event.Event {
				evt1 := event.New("test1", "unknown")
				evt2 := event.New("test2", "user")
				evt2.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "User message"}}},
				}
				return []event.Event{*evt1, *evt2}
			},
			expectedLength: 1,
			expectFirst:    true,
		},
		{
			name: "events_with_empty_choices",
			setupEvents: func() []event.Event {
				evt1 := event.New("test1", "unknown")
				evt1.Response = &model.Response{
					Choices: []model.Choice{},
				}
				evt2 := event.New("test2", "user")
				evt2.Response = &model.Response{
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "User message"}}},
				}
				return []event.Event{*evt1, *evt2}
			},
			expectedLength: 1,
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

	sessionKey := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session123",
	}

	sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{})
	require.NoError(t, err)

	baseTime := time.Now()
	events := []*event.Event{
		{
			ID: "event1", Timestamp: baseTime.Add(-5 * time.Hour),
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Assistant message 1"}}}},
		},
		{
			ID: "event2", Timestamp: baseTime.Add(-4 * time.Hour),
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Assistant message 2"}}}},
		},
		{
			ID: "event3", Timestamp: baseTime.Add(-3 * time.Hour),
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "User message 1"}}}},
		},
		{
			ID: "event4", Timestamp: baseTime.Add(-2 * time.Hour),
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Assistant message 3"}}}},
		},
	}

	for _, evt := range events {
		err := service.AppendEvent(context.Background(), sess, evt)
		require.NoError(t, err)
	}

	retrievedSess, err := service.GetSession(context.Background(), sessionKey)
	require.NoError(t, err)
	require.NotNil(t, retrievedSess)

	assert.Equal(t, 2, len(retrievedSess.Events), "Should filter out assistant events before first user event")
	assert.Equal(t, "event3", retrievedSess.Events[0].ID)
	assert.Equal(t, model.RoleUser, retrievedSess.Events[0].Response.Choices[0].Message.Role)
	assert.Equal(t, "event4", retrievedSess.Events[1].ID)

	sessionList, err := service.ListSessions(context.Background(), session.UserKey{AppName: "testapp", UserID: "user123"})
	require.NoError(t, err)
	require.Len(t, sessionList, 1)
	assert.Equal(t, 2, len(sessionList[0].Events))
	assert.Equal(t, "event3", sessionList[0].Events[0].ID)
}

func TestGetSession_AllAssistantEvents_Integration(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	sessionKey := session.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	sess, err := service.CreateSession(context.Background(), sessionKey, session.StateMap{})
	require.NoError(t, err)

	baseTime := time.Now()
	events := []*event.Event{
		{ID: "event1", Timestamp: baseTime.Add(-3 * time.Hour),
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "user message 1"}}}}},
		{ID: "event2", Timestamp: baseTime.Add(-2 * time.Hour),
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Assistant message 1"}}}}},
		{ID: "event3", Timestamp: baseTime.Add(-1 * time.Hour),
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Assistant message 2"}}}}},
	}

	for _, evt := range events {
		err := service.AppendEvent(context.Background(), sess, evt)
		require.NoError(t, err)
	}

	retrievedSess, err := service.GetSession(context.Background(), sessionKey)
	require.NoError(t, err)
	require.NotNil(t, retrievedSess)
	assert.Equal(t, 3, len(retrievedSess.Events))
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
	key := session.Key{AppName: "testapp", UserID: "user123", SessionID: "session123"}

	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		evt := createTestEvent(fmt.Sprintf("e%d", i), "agent", "content", time.Now(), false)
		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
	}

	retrievedSess, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, retrievedSess)
	assert.LessOrEqual(t, len(retrievedSess.Events), limit)
}

func TestService_GetEventsList_AfterTime(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	service, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "testapp", UserID: "user123", SessionID: "session123"}

	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	baseTime := time.Now().Add(-1 * time.Hour)
	for i := 0; i < 5; i++ {
		evt := createTestEvent(fmt.Sprintf("e%d", i), "agent", "content", baseTime.Add(time.Duration(i)*time.Minute), false)
		err = service.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
	}

	afterTime := baseTime.Add(2 * time.Minute)
	retrievedSess, err := service.GetSession(ctx, key, session.WithEventTime(afterTime))
	require.NoError(t, err)
	assert.NotNil(t, retrievedSess)
	assert.LessOrEqual(t, len(retrievedSess.Events), 3)
}
