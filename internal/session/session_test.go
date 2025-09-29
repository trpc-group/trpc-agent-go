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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Helper function to create a test event with specified role
func createTestEvent(role model.Role, content string, timestamp time.Time, stateDelta session.StateMap) *event.Event {
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
func createTestSession(events []event.Event, state session.StateMap) *session.Session {
	return &session.Session{
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
		inputSession   *session.Session
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
			var testSession *session.Session
			if tt.inputSession != nil {
				testSession = &session.Session{
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

			EnsureEventStartWithUser(testSession)

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
		inputSession   *session.Session
		options        []session.Option
		expectedEvents []event.Event
		description    string
	}{
		{
			name:           "nil session",
			inputSession:   nil,
			options:        []session.Option{session.WithEventNum(2)},
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
			options: []session.Option{session.WithEventNum(2)},
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
			options: []session.Option{session.WithEventTime(baseTime.Add(4 * time.Minute))},
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleAssistant, "newer msg", baseTime.Add(5*time.Minute), nil),
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
			options:        []session.Option{session.WithEventTime(baseTime.Add(10 * time.Minute))},
			expectedEvents: []event.Event{},
			description:    "Should clear all events when none match time filter",
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
			options: []session.Option{
				session.WithEventNum(3),
				session.WithEventTime(baseTime.Add(90 * time.Second)),
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
			options: []session.Option{},
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
			var testSession *session.Session
			if tt.inputSession != nil {
				testSession = &session.Session{
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

			ApplyEventFiltering(testSession, tt.options...)

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
		inputSession  *session.Session
		inputEvent    *event.Event
		expectedState session.StateMap
		description   string
	}{
		{
			name:          "nil session",
			inputSession:  nil,
			inputEvent:    createTestEvent(model.RoleUser, "test", time.Now(), session.StateMap{"key": []byte("value")}),
			expectedState: nil,
			description:   "Should handle nil session gracefully",
		},
		{
			name:          "nil event",
			inputSession:  createTestSession([]event.Event{}, session.StateMap{"existing": []byte("value")}),
			inputEvent:    nil,
			expectedState: session.StateMap{"existing": []byte("value")},
			description:   "Should handle nil event gracefully",
		},
		{
			name:          "nil session state",
			inputSession:  createTestSession([]event.Event{}, nil),
			inputEvent:    createTestEvent(model.RoleUser, "test", time.Now(), session.StateMap{"key1": []byte("value1")}),
			expectedState: session.StateMap{"key1": []byte("value1")},
			description:   "Should initialize state when nil",
		},
		{
			name:         "merge into existing state",
			inputSession: createTestSession([]event.Event{}, session.StateMap{"existing": []byte("old_value")}),
			inputEvent:   createTestEvent(model.RoleUser, "test", time.Now(), session.StateMap{"new_key": []byte("new_value")}),
			expectedState: session.StateMap{
				"existing": []byte("old_value"),
				"new_key":  []byte("new_value"),
			},
			description: "Should merge new state with existing state",
		},
		{
			name:          "overwrite existing state key",
			inputSession:  createTestSession([]event.Event{}, session.StateMap{"key": []byte("old_value")}),
			inputEvent:    createTestEvent(model.RoleUser, "test", time.Now(), session.StateMap{"key": []byte("new_value")}),
			expectedState: session.StateMap{"key": []byte("new_value")},
			description:   "Should overwrite existing state keys",
		},
		{
			name:          "empty state delta",
			inputSession:  createTestSession([]event.Event{}, session.StateMap{"existing": []byte("value")}),
			inputEvent:    createTestEvent(model.RoleUser, "test", time.Now(), session.StateMap{}),
			expectedState: session.StateMap{"existing": []byte("value")},
			description:   "Should leave state unchanged when delta is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy to avoid modifying the original
			var testSession *session.Session
			if tt.inputSession != nil {
				testSession = &session.Session{
					ID:        tt.inputSession.ID,
					AppName:   tt.inputSession.AppName,
					UserID:    tt.inputSession.UserID,
					Events:    tt.inputSession.Events,
					State:     make(session.StateMap),
					CreatedAt: tt.inputSession.CreatedAt,
					UpdatedAt: tt.inputSession.UpdatedAt,
				}
				for k, v := range tt.inputSession.State {
					testSession.State[k] = v
				}
			}

			ApplyEventStateDelta(testSession, tt.inputEvent)

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
		inputState    session.StateMap
		inputEvent    *event.Event
		expectedState session.StateMap
		description   string
	}{
		{
			name:          "nil state",
			inputState:    nil,
			inputEvent:    createTestEvent(model.RoleUser, "test", time.Now(), session.StateMap{"key": []byte("value")}),
			expectedState: nil,
			description:   "Should handle nil state gracefully",
		},
		{
			name:          "nil event",
			inputState:    session.StateMap{"existing": []byte("value")},
			inputEvent:    nil,
			expectedState: session.StateMap{"existing": []byte("value")},
			description:   "Should handle nil event gracefully",
		},
		{
			name:       "merge into existing state",
			inputState: session.StateMap{"existing": []byte("old_value")},
			inputEvent: createTestEvent(model.RoleUser, "test", time.Now(), session.StateMap{"new_key": []byte("new_value")}),
			expectedState: session.StateMap{
				"existing": []byte("old_value"),
				"new_key":  []byte("new_value"),
			},
			description: "Should merge new state with existing state",
		},
		{
			name:          "overwrite existing state key",
			inputState:    session.StateMap{"key": []byte("old_value")},
			inputEvent:    createTestEvent(model.RoleUser, "test", time.Now(), session.StateMap{"key": []byte("new_value")}),
			expectedState: session.StateMap{"key": []byte("new_value")},
			description:   "Should overwrite existing state keys",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy to avoid modifying the original
			var testState session.StateMap
			if tt.inputState != nil {
				testState = make(session.StateMap)
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
		inputSession    *session.Session
		inputEvent      *event.Event
		options         []session.Option
		expectedEvents  []event.Event
		expectedState   session.StateMap
		expectTimestamp bool
		description     string
	}{
		{
			name:            "nil session",
			inputSession:    nil,
			inputEvent:      createTestEvent(model.RoleUser, "test", now, nil),
			options:         []session.Option{},
			expectedEvents:  nil,
			expectedState:   nil,
			expectTimestamp: false,
			description:     "Should handle nil session gracefully",
		},
		{
			name:            "nil event",
			inputSession:    createTestSession([]event.Event{}, nil),
			inputEvent:      nil,
			options:         []session.Option{},
			expectedEvents:  []event.Event{},
			expectedState:   session.StateMap{}, // State gets initialized even when event is nil
			expectTimestamp: false,
			description:     "Should handle nil event gracefully",
		},
		{
			name:         "successful update with user event",
			inputSession: createTestSession([]event.Event{}, nil),
			inputEvent:   createTestEvent(model.RoleUser, "new message", now, session.StateMap{"key": []byte("value")}),
			options:      []session.Option{},
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "new message", now, session.StateMap{"key": []byte("value")}),
			},
			expectedState:   session.StateMap{"key": []byte("value")},
			expectTimestamp: true,
			description:     "Should append event and update state",
		},
		{
			name: "update with filtering",
			inputSession: createTestSession([]event.Event{
				*createTestEvent(model.RoleUser, "old msg 1", baseTime, nil),
				*createTestEvent(model.RoleAssistant, "old msg 2", baseTime.Add(time.Minute), nil),
			}, session.StateMap{"existing": []byte("value")}),
			inputEvent: createTestEvent(model.RoleUser, "new message", now, session.StateMap{"new_key": []byte("new_value")}),
			options:    []session.Option{session.WithEventNum(2)},
			// After filtering to keep last 2 events: [assistant msg, user new message]
			// After ensuring user start: [user new message] (assistant event at beginning is removed)
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "new message", now, session.StateMap{"new_key": []byte("new_value")}),
			},
			expectedState: session.StateMap{
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
			options:    []session.Option{},
			expectedEvents: []event.Event{
				*createTestEvent(model.RoleUser, "user msg", baseTime, nil),
				*createTestEvent(model.RoleAssistant, "assistant msg", now, nil),
			},
			expectedState:   session.StateMap{},
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
			options:         []session.Option{},
			expectedEvents:  []event.Event{},
			expectedState:   session.StateMap{},
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
			options:         []session.Option{},
			expectedEvents:  []event.Event{},
			expectedState:   session.StateMap{},
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
			options:         []session.Option{},
			expectedEvents:  []event.Event{},
			expectedState:   session.StateMap{},
			expectTimestamp: true,
			description:     "should not append to events when response is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy to avoid modifying the original
			var testSession *session.Session
			if tt.inputSession != nil {
				testSession = &session.Session{
					ID:        tt.inputSession.ID,
					AppName:   tt.inputSession.AppName,
					UserID:    tt.inputSession.UserID,
					Events:    make([]event.Event, len(tt.inputSession.Events)),
					State:     make(session.StateMap),
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

			UpdateUserSession(testSession, tt.inputEvent, tt.options...)

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
		options      []session.Option
		expectedNum  int
		expectedTime time.Time
		description  string
	}{
		{
			name:         "no options",
			options:      []session.Option{},
			expectedNum:  0,
			expectedTime: time.Time{},
			description:  "Should return zero values when no options provided",
		},
		{
			name:         "event number option",
			options:      []session.Option{session.WithEventNum(5)},
			expectedNum:  5,
			expectedTime: time.Time{},
			description:  "Should set event number correctly",
		},
		{
			name:         "event time option",
			options:      []session.Option{session.WithEventTime(now)},
			expectedNum:  0,
			expectedTime: now,
			description:  "Should set event time correctly",
		},
		{
			name:         "multiple options",
			options:      []session.Option{session.WithEventNum(3), session.WithEventTime(now)},
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
