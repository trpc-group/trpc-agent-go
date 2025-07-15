//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package session

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
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

func (m *MockService) AppendEvent(ctx context.Context, session *Session, event *event.Event, options ...Option) error {
	return nil
}

func (m *MockService) Close() error {
	return nil
}
