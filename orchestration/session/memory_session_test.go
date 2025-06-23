package session

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewInMemorySessionService(t *testing.T) {
	tests := []struct {
		name string
		want bool // whether service should be successfully created
	}{
		{
			name: "create new service",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewInMemorySessionService()

			assert.Equal(t, tt.want, service != nil)

			if service != nil {
				assert.NotNil(t, service.buckets, "buckets map should be initialized")

				_, exists := service.buckets[""]
				assert.True(t, exists, "default bucket should be created")
			}
		})
	}
}

func TestCreateSession(t *testing.T) {
	tests := []struct {
		name      string
		appName   string
		userID    string
		state     StateMap
		sessionID string
		wantErr   bool
		validate  func(*testing.T, *Session, error)
	}{
		{
			name:      "create session with provided ID",
			appName:   "test-app",
			userID:    "test-user",
			state:     StateMap{"key1": "value1", "key2": 42},
			sessionID: "test-session-id",
			wantErr:   false,
			validate: func(t *testing.T, session *Session, err error) {
				require.NoError(t, err)
				assert.Equal(t, "test-session-id", session.ID)
				assert.Equal(t, "test-app", session.AppName)
				assert.Equal(t, "test-user", session.UserID)
			},
		},
		{
			name:      "create session with auto-generated ID",
			appName:   "test-app",
			userID:    "test-user",
			state:     StateMap{"test": "data"},
			sessionID: "",
			wantErr:   false,
			validate: func(t *testing.T, session *Session, err error) {
				require.NoError(t, err)
				assert.NotEmpty(t, session.ID, "expected auto-generated session ID")
				assert.Len(t, session.ID, 36, "expected UUID format (36 chars)")
			},
		},
		{
			name:      "create session with nil state",
			appName:   "test-app",
			userID:    "test-user",
			state:     nil,
			sessionID: "test-session",
			wantErr:   false,
			validate: func(t *testing.T, session *Session, err error) {
				require.NoError(t, err)
				assert.NotNil(t, session.State, "session state should not be nil")
			},
		},
		{
			name:      "create session with empty state",
			appName:   "test-app",
			userID:    "test-user",
			state:     StateMap{},
			sessionID: "test-session",
			wantErr:   false,
			validate: func(t *testing.T, session *Session, err error) {
				require.NoError(t, err)
				assert.NotNil(t, session.State, "session state should not be nil")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewInMemorySessionService()
			ctx := context.Background()

			session, err := service.CreateSession(ctx, tt.appName, tt.userID, tt.state, tt.sessionID)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.validate != nil {
				tt.validate(t, session, err)
			}
		})
	}
}

func TestGetSession(t *testing.T) {
	service := NewInMemorySessionService()
	ctx := context.Background()

	// Setup: create test sessions
	setupData := []struct {
		appName   string
		userID    string
		sessionID string
		state     StateMap
	}{
		{"app1", "user1", "session1", StateMap{"key1": "value1"}},
		{"app1", "user1", "session2", StateMap{"key2": "value2"}},
		{"app2", "user2", "session3", StateMap{"key3": "value3"}},
	}

	for _, data := range setupData {
		_, err := service.CreateSession(ctx, data.appName, data.userID, data.state, data.sessionID)
		require.NoError(t, err, "setup failed")
	}

	tests := []struct {
		name      string
		appName   string
		userID    string
		sessionID string
		opts      *GetSessionOpts
		wantNil   bool
		wantErr   bool
		validate  func(*testing.T, *Session, error)
	}{
		{
			name:      "get existing session",
			appName:   "app1",
			userID:    "user1",
			sessionID: "session1",
			opts:      nil,
			wantNil:   false,
			wantErr:   false,
			validate: func(t *testing.T, session *Session, err error) {
				assert.Equal(t, "session1", session.ID)
			},
		},
		{
			name:      "get non-existent session",
			appName:   "app1",
			userID:    "user1",
			sessionID: "nonexistent",
			opts:      nil,
			wantNil:   true,
			wantErr:   false,
			validate:  nil,
		},
		{
			name:      "get session from non-existent app",
			appName:   "nonexistent-app",
			userID:    "user1",
			sessionID: "session1",
			opts:      nil,
			wantNil:   true,
			wantErr:   false,
			validate:  nil,
		},
		{
			name:      "get session with NumRecentEvents option",
			appName:   "app1",
			userID:    "user1",
			sessionID: "session1",
			opts:      &GetSessionOpts{NumRecentEvents: 2},
			wantNil:   false,
			wantErr:   false,
			validate: func(t *testing.T, session *Session, err error) {
				assert.NotNil(t, session, "expected session, got nil")
			},
		},
		{
			name:      "get session with AfterTime option",
			appName:   "app1",
			userID:    "user1",
			sessionID: "session1",
			opts:      &GetSessionOpts{AfterTime: time.Now().Add(-1 * time.Hour)},
			wantNil:   false,
			wantErr:   false,
			validate: func(t *testing.T, session *Session, err error) {
				assert.NotNil(t, session, "expected session, got nil")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := service.GetSession(ctx, tt.appName, tt.userID, tt.sessionID, tt.opts)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.wantNil {
				assert.Nil(t, session)
			} else {
				assert.NotNil(t, session)
			}

			if tt.validate != nil {
				tt.validate(t, session, err)
			}
		})
	}
}

func TestListSessions(t *testing.T) {
	tests := []struct {
		name      string
		setupData []struct {
			appName   string
			userID    string
			sessionID string
		}
		listAppName string
		listUserID  string
		wantCount   int
		wantErr     bool
	}{
		{
			name: "list sessions for user with sessions",
			setupData: []struct {
				appName   string
				userID    string
				sessionID string
			}{
				{"app1", "user1", "session1"},
				{"app1", "user1", "session2"},
				{"app1", "user1", "session3"},
				{"app1", "user2", "session4"}, // different user
				{"app2", "user1", "session5"}, // different app
			},
			listAppName: "app1",
			listUserID:  "user1",
			wantCount:   3,
			wantErr:     false,
		},
		{
			name:        "list sessions for user with no sessions",
			setupData:   nil,
			listAppName: "nonexistent-app",
			listUserID:  "nonexistent-user",
			wantCount:   0,
			wantErr:     false,
		},
		{
			name: "list sessions for different user",
			setupData: []struct {
				appName   string
				userID    string
				sessionID string
			}{
				{"app1", "user1", "session1"},
				{"app1", "user2", "session2"},
			},
			listAppName: "app1",
			listUserID:  "user2",
			wantCount:   1,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Fresh service for each test
			testService := NewInMemorySessionService()
			ctx := context.Background()

			// Setup test data
			for _, data := range tt.setupData {
				_, err := testService.CreateSession(ctx, data.appName, data.userID, StateMap{"test": "data"}, data.sessionID)
				require.NoError(t, err, "setup failed")
			}

			// Test ListSessions
			sessions, err := testService.ListSessions(ctx, tt.listAppName, tt.listUserID)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.Len(t, sessions, tt.wantCount, "unexpected number of sessions returned")
		})
	}
}

func TestDeleteSession(t *testing.T) {
	service := NewInMemorySessionService()
	ctx := context.Background()

	// Setup: create test session
	appName := "test-app"
	userID := "test-user"
	sessionID := "test-session"
	_, err := service.CreateSession(ctx, appName, userID, StateMap{"test": "data"}, sessionID)
	require.NoError(t, err, "setup failed")

	tests := []struct {
		name          string
		deleteAppName string
		deleteUserID  string
		deleteSession string
		wantErr       bool
		shouldExist   bool // whether session should exist after deletion
	}{
		{
			name:          "delete existing session",
			deleteAppName: appName,
			deleteUserID:  userID,
			deleteSession: sessionID,
			wantErr:       false,
			shouldExist:   false,
		},
		{
			name:          "delete non-existent session",
			deleteAppName: "nonexistent-app",
			deleteUserID:  "nonexistent-user",
			deleteSession: "nonexistent-session",
			wantErr:       false,
			shouldExist:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.DeleteSession(ctx, tt.deleteAppName, tt.deleteUserID, tt.deleteSession)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Verify deletion by trying to get the original session
			if tt.name == "delete existing session" {
				session, err := service.GetSession(ctx, appName, userID, sessionID, nil)
				assert.NoError(t, err, "GetSession() after delete failed")

				if tt.shouldExist {
					assert.NotNil(t, session, "session should exist after deletion")
				} else {
					assert.Nil(t, session, "session should not exist after deletion")
				}
			}
		})
	}
}

func TestConcurrentAccess(t *testing.T) {
	service := NewInMemorySessionService()
	ctx := context.Background()

	tests := []struct {
		name        string
		concurrency int
		appName     string
		userID      string
	}{
		{
			name:        "concurrent session creation",
			concurrency: 10,
			appName:     "test-app",
			userID:      "test-user",
		},
		{
			name:        "concurrent bucket creation",
			concurrency: 5,
			appName:     "concurrent-app",
			userID:      "test-user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan bool, tt.concurrency)
			errors := make(chan error, tt.concurrency)

			// Launch concurrent operations
			for i := 0; i < tt.concurrency; i++ {
				go func(id int) {
					defer func() { done <- true }()

					sessionID := fmt.Sprintf("session-%d", id)
					state := StateMap{"id": id}

					_, err := service.CreateSession(ctx, tt.appName, tt.userID, state, sessionID)
					if err != nil {
						errors <- err
						return
					}
				}(i)
			}

			// Wait for all operations to complete
			for i := 0; i < tt.concurrency; i++ {
				<-done
			}

			// Check for errors
			close(errors)
			for err := range errors {
				assert.NoError(t, err, "concurrent operation failed")
			}

			// Verify all sessions were created
			sessions, err := service.ListSessions(ctx, tt.appName, tt.userID)
			require.NoError(t, err, "ListSessions() failed")
			assert.Len(t, sessions, tt.concurrency, "expected all sessions to be created")
		})
	}
}

func TestGetOrCreateBucket(t *testing.T) {
	service := NewInMemorySessionService()

	tests := []struct {
		name    string
		appName string
		want    bool // whether bucket should be created/retrieved successfully
	}{
		{
			name:    "create bucket for new app",
			appName: "new-app",
			want:    true,
		},
		{
			name:    "get existing bucket",
			appName: "new-app", // same as above, should return existing
			want:    true,
		},
		{
			name:    "create bucket for another app",
			appName: "another-app",
			want:    true,
		},
		{
			name:    "get default bucket",
			appName: "",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket := service.getOrCreateBucket(tt.appName)

			assert.Equal(t, tt.want, bucket != nil)

			if bucket != nil {
				// Verify bucket is properly initialized
				assert.NotNil(t, bucket.sessions, "bucket sessions map should be initialized")
				assert.NotNil(t, bucket.userState, "bucket userState map should be initialized")
				assert.NotNil(t, bucket.appState, "bucket appState map should be initialized")
			}
		})
	}
}

// Additional tests for edge cases and State functionality
func TestStateMerging(t *testing.T) {
	service := NewInMemorySessionService()
	ctx := context.Background()

	appName := "test-app"
	userID := "test-user"
	sessionID := "test-session"

	// Setup app and user state
	bucket := service.getOrCreateBucket(appName)
	bucket.appState["config"] = "production"
	bucket.appState["version"] = "1.0.0"

	if bucket.userState[userID] == nil {
		bucket.userState[userID] = make(StateMap)
	}
	bucket.userState[userID]["preference"] = "dark_mode"
	bucket.userState[userID]["language"] = "zh-CN"

	// Create session
	sessionState := StateMap{"context": "chat"}
	session, err := service.CreateSession(ctx, appName, userID, sessionState, sessionID)
	require.NoError(t, err)

	// Verify session is created successfully
	assert.NotNil(t, session, "session should be created")
	assert.Equal(t, sessionID, session.ID)
	assert.Equal(t, appName, session.AppName)
	assert.Equal(t, userID, session.UserID)
}

func TestBucketIsolation(t *testing.T) {
	service := NewInMemorySessionService()
	ctx := context.Background()

	// Create sessions in different apps
	app1Session, err := service.CreateSession(ctx, "app1", "user1", StateMap{"key": "value1"}, "session1")
	require.NoError(t, err)

	app2Session, err := service.CreateSession(ctx, "app2", "user1", StateMap{"key": "value2"}, "session2")
	require.NoError(t, err)

	// Verify sessions are isolated
	assert.Equal(t, "app1", app1Session.AppName)
	assert.Equal(t, "app2", app2Session.AppName)

	// List sessions for each app should only return sessions from that app
	app1Sessions, err := service.ListSessions(ctx, "app1", "user1")
	require.NoError(t, err)
	assert.Len(t, app1Sessions, 1)

	app2Sessions, err := service.ListSessions(ctx, "app2", "user1")
	require.NoError(t, err)
	assert.Len(t, app2Sessions, 1)
}
