//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/spaolacci/murmur3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestWithEventNum(t *testing.T) {
	tests := []struct {
		name     string
		num      int
		expected int
	}{
		{
			name:     "positive number",
			num:      10,
			expected: 10,
		},
		{
			name:     "zero",
			num:      0,
			expected: 0,
		},
		{
			name:     "negative number",
			num:      -5,
			expected: -5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			option := WithEventNum(tt.num)
			opts := &Options{}
			option(opts)
			assert.Equal(t, tt.expected, opts.EventNum)
		})
	}
}

func TestWithEventTime(t *testing.T) {
	nowTime := time.Now()                   // fixed current time for test.
	pastTime := nowTime.Add(-1 * time.Hour) // one hour before now.

	tests := []struct {
		name string
		at   time.Time
	}{
		{
			name: "current time",
			at:   nowTime,
		},
		{
			name: "zero time",
			at:   time.Time{},
		},
		{
			name: "past time",
			at:   pastTime,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			option := WithEventTime(tt.at)
			opts := &Options{}
			option(opts)
			assert.True(t, opts.EventTime.Equal(tt.at))
		})
	}
}

func TestWithSummaryFilterKey(t *testing.T) {
	tests := []struct {
		name      string
		filterKey string
	}{
		{
			name:      "empty filter key",
			filterKey: "",
		},
		{
			name:      "specific filter key",
			filterKey: "user-messages",
		},
		{
			name:      "another filter key",
			filterKey: "tool-calls",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			option := WithSummaryFilterKey(tt.filterKey)
			opts := &SummaryOptions{}
			option(opts)
			assert.Equal(t, tt.filterKey, opts.FilterKey)
		})
	}
}

func TestKey_CheckSessionKey(t *testing.T) {
	tests := []struct {
		name    string
		key     Key
		wantErr error
	}{
		{
			name: "valid session key",
			key: Key{
				AppName:   "testapp",
				UserID:    "user123",
				SessionID: "session456",
			},
			wantErr: nil,
		},
		{
			name: "missing app name",
			key: Key{
				UserID:    "user123",
				SessionID: "session456",
			},
			wantErr: ErrAppNameRequired,
		},
		{
			name: "missing user id",
			key: Key{
				AppName:   "testapp",
				SessionID: "session456",
			},
			wantErr: ErrUserIDRequired,
		},
		{
			name: "missing session id",
			key: Key{
				AppName: "testapp",
				UserID:  "user123",
			},
			wantErr: ErrSessionIDRequired,
		},
		{
			name: "empty app name",
			key: Key{
				AppName:   "",
				UserID:    "user123",
				SessionID: "session456",
			},
			wantErr: ErrAppNameRequired,
		},
		{
			name: "empty user id",
			key: Key{
				AppName:   "testapp",
				UserID:    "",
				SessionID: "session456",
			},
			wantErr: ErrUserIDRequired,
		},
		{
			name: "empty session id",
			key: Key{
				AppName:   "testapp",
				UserID:    "user123",
				SessionID: "",
			},
			wantErr: ErrSessionIDRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.key.CheckSessionKey()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestKey_CheckUserKey(t *testing.T) {
	tests := []struct {
		name    string
		key     Key
		wantErr error
	}{
		{
			name: "valid user key",
			key: Key{
				AppName: "testapp",
				UserID:  "user123",
			},
			wantErr: nil,
		},
		{
			name: "missing app name",
			key: Key{
				UserID: "user123",
			},
			wantErr: ErrAppNameRequired,
		},
		{
			name: "missing user id",
			key: Key{
				AppName: "testapp",
			},
			wantErr: ErrUserIDRequired,
		},
		{
			name: "empty app name",
			key: Key{
				AppName: "",
				UserID:  "user123",
			},
			wantErr: ErrAppNameRequired,
		},
		{
			name: "empty user id",
			key: Key{
				AppName: "testapp",
				UserID:  "",
			},
			wantErr: ErrUserIDRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.key.CheckUserKey()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUserKey_CheckUserKey(t *testing.T) {
	tests := []struct {
		name    string
		key     UserKey
		wantErr error
	}{
		{
			name: "valid user key",
			key: UserKey{
				AppName: "testapp",
				UserID:  "user123",
			},
			wantErr: nil,
		},
		{
			name: "missing app name",
			key: UserKey{
				UserID: "user123",
			},
			wantErr: ErrAppNameRequired,
		},
		{
			name: "missing user id",
			key: UserKey{
				AppName: "testapp",
			},
			wantErr: ErrUserIDRequired,
		},
		{
			name: "empty app name",
			key: UserKey{
				AppName: "",
				UserID:  "user123",
			},
			wantErr: ErrAppNameRequired,
		},
		{
			name: "empty user id",
			key: UserKey{
				AppName: "testapp",
				UserID:  "",
			},
			wantErr: ErrUserIDRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.key.CheckUserKey()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSession_Struct(t *testing.T) {
	// Test that Session struct can be created and fields are accessible
	session := &Session{
		ID:        "test-session",
		AppName:   "testapp",
		UserID:    "user123",
		State:     StateMap{"key1": []byte("value1")},
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	assert.Equal(t, "test-session", session.ID)
	assert.Equal(t, "testapp", session.AppName)
	assert.Equal(t, "user123", session.UserID)
	assert.Equal(t, []byte("value1"), session.State["key1"])
	assert.Equal(t, 0, len(session.Events))
	assert.False(t, session.UpdatedAt.IsZero())
	assert.False(t, session.CreatedAt.IsZero())
}

func TestStateMap_Operations(t *testing.T) {
	// Test StateMap operations
	stateMap := StateMap{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
	}

	// Test get
	value, exists := stateMap["key1"]
	assert.True(t, exists)
	assert.Equal(t, []byte("value1"), value)

	// Test set
	stateMap["key3"] = []byte("value3")
	assert.Equal(t, []byte("value3"), stateMap["key3"])

	// Test delete
	delete(stateMap, "key2")
	_, exists = stateMap["key2"]
	assert.False(t, exists)

	// Test length
	assert.Equal(t, 2, len(stateMap))
}

func TestOptions_Struct(t *testing.T) {
	// Test that Options struct can be created and fields are accessible
	opts := &Options{
		EventNum:  10,
		EventTime: time.Now(),
	}

	assert.Equal(t, 10, opts.EventNum)
	assert.False(t, opts.EventTime.IsZero())
}

func TestSession_GetEvents(t *testing.T) {
	tests := []struct {
		name           string
		initialEvents  []event.Event
		expectedLength int
	}{
		{
			name: "get events from session with events",
			initialEvents: []event.Event{
				{ID: "event1"},
				{ID: "event2"},
				{ID: "event3"},
			},
			expectedLength: 3,
		},
		{
			name:           "get events from session with no events",
			initialEvents:  []event.Event{},
			expectedLength: 0,
		},
		{
			name:           "get events from session with nil events",
			initialEvents:  nil,
			expectedLength: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &Session{
				ID:      "test-session",
				AppName: "testapp",
				UserID:  "user123",
				Events:  tt.initialEvents,
			}

			events := session.GetEvents()

			assert.Equal(t, tt.expectedLength, len(events))

			// Verify that the returned events are a copy, not the original.
			if tt.expectedLength > 0 {
				// Modify the returned slice.
				events[0].ID = "modified"
				// Original should remain unchanged.
				assert.NotEqual(t, "modified", session.Events[0].ID)
			}
		})
	}
}

func TestSession_GetEventCount(t *testing.T) {
	tests := []struct {
		name          string
		initialEvents []event.Event
		expectedCount int
	}{
		{
			name: "count events in session with multiple events",
			initialEvents: []event.Event{
				{ID: "event1"},
				{ID: "event2"},
				{ID: "event3"},
			},
			expectedCount: 3,
		},
		{
			name: "count events in session with one event",
			initialEvents: []event.Event{
				{ID: "event1"},
			},
			expectedCount: 1,
		},
		{
			name:          "count events in session with no events",
			initialEvents: []event.Event{},
			expectedCount: 0,
		},
		{
			name:          "count events in session with nil events",
			initialEvents: nil,
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &Session{
				ID:      "test-session",
				AppName: "testapp",
				UserID:  "user123",
				Events:  tt.initialEvents,
			}

			count := session.GetEventCount()
			assert.Equal(t, tt.expectedCount, count)
		})
	}
}

func TestSessionAppendTrackEvent(t *testing.T) {
	t.Run("nil session returns error", func(t *testing.T) {
		var sess *Session
		err := sess.AppendTrackEvent(&TrackEvent{Track: "alpha"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "session is nil")
	})

	t.Run("nil event returns error", func(t *testing.T) {
		sess := &Session{}
		err := sess.AppendTrackEvent(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "track event is nil")
	})

	t.Run("append initializes state and stores copy", func(t *testing.T) {
		sess := &Session{}
		first := &TrackEvent{
			Track:   "alpha",
			Payload: json.RawMessage("first"),
		}
		err := sess.AppendTrackEvent(first)
		require.NoError(t, err)

		require.NotNil(t, sess.State)
		tracks, err := TracksFromState(sess.State)
		require.NoError(t, err)
		assert.Equal(t, []Track{"alpha"}, tracks)

		require.NotNil(t, sess.Tracks)
		trackData, ok := sess.Tracks["alpha"]
		require.True(t, ok)
		require.NotNil(t, trackData)
		require.Len(t, trackData.Events, 1)
		assert.Equal(t, first.Track, trackData.Events[0].Track)
		assert.Equal(t, json.RawMessage("first"), trackData.Events[0].Payload)
		assert.False(t, sess.UpdatedAt.IsZero())

		first.Payload = json.RawMessage("mutated")
		second := &TrackEvent{
			Track:   "alpha",
			Payload: json.RawMessage("second"),
		}
		err = sess.AppendTrackEvent(second)
		require.NoError(t, err)

		require.Len(t, trackData.Events, 2)
		assert.Equal(t, json.RawMessage("first"), trackData.Events[0].Payload)
		assert.Equal(t, json.RawMessage("second"), trackData.Events[1].Payload)
	})
}

func TestSessionGetTrackEvents(t *testing.T) {
	t.Run("tracks map empty", func(t *testing.T) {
		sess := &Session{}
		_, err := sess.GetTrackEvents("alpha")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tracks is empty")
	})

	t.Run("track missing", func(t *testing.T) {
		sess := &Session{Tracks: map[Track]*TrackEvents{}}
		_, err := sess.GetTrackEvents("alpha")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "track events not found")
	})

	t.Run("track entry nil", func(t *testing.T) {
		sess := &Session{Tracks: map[Track]*TrackEvents{"alpha": nil}}
		_, err := sess.GetTrackEvents("alpha")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "track events not found")
	})

	t.Run("track exists without events returns copy", func(t *testing.T) {
		original := &TrackEvents{Track: "alpha"}
		sess := &Session{Tracks: map[Track]*TrackEvents{"alpha": original}}
		result, err := sess.GetTrackEvents("alpha")
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, Track("alpha"), result.Track)
		assert.Nil(t, result.Events)
		assert.NotSame(t, original, result)
	})

	t.Run("track events slice copied", func(t *testing.T) {
		eventTime := time.Now()
		original := &TrackEvents{
			Track: "alpha",
			Events: []TrackEvent{
				{
					Track:     "alpha",
					Payload:   json.RawMessage("first"),
					Timestamp: eventTime,
				},
			},
		}
		sess := &Session{Tracks: map[Track]*TrackEvents{"alpha": original}}
		result, err := sess.GetTrackEvents("alpha")
		require.NoError(t, err)
		require.Len(t, result.Events, 1)
		assert.Equal(t, json.RawMessage("first"), original.Events[0].Payload)

		result.Events[0].Payload = json.RawMessage("changed")
		assert.Equal(t, json.RawMessage("first"), original.Events[0].Payload)
		assert.Equal(t, json.RawMessage("changed"), result.Events[0].Payload)
	})
}

func TestSession_GetEventsConcurrentSafety(t *testing.T) {
	// Test that GetEvents is safe for concurrent reads.
	session := &Session{
		ID:      "test-session",
		AppName: "testapp",
		UserID:  "user123",
		Events: []event.Event{
			{ID: "event1"},
			{ID: "event2"},
		},
	}

	// Run multiple goroutines reading events concurrently.
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			events := session.GetEvents()
			assert.Equal(t, 2, len(events))
			done <- true
		}()
	}

	// Wait for all goroutines to complete.
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestSession_GetEventCountConcurrentSafety(t *testing.T) {
	// Test that GetEventCount is safe for concurrent reads.
	session := &Session{
		ID:      "test-session",
		AppName: "testapp",
		UserID:  "user123",
		Events: []event.Event{
			{ID: "event1"},
			{ID: "event2"},
			{ID: "event3"},
		},
	}

	// Run multiple goroutines counting events concurrently.
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			count := session.GetEventCount()
			assert.Equal(t, 3, count)
			done <- true
		}()
	}

	// Wait for all goroutines to complete.
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestSummary_Struct(t *testing.T) {
	// Test that Summary struct can be created and fields are accessible.
	now := time.Now()
	summary := &Summary{
		Summary:   "This is a test summary",
		Topics:    []string{"topic1", "topic2", "topic3"},
		UpdatedAt: now,
	}

	assert.Equal(t, "This is a test summary", summary.Summary)
	assert.Equal(t, 3, len(summary.Topics))
	assert.Equal(t, "topic1", summary.Topics[0])
	assert.Equal(t, "topic2", summary.Topics[1])
	assert.Equal(t, "topic3", summary.Topics[2])
	assert.True(t, summary.UpdatedAt.Equal(now))
}

func TestService_Interface(t *testing.T) {
	// Test that Service interface is properly defined
	// This is a compile-time test to ensure the interface is complete
	var _ Service = (*MockService)(nil)
}

// MockService is a mock implementation of Service interface for testing
type MockService struct{}

func (m *MockService) CreateSession(ctx context.Context, key Key, state StateMap, options ...Option) (*Session, error) {
	return nil, nil
}

func (m *MockService) GetSession(ctx context.Context, key Key, options ...Option) (*Session, error) {
	return nil, nil
}

func (m *MockService) ListSessions(ctx context.Context, userKey UserKey, options ...Option) ([]*Session, error) {
	return nil, nil
}

func (m *MockService) DeleteSession(ctx context.Context, key Key, options ...Option) error {
	return nil
}

func (m *MockService) UpdateAppState(ctx context.Context, appName string, state StateMap) error {
	return nil
}

func (m *MockService) DeleteAppState(ctx context.Context, appName string, key string) error {
	return nil
}

func (m *MockService) ListAppStates(ctx context.Context, appName string) (StateMap, error) {
	return nil, nil
}

func (m *MockService) UpdateUserState(ctx context.Context, userKey UserKey, state StateMap) error {
	return nil
}

func (m *MockService) ListUserStates(ctx context.Context, userKey UserKey) (StateMap, error) {
	return nil, nil
}

func (m *MockService) DeleteUserState(ctx context.Context, userKey UserKey, key string) error {
	return nil
}

func (m *MockService) UpdateSessionState(ctx context.Context, key Key, state StateMap) error {
	return nil
}

func (m *MockService) AppendEvent(ctx context.Context, session *Session, event *event.Event, options ...Option) error {
	return nil
}

func (m *MockService) CreateSessionSummary(ctx context.Context, sess *Session, filterKey string, force bool) error {
	return nil
}

func (m *MockService) EnqueueSummaryJob(ctx context.Context, sess *Session, filterKey string, force bool) error {
	return nil
}

func (m *MockService) GetSessionSummaryText(ctx context.Context, sess *Session, opts ...SummaryOption) (string, bool) {
	return "", false
}

func (m *MockService) Close() error {
	return nil
}

// Helper function to create a test event with specified role
func createTestEvent(role model.Role, content string, timestamp time.Time, stateDelta StateMap) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    role,
						Content: content,
					},
				},
			},
		},
		Timestamp:  timestamp,
		StateDelta: stateDelta,
	}
}

// Helper function to create a test session
func createTestSession(events []event.Event, state StateMap) *Session {
	return &Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		Events:    events,
		State:     state,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func TestEnsureEventStartWithUser(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name           string
		inputSession   *Session
		expectedEvents []event.Event
		description    string
	}{
		{
			name:           "nil session",
			inputSession:   nil,
			expectedEvents: nil,
			description:    "Should handle nil session gracefully",
		},
		{
			name:           "empty events",
			inputSession:   createTestSession([]event.Event{}, nil),
			expectedEvents: []event.Event{},
			description:    "Should handle empty events gracefully",
		},
		{
			name: "events already start with user",
			inputSession: createTestSession([]event.Event{
				*createTestEvent(model.RoleUser, "user msg 1", now, nil),
				*createTestEvent(model.RoleAssistant, "assistant msg 1", now.Add(time.Minute), nil),
			}, nil),
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "user msg 1", now, nil),
				*createTestEvent(model.RoleAssistant, "assistant msg 1", now.Add(time.Minute), nil),
			},
			description: "Should keep all events when already starting with user",
		},
		{
			name: "remove assistant events at beginning",
			inputSession: createTestSession([]event.Event{
				*createTestEvent(model.RoleAssistant, "assistant msg 1", now, nil),
				*createTestEvent(model.RoleAssistant, "assistant msg 2", now.Add(time.Minute), nil),
				*createTestEvent(model.RoleUser, "user msg 1", now.Add(2*time.Minute), nil),
				*createTestEvent(model.RoleAssistant, "assistant msg 3", now.Add(3*time.Minute), nil),
			}, nil),
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "user msg 1", now.Add(2*time.Minute), nil),
				*createTestEvent(model.RoleAssistant, "assistant msg 3", now.Add(3*time.Minute), nil),
			},
			description: "Should remove events before first user event",
		},
		{
			name: "all assistant events",
			inputSession: createTestSession([]event.Event{
				*createTestEvent(model.RoleAssistant, "assistant msg 1", now, nil),
				*createTestEvent(model.RoleAssistant, "assistant msg 2", now.Add(time.Minute), nil),
			}, nil),
			expectedEvents: []event.Event{},
			description:    "Should clear all events when no user event found",
		},
		{
			name: "mixed roles with user at end",
			inputSession: createTestSession([]event.Event{
				*createTestEvent(model.RoleSystem, "system msg", now, nil),
				*createTestEvent(model.RoleAssistant, "assistant msg", now.Add(time.Minute), nil),
				*createTestEvent(model.RoleUser, "user msg", now.Add(2*time.Minute), nil),
			}, nil),
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "user msg", now.Add(2*time.Minute), nil),
			},
			description: "Should keep events from first user event to end",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy to avoid modifying the original
			var testSession *Session
			if tt.inputSession != nil {
				testSession = &Session{
					ID:        tt.inputSession.ID,
					AppName:   tt.inputSession.AppName,
					UserID:    tt.inputSession.UserID,
					Events:    make([]event.Event, len(tt.inputSession.Events)),
					State:     tt.inputSession.State,
					CreatedAt: tt.inputSession.CreatedAt,
					UpdatedAt: tt.inputSession.UpdatedAt,
				}
				copy(testSession.Events, tt.inputSession.Events)
			}

			testSession.EnsureEventStartWithUser()

			if tt.inputSession == nil {
				assert.Nil(t, testSession, tt.description)
			} else {
				require.NotNil(t, testSession, tt.description)
				assert.Equal(t, tt.expectedEvents, testSession.Events, tt.description)
			}
		})
	}
}

func TestApplyEventFiltering(t *testing.T) {
	now := time.Now()
	baseTime := now.Add(-10 * time.Minute)

	tests := []struct {
		name           string
		inputSession   *Session
		options        []Option
		expectedEvents []event.Event
		description    string
	}{
		{
			name:           "nil session",
			inputSession:   nil,
			options:        []Option{WithEventNum(2)},
			expectedEvents: nil,
			description:    "Should handle nil session gracefully",
		},
		{
			name: "event number limit",
			inputSession: createTestSession([]event.Event{
				*createTestEvent(model.RoleUser, "msg 1", baseTime, nil),
				*createTestEvent(model.RoleAssistant, "msg 2", baseTime.Add(time.Minute), nil),
				*createTestEvent(model.RoleUser, "msg 3", baseTime.Add(2*time.Minute), nil),
				*createTestEvent(model.RoleAssistant, "msg 4", baseTime.Add(3*time.Minute), nil),
			}, nil),
			options: []Option{WithEventNum(2)},
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "msg 3", baseTime.Add(2*time.Minute), nil),
				*createTestEvent(model.RoleAssistant, "msg 4", baseTime.Add(3*time.Minute), nil),
			},
			description: "Should keep only the last N events",
		},
		{
			name: "event time filter",
			inputSession: createTestSession([]event.Event{
				*createTestEvent(model.RoleUser, "old msg", baseTime, nil),
				*createTestEvent(model.RoleAssistant, "newer msg", baseTime.Add(5*time.Minute), nil),
				*createTestEvent(model.RoleUser, "newest msg", baseTime.Add(8*time.Minute), nil),
			}, nil),
			options: []Option{WithEventTime(baseTime.Add(4 * time.Minute))},
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "newest msg", baseTime.Add(8*time.Minute), nil),
			},
			description: "Should keep events after specified time",
		},
		{
			name: "event time filter - no matching events",
			inputSession: createTestSession([]event.Event{
				*createTestEvent(model.RoleUser, "old msg 1", baseTime, nil),
				*createTestEvent(model.RoleAssistant, "old msg 2", baseTime.Add(time.Minute), nil),
			}, nil),
			options: []Option{WithEventTime(baseTime.Add(10 * time.Minute))},
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "old msg 1", baseTime, nil),
			},
			description: "Should clear all events when none match time filter",
		},
		{
			name: "not user message",
			inputSession: createTestSession([]event.Event{
				*createTestEvent(model.RoleAssistant, "old msg 1", baseTime, nil),
				*createTestEvent(model.RoleAssistant, "old msg 2", baseTime.Add(time.Minute), nil),
			}, nil),
			options:        []Option{WithEventTime(baseTime.Add(10 * time.Minute))},
			expectedEvents: []event.Event{},
			description:    "Should clear all events when none match user message",
		},
		{
			name: "both number and time filters",
			inputSession: createTestSession([]event.Event{
				*createTestEvent(model.RoleUser, "msg 1", baseTime, nil),
				*createTestEvent(model.RoleAssistant, "msg 2", baseTime.Add(time.Minute), nil),
				*createTestEvent(model.RoleUser, "msg 3", baseTime.Add(2*time.Minute), nil),
				*createTestEvent(model.RoleAssistant, "msg 4", baseTime.Add(3*time.Minute), nil),
				*createTestEvent(model.RoleUser, "msg 5", baseTime.Add(4*time.Minute), nil),
			}, nil),
			options: []Option{
				WithEventNum(3),
				WithEventTime(baseTime.Add(90 * time.Second)),
			},
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "msg 3", baseTime.Add(2*time.Minute), nil),
				*createTestEvent(model.RoleAssistant, "msg 4", baseTime.Add(3*time.Minute), nil),
				*createTestEvent(model.RoleUser, "msg 5", baseTime.Add(4*time.Minute), nil),
			},
			description: "Should apply number limit first, then time filter",
		},
		{
			name: "no filtering options",
			inputSession: createTestSession([]event.Event{
				*createTestEvent(model.RoleUser, "msg 1", baseTime, nil),
				*createTestEvent(model.RoleAssistant, "msg 2", baseTime.Add(time.Minute), nil),
			}, nil),
			options: []Option{},
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "msg 1", baseTime, nil),
				*createTestEvent(model.RoleAssistant, "msg 2", baseTime.Add(time.Minute), nil),
			},
			description: "Should keep all events when no filters applied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy to avoid modifying the original
			var testSession *Session
			if tt.inputSession != nil {
				testSession = &Session{
					ID:        tt.inputSession.ID,
					AppName:   tt.inputSession.AppName,
					UserID:    tt.inputSession.UserID,
					Events:    make([]event.Event, len(tt.inputSession.Events)),
					State:     tt.inputSession.State,
					CreatedAt: tt.inputSession.CreatedAt,
					UpdatedAt: tt.inputSession.UpdatedAt,
				}
				copy(testSession.Events, tt.inputSession.Events)
			}

			testSession.ApplyEventFiltering(tt.options...)

			if tt.inputSession == nil {
				assert.Nil(t, testSession, tt.description)
			} else {
				require.NotNil(t, testSession, tt.description)
				assert.Equal(t, tt.expectedEvents, testSession.Events, tt.description)
			}
		})
	}
}

func TestApplyEventStateDelta(t *testing.T) {
	tests := []struct {
		name          string
		inputSession  *Session
		inputEvent    *event.Event
		expectedState StateMap
		description   string
	}{
		{
			name:          "nil session",
			inputSession:  nil,
			inputEvent:    createTestEvent(model.RoleUser, "test", time.Now(), StateMap{"key": []byte("value")}),
			expectedState: nil,
			description:   "Should handle nil session gracefully",
		},
		{
			name:          "nil event",
			inputSession:  createTestSession([]event.Event{}, StateMap{"existing": []byte("value")}),
			inputEvent:    nil,
			expectedState: StateMap{"existing": []byte("value")},
			description:   "Should handle nil event gracefully",
		},
		{
			name:          "nil session state",
			inputSession:  createTestSession([]event.Event{}, nil),
			inputEvent:    createTestEvent(model.RoleUser, "test", time.Now(), StateMap{"key1": []byte("value1")}),
			expectedState: StateMap{"key1": []byte("value1")},
			description:   "Should initialize state when nil",
		},
		{
			name:         "merge into existing state",
			inputSession: createTestSession([]event.Event{}, StateMap{"existing": []byte("old_value")}),
			inputEvent:   createTestEvent(model.RoleUser, "test", time.Now(), StateMap{"new_key": []byte("new_value")}),
			expectedState: StateMap{
				"existing": []byte("old_value"),
				"new_key":  []byte("new_value"),
			},
			description: "Should merge new state with existing state",
		},
		{
			name:          "overwrite existing state key",
			inputSession:  createTestSession([]event.Event{}, StateMap{"key": []byte("old_value")}),
			inputEvent:    createTestEvent(model.RoleUser, "test", time.Now(), StateMap{"key": []byte("new_value")}),
			expectedState: StateMap{"key": []byte("new_value")},
			description:   "Should overwrite existing state keys",
		},
		{
			name:          "empty state delta",
			inputSession:  createTestSession([]event.Event{}, StateMap{"existing": []byte("value")}),
			inputEvent:    createTestEvent(model.RoleUser, "test", time.Now(), StateMap{}),
			expectedState: StateMap{"existing": []byte("value")},
			description:   "Should leave state unchanged when delta is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy to avoid modifying the original
			var testSession *Session
			if tt.inputSession != nil {
				testSession = &Session{
					ID:        tt.inputSession.ID,
					AppName:   tt.inputSession.AppName,
					UserID:    tt.inputSession.UserID,
					Events:    tt.inputSession.Events,
					State:     make(StateMap),
					CreatedAt: tt.inputSession.CreatedAt,
					UpdatedAt: tt.inputSession.UpdatedAt,
				}
				for k, v := range tt.inputSession.State {
					testSession.State[k] = v
				}
			}

			testSession.ApplyEventStateDelta(tt.inputEvent)

			if tt.inputSession == nil {
				assert.Nil(t, testSession, tt.description)
			} else {
				require.NotNil(t, testSession, tt.description)
				assert.Equal(t, tt.expectedState, testSession.State, tt.description)
			}
		})
	}
}

func TestApplyEventStateDeltaMap(t *testing.T) {
	tests := []struct {
		name          string
		inputState    StateMap
		inputEvent    *event.Event
		expectedState StateMap
		description   string
	}{
		{
			name:          "nil state",
			inputState:    nil,
			inputEvent:    createTestEvent(model.RoleUser, "test", time.Now(), StateMap{"key": []byte("value")}),
			expectedState: nil,
			description:   "Should handle nil state gracefully",
		},
		{
			name:          "nil event",
			inputState:    StateMap{"existing": []byte("value")},
			inputEvent:    nil,
			expectedState: StateMap{"existing": []byte("value")},
			description:   "Should handle nil event gracefully",
		},
		{
			name:       "merge into existing state",
			inputState: StateMap{"existing": []byte("old_value")},
			inputEvent: createTestEvent(model.RoleUser, "test", time.Now(), StateMap{"new_key": []byte("new_value")}),
			expectedState: StateMap{
				"existing": []byte("old_value"),
				"new_key":  []byte("new_value"),
			},
			description: "Should merge new state with existing state",
		},
		{
			name:          "overwrite existing state key",
			inputState:    StateMap{"key": []byte("old_value")},
			inputEvent:    createTestEvent(model.RoleUser, "test", time.Now(), StateMap{"key": []byte("new_value")}),
			expectedState: StateMap{"key": []byte("new_value")},
			description:   "Should overwrite existing state keys",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy to avoid modifying the original
			var testState StateMap
			if tt.inputState != nil {
				testState = make(StateMap)
				for k, v := range tt.inputState {
					testState[k] = v
				}
			}

			ApplyEventStateDeltaMap(testState, tt.inputEvent)

			assert.Equal(t, tt.expectedState, testState, tt.description)
		})
	}
}

func TestUpdateUserSession(t *testing.T) {
	now := time.Now()
	baseTime := now.Add(-5 * time.Minute)

	tests := []struct {
		name            string
		inputSession    *Session
		inputEvent      *event.Event
		options         []Option
		expectedEvents  []event.Event
		expectedState   StateMap
		expectTimestamp bool
		description     string
	}{
		{
			name:            "nil session",
			inputSession:    nil,
			inputEvent:      createTestEvent(model.RoleUser, "test", now, nil),
			options:         []Option{},
			expectedEvents:  nil,
			expectedState:   nil,
			expectTimestamp: false,
			description:     "Should handle nil session gracefully",
		},
		{
			name:            "nil event",
			inputSession:    createTestSession([]event.Event{}, nil),
			inputEvent:      nil,
			options:         []Option{},
			expectedEvents:  []event.Event{},
			expectedState:   StateMap{}, // State gets initialized even when event is nil
			expectTimestamp: false,
			description:     "Should handle nil event gracefully",
		},
		{
			name:         "successful update with user event",
			inputSession: createTestSession([]event.Event{}, nil),
			inputEvent:   createTestEvent(model.RoleUser, "new message", now, StateMap{"key": []byte("value")}),
			options:      []Option{},
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "new message", now, StateMap{"key": []byte("value")}),
			},
			expectedState:   StateMap{"key": []byte("value")},
			expectTimestamp: true,
			description:     "Should append event and update state",
		},
		{
			name: "update with filtering",
			inputSession: createTestSession([]event.Event{
				*createTestEvent(model.RoleUser, "old msg 1", baseTime, nil),
				*createTestEvent(model.RoleAssistant, "old msg 2", baseTime.Add(time.Minute), nil),
			}, StateMap{"existing": []byte("value")}),
			inputEvent: createTestEvent(model.RoleUser, "new message", now, StateMap{"new_key": []byte("new_value")}),
			options:    []Option{WithEventNum(2)},
			// After filtering to keep last 2 events: [assistant msg, user new message]
			// After ensuring user start: [user new message] (assistant event at beginning is removed)
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "new message", now, StateMap{"new_key": []byte("new_value")}),
			},
			expectedState: StateMap{
				"existing": []byte("value"),
				"new_key":  []byte("new_value"),
			},
			expectTimestamp: true,
			description:     "Should apply filtering and ensure user start",
		},
		{
			name: "ensure user start after adding assistant event",
			inputSession: createTestSession([]event.Event{
				*createTestEvent(model.RoleUser, "user msg", baseTime, nil),
			}, nil),
			inputEvent: createTestEvent(model.RoleAssistant, "assistant msg", now, nil),
			options:    []Option{},
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "user msg", baseTime, nil),
				*createTestEvent(model.RoleAssistant, "assistant msg", now, nil),
			},
			expectedState:   StateMap{},
			expectTimestamp: true,
			description:     "Should keep events when they already start with user",
		},
		{
			name:         "response is nil",
			inputSession: createTestSession([]event.Event{}, nil),
			inputEvent: &event.Event{
				Timestamp:  now,
				StateDelta: nil,
			},
			options:         []Option{},
			expectedEvents:  []event.Event{},
			expectedState:   StateMap{},
			expectTimestamp: true,
			description:     "should not append to events when response is nil",
		},
		{
			name:         "response is partial",
			inputSession: createTestSession([]event.Event{}, nil),
			inputEvent: &event.Event{
				Response: &model.Response{
					IsPartial: true,
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Role:    "user",
								Content: "hello word",
							},
						},
					},
				},
				Timestamp:  now,
				StateDelta: nil,
			},
			options:         []Option{},
			expectedEvents:  []event.Event{},
			expectedState:   StateMap{},
			expectTimestamp: true,
			description:     "should not append to events when response is partial",
		},
		{
			name:         "response is invalid",
			inputSession: createTestSession([]event.Event{}, nil),
			inputEvent: &event.Event{
				Response: &model.Response{
					IsPartial: true,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    "assistant",
								Content: "",
							},
						},
					},
				},
				Timestamp:  now,
				StateDelta: nil,
			},
			options:         []Option{},
			expectedEvents:  []event.Event{},
			expectedState:   StateMap{},
			expectTimestamp: true,
			description:     "should not append to events when response is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy to avoid modifying the original
			var testSession *Session
			if tt.inputSession != nil {
				testSession = &Session{
					ID:        tt.inputSession.ID,
					AppName:   tt.inputSession.AppName,
					UserID:    tt.inputSession.UserID,
					Events:    make([]event.Event, len(tt.inputSession.Events)),
					State:     make(StateMap),
					CreatedAt: tt.inputSession.CreatedAt,
					UpdatedAt: tt.inputSession.UpdatedAt,
				}
				copy(testSession.Events, tt.inputSession.Events)
				for k, v := range tt.inputSession.State {
					testSession.State[k] = v
				}
			}

			oldUpdateTime := time.Time{}
			if testSession != nil {
				oldUpdateTime = testSession.UpdatedAt
			}

			testSession.UpdateUserSession(tt.inputEvent, tt.options...)

			if tt.inputSession == nil {
				assert.Nil(t, testSession, tt.description)
			} else {
				require.NotNil(t, testSession, tt.description)
				assert.Equal(t, tt.expectedEvents, testSession.Events, tt.description)
				assert.Equal(t, tt.expectedState, testSession.State, tt.description)

				if tt.expectTimestamp {
					assert.True(t, testSession.UpdatedAt.After(oldUpdateTime), "UpdatedAt should be updated")
				}
			}
		})
	}
}

func TestApplyOptions(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name         string
		options      []Option
		expectedNum  int
		expectedTime time.Time
		description  string
	}{
		{
			name:         "no options",
			options:      []Option{},
			expectedNum:  0,
			expectedTime: time.Time{},
			description:  "Should return zero values when no options provided",
		},
		{
			name:         "event number option",
			options:      []Option{WithEventNum(5)},
			expectedNum:  5,
			expectedTime: time.Time{},
			description:  "Should set event number correctly",
		},
		{
			name:         "event time option",
			options:      []Option{WithEventTime(now)},
			expectedNum:  0,
			expectedTime: now,
			description:  "Should set event time correctly",
		},
		{
			name:         "multiple options",
			options:      []Option{WithEventNum(3), WithEventTime(now)},
			expectedNum:  3,
			expectedTime: now,
			description:  "Should set both event number and time correctly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyOptions(tt.options...)

			assert.Equal(t, tt.expectedNum, result.EventNum, tt.description)
			assert.Equal(t, tt.expectedTime, result.EventTime, tt.description)
		})
	}
}

func TestSession_Clone(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name         string
		setupSession func() *Session
		wantErr      bool
	}{
		{
			name: "complete session with all fields populated",
			setupSession: func() *Session {
				return &Session{
					ID:      "session-123",
					AppName: "test-app",
					UserID:  "user-456",
					State: StateMap{
						"key1": []byte("value1"),
						"key2": []byte("value2"),
					},
					Events: []event.Event{
						{ID: "event1"},
						{ID: "event2"},
					},
					Summaries: map[string]*Summary{
						"filter1": {Summary: "filter1"},
						"filter2": {Summary: "filter2"},
					},
					UpdatedAt: now,
					CreatedAt: now.Add(-time.Hour),
					Hash:      12345,
				}
			},
		},
		{
			name: "session with nil state map",
			setupSession: func() *Session {
				return &Session{
					ID:        "session-nil-state",
					AppName:   "test-app",
					UserID:    "user-789",
					State:     nil,
					Events:    []event.Event{},
					Summaries: map[string]*Summary{},
					UpdatedAt: now,
					CreatedAt: now.Add(-2 * time.Hour),
					Hash:      67890,
				}
			},
		},
		{
			name: "session with empty state map",
			setupSession: func() *Session {
				return &Session{
					ID:        "session-empty-state",
					AppName:   "test-app",
					UserID:    "user-000",
					State:     StateMap{},
					Events:    []event.Event{{ID: "event1"}},
					Summaries: nil,
					UpdatedAt: now.Add(-30 * time.Minute),
					CreatedAt: now.Add(-3 * time.Hour),
					Hash:      11111,
				}
			},
		},
		{
			name: "session with nil summaries",
			setupSession: func() *Session {
				return &Session{
					ID:      "session-nil-summaries",
					AppName: "test-app",
					UserID:  "user-111",
					State: StateMap{
						"singleKey": []byte("singleValue"),
					},
					Events:    []event.Event{},
					Summaries: nil,
					UpdatedAt: now.Add(-time.Hour),
					CreatedAt: now.Add(-4 * time.Hour),
					Hash:      22222,
				}
			},
		},
		{
			name: "session with empty events slice",
			setupSession: func() *Session {
				return &Session{
					ID:        "session-empty-events",
					AppName:   "test-app",
					UserID:    "user-222",
					State:     StateMap{},
					Events:    []event.Event{},
					Summaries: map[string]*Summary{},
					UpdatedAt: now,
					CreatedAt: now.Add(-5 * time.Hour),
					Hash:      33333,
				}
			},
		},
		{
			name: "session with nil events slice",
			setupSession: func() *Session {
				sess := &Session{
					ID:      "session-nil-events",
					AppName: "test-app",
					UserID:  "user-333",
					State:   StateMap{"test": []byte("value")},
					Summaries: map[string]*Summary{
						"test": {Summary: "test"},
					},
					UpdatedAt: now,
					CreatedAt: now.Add(-6 * time.Hour),
					Hash:      44444,
				}
				// Use reflection to set Events to nil since it's not exported in the literal
				// This is a workaround for testing edge cases
				sess.Events = nil
				return sess
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := tt.setupSession()

			// Execute clone operation
			cloned := original.Clone()

			// Validate basic field equality
			if cloned.ID != original.ID {
				t.Errorf("Clone() ID = %v, want %v", cloned.ID, original.ID)
			}
			if cloned.AppName != original.AppName {
				t.Errorf("Clone() AppName = %v, want %v", cloned.AppName, original.AppName)
			}
			if cloned.UserID != original.UserID {
				t.Errorf("Clone() UserID = %v, want %v", cloned.UserID, original.UserID)
			}
			if !cloned.UpdatedAt.Equal(original.UpdatedAt) {
				t.Errorf("Clone() UpdatedAt = %v, want %v", cloned.UpdatedAt, original.UpdatedAt)
			}
			if !cloned.CreatedAt.Equal(original.CreatedAt) {
				t.Errorf("Clone() CreatedAt = %v, want %v", cloned.CreatedAt, original.CreatedAt)
			}
			if cloned.Hash != original.Hash {
				t.Errorf("Clone() Hash = %v, want %v", cloned.Hash, original.Hash)
			}

			// Test that it's a different instance
			if cloned == original {
				t.Error("Clone() should return a different instance")
			}

			// Test Events deep copy
			if len(cloned.Events) != len(original.Events) {
				t.Errorf("Clone() Events length = %v, want %v", len(cloned.Events), len(original.Events))
			} else {
				// Verify content equality
				for i := range original.Events {
					if !reflect.DeepEqual(cloned.Events[i], original.Events[i]) {
						t.Errorf("Clone() Events[%d] = %v, want %v", i, cloned.Events[i], original.Events[i])
					}
				}

				// Test deep copy by modifying clone (if there are events)
				if len(cloned.Events) > 0 {
					originalFirstEvent := original.Events[0]
					// Create a modified event (assuming event.Event has a Type field)
					modifiedEvent := event.Event{ID: "modified"}
					cloned.Events[0] = modifiedEvent

					// Original should remain unchanged
					if !reflect.DeepEqual(original.Events[0], originalFirstEvent) {
						t.Error("Clone() Events are not deep copied - modifying clone affected original")
					}
				}
			}

			// Test State deep copy
			if len(cloned.State) != len(original.State) {
				t.Errorf("Clone() State length = %v, want %v", len(cloned.State), len(original.State))
			} else {
				// Verify content equality
				for k, v := range original.State {
					clonedVal, exists := cloned.State[k]
					if !exists {
						t.Errorf("Clone() State missing key %s", k)
						continue
					}
					if !bytes.Equal(clonedVal, v) {
						t.Errorf("Clone() State[%s] = %v, want %v", k, clonedVal, v)
					}
				}

				// Test deep copy by modifying clone
				if len(cloned.State) > 0 {
					for k := range cloned.State {
						originalVal := original.State[k]
						cloned.State[k] = []byte("modified")

						// Original should remain unchanged
						if bytes.Equal(original.State[k], []byte("modified")) {
							t.Errorf("Clone() State are not deep copied - modifying clone affected original state key %s", k)
						}
						// Restore original value for other tests
						cloned.State[k] = originalVal
						break // Only test one key
					}
				}
			}

			// Test Summaries deep copy
			if len(cloned.Summaries) != len(original.Summaries) {
				t.Errorf("Clone() Summaries length = %v, want %v", len(cloned.Summaries), len(original.Summaries))
			} else {
				for k, originalSummary := range original.Summaries {
					clonedSummary, exists := cloned.Summaries[k]
					if !exists {
						t.Errorf("Clone() Summaries missing key %s", k)
						continue
					}

					// Test that we have a copy (different pointer)
					if clonedSummary == originalSummary {
						t.Errorf("Clone() Summaries[%s] should be a different pointer", k)
					}

					// Test content equality
					if clonedSummary.Summary != originalSummary.Summary {
						t.Errorf("Clone() Summaries[%s].Count = %v, want %v", k, clonedSummary.Summary, originalSummary.Summary)
					}
				}
			}

			// Test that mutex fields are zero values (not copied)
			if cloned.EventMu != (sync.RWMutex{}) {
				t.Error("Clone() should not copy EventMu mutex")
			}
			if cloned.SummariesMu != (sync.RWMutex{}) {
				t.Error("Clone() should not copy SummariesMu mutex")
			}
		})
	}
}

// Additional test for concurrent access during clone
func TestSession_Clone_Concurrent(t *testing.T) {
	sess := &Session{
		ID:      "concurrent-session",
		AppName: "test-app",
		UserID:  "user-123",
		State: StateMap{
			"key1": []byte("value1"),
			"key2": []byte("value2"),
		},
		Events: []event.Event{
			{ID: "event1"},
			{ID: "event2"},
		},
		Summaries: map[string]*Summary{
			"summary1": {Summary: "summary1"},
		},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now().Add(-time.Hour),
		Hash:      99999,
	}

	// Test concurrent access doesn't cause race conditions
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cloned := sess.Clone()
			if cloned.ID != sess.ID {
				t.Errorf("Concurrent Clone() ID mismatch: got %v, want %v", cloned.ID, sess.ID)
			}
		}()
	}
	wg.Wait()
}

func TestNewSession(t *testing.T) {
	fixedTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		appName   string
		userID    string
		sessionID string
		options   []SessionOptions
		want      *Session
		wantErr   bool
	}{
		{
			name:      "basic session without options",
			appName:   "test-app",
			userID:    "user-123",
			sessionID: "session-456",
			options:   []SessionOptions{},
			want: &Session{
				ID:        "session-456",
				AppName:   "test-app",
				UserID:    "user-123",
				Events:    []event.Event{},
				Summaries: map[string]*Summary{},
				State:     StateMap{},
			},
		},
		{
			name:      "session with events",
			appName:   "test-app",
			userID:    "user-123",
			sessionID: "session-456",
			options: []SessionOptions{
				WithSessionEvents([]event.Event{
					{ID: "event1"},
					{ID: "event2"},
				}),
			},
			want: &Session{
				ID:      "session-456",
				AppName: "test-app",
				UserID:  "user-123",
				Events: []event.Event{
					{ID: "event1"},
					{ID: "event2"},
				},
				Summaries: map[string]*Summary{},
				State:     StateMap{},
			},
		},
		{
			name:      "session with summaries",
			appName:   "test-app",
			userID:    "user-123",
			sessionID: "session-456",
			options: []SessionOptions{
				WithSessionSummaries(map[string]*Summary{
					"filter1": {Summary: "summary1"},
					"filter2": {Summary: "summary2"},
				}),
			},
			want: &Session{
				ID:      "session-456",
				AppName: "test-app",
				UserID:  "user-123",
				Events:  []event.Event{},
				Summaries: map[string]*Summary{
					"filter1": {Summary: "summary1"},
					"filter2": {Summary: "summary2"},
				},
				State: StateMap{},
			},
		},
		{
			name:      "session with state",
			appName:   "test-app",
			userID:    "user-123",
			sessionID: "session-456",
			options: []SessionOptions{
				WithSessionState(StateMap{
					"key1": []byte("value1"),
					"key2": []byte("value2"),
				}),
			},
			want: &Session{
				ID:        "session-456",
				AppName:   "test-app",
				UserID:    "user-123",
				Events:    []event.Event{},
				Summaries: map[string]*Summary{},
				State: StateMap{
					"key1": []byte("value1"),
					"key2": []byte("value2"),
				},
			},
		},
		{
			name:      "session with custom timestamps",
			appName:   "test-app",
			userID:    "user-123",
			sessionID: "session-456",
			options: []SessionOptions{
				WithSessionCreatedAt(fixedTime),
				WithSessionUpdatedAt(fixedTime.Add(time.Hour)),
			},
			want: &Session{
				ID:        "session-456",
				AppName:   "test-app",
				UserID:    "user-123",
				Events:    []event.Event{},
				Summaries: map[string]*Summary{},
				State:     StateMap{},
				CreatedAt: fixedTime,
				UpdatedAt: fixedTime.Add(time.Hour),
			},
		},
		{
			name:      "session with all options combined",
			appName:   "test-app",
			userID:    "user-123",
			sessionID: "session-456",
			options: []SessionOptions{
				WithSessionEvents([]event.Event{
					{ID: "event1"},
				}),
				WithSessionSummaries(map[string]*Summary{
					"filter1": {Summary: "summary1"},
				}),
				WithSessionState(StateMap{
					"key1": []byte("value1"),
				}),
				WithSessionCreatedAt(fixedTime),
				WithSessionUpdatedAt(fixedTime.Add(time.Hour)),
			},
			want: &Session{
				ID:      "session-456",
				AppName: "test-app",
				UserID:  "user-123",
				Events: []event.Event{
					{ID: "event1"},
				},
				Summaries: map[string]*Summary{
					"filter1": {Summary: "summary1"},
				},
				State: StateMap{
					"key1": []byte("value1"),
				},
				CreatedAt: fixedTime,
				UpdatedAt: fixedTime.Add(time.Hour),
			},
		},
		{
			name:      "session with empty strings",
			appName:   "",
			userID:    "",
			sessionID: "",
			options:   []SessionOptions{},
			want: &Session{
				ID:        "",
				AppName:   "",
				UserID:    "",
				Events:    []event.Event{},
				Summaries: map[string]*Summary{},
				State:     StateMap{},
			},
		},
		{
			name:      "session with special characters in IDs",
			appName:   "test-app-特殊字符",
			userID:    "user@example.com",
			sessionID: "session/with/slashes",
			options:   []SessionOptions{},
			want: &Session{
				ID:        "session/with/slashes",
				AppName:   "test-app-特殊字符",
				UserID:    "user@example.com",
				Events:    []event.Event{},
				Summaries: map[string]*Summary{},
				State:     StateMap{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create the session
			got := NewSession(tt.appName, tt.userID, tt.sessionID, tt.options...)

			// Test basic fields
			if got.ID != tt.want.ID {
				t.Errorf("NewSession() ID = %v, want %v", got.ID, tt.want.ID)
			}
			if got.AppName != tt.want.AppName {
				t.Errorf("NewSession() AppName = %v, want %v", got.AppName, tt.want.AppName)
			}
			if got.UserID != tt.want.UserID {
				t.Errorf("NewSession() UserID = %v, want %v", got.UserID, tt.want.UserID)
			}

			// If CreatedAt was set in options, verify it
			if !tt.want.CreatedAt.IsZero() && !got.CreatedAt.Equal(tt.want.CreatedAt) {
				t.Errorf("NewSession() CreatedAt = %v, want %v", got.CreatedAt, tt.want.CreatedAt)
			}

			// If UpdatedAt was set in options, verify it
			if !tt.want.UpdatedAt.IsZero() && !got.UpdatedAt.Equal(tt.want.UpdatedAt) {
				t.Errorf("NewSession() UpdatedAt = %v, want %v", got.UpdatedAt, tt.want.UpdatedAt)
			}

			// Test that timestamps are set when not provided (for default case)
			if len(tt.options) == 0 {
				if got.CreatedAt.IsZero() {
					t.Error("NewSession() CreatedAt should be set when not provided")
				}
				if got.UpdatedAt.IsZero() {
					t.Error("NewSession() UpdatedAt should be set when not provided")
				}
			}

			// Test Events
			if len(got.Events) != len(tt.want.Events) {
				t.Errorf("NewSession() Events length = %v, want %v", len(got.Events), len(tt.want.Events))
			} else {
				for i, event := range tt.want.Events {
					if !reflect.DeepEqual(got.Events[i], event) {
						t.Errorf("NewSession() Events[%d] = %v, want %v", i, got.Events[i], event)
					}
				}
			}

			// Test Summaries
			if len(got.Summaries) != len(tt.want.Summaries) {
				t.Errorf("NewSession() Summaries length = %v, want %v", len(got.Summaries), len(tt.want.Summaries))
			} else {
				for k, wantSummary := range tt.want.Summaries {
					gotSummary, exists := got.Summaries[k]
					if !exists {
						t.Errorf("NewSession() Summaries missing key %s", k)
						continue
					}
					if gotSummary.Summary != wantSummary.Summary {
						t.Errorf("NewSession() Summaries[%s].Count = %v, want %v", k, gotSummary.Summary, wantSummary.Summary)
					}
				}
			}

			// Test State
			if len(got.State) != len(tt.want.State) {
				t.Errorf("NewSession() State length = %v, want %v", len(got.State), len(tt.want.State))
			} else {
				for k, wantValue := range tt.want.State {
					gotValue, exists := got.State[k]
					if !exists {
						t.Errorf("NewSession() State missing key %s", k)
						continue
					}
					if !bytes.Equal(gotValue, wantValue) {
						t.Errorf("NewSession() State[%s] = %v, want %v", k, gotValue, wantValue)
					}
				}
			}

			// Test that Hash is computed correctly
			expectedHashKey := fmt.Sprintf("%s:%s:%s", tt.appName, tt.userID, tt.sessionID)
			expectedHash := int(murmur3.Sum32([]byte(expectedHashKey)))
			if got.Hash != expectedHash {
				t.Errorf("NewSession() Hash = %v, want %v", got.Hash, expectedHash)
			}

			// Test that mutexes are initialized (zero values)
			if got.EventMu != (sync.RWMutex{}) {
				t.Error("NewSession() EventMu should be initialized")
			}
			if got.SummariesMu != (sync.RWMutex{}) {
				t.Error("NewSession() SummariesMu should be initialized")
			}
		})
	}
}

// Test option function application order
func TestNewSession_OptionOrder(t *testing.T) {
	// Test that later options override earlier ones
	firstTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	secondTime := time.Date(2023, 12, 31, 23, 59, 59, 0, time.UTC)

	sess := NewSession("app", "user", "session",
		WithSessionCreatedAt(firstTime),
		WithSessionCreatedAt(secondTime), // This should override the first
	)

	if !sess.CreatedAt.Equal(secondTime) {
		t.Errorf("NewSession() option order: CreatedAt = %v, want %v (last option should win)", sess.CreatedAt, secondTime)
	}
}
