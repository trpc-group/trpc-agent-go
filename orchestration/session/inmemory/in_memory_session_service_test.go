package inmemory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
)

func TestNewSessionService(t *testing.T) {
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
			service := NewSessionService()

			assert.Equal(t, tt.want, service != nil)

			if service != nil {
				assert.NotNil(t, service.apps, "apps map should be initialized")
				// Apps are created on demand, so initially the map should be empty
				assert.Equal(t, 0, len(service.apps), "apps map should be initially empty")
			}
		})
	}
}

func TestCreateSession(t *testing.T) {
	tests := []struct {
		name      string
		appName   string
		userID    string
		state     session.StateMap
		sessionID string
		wantErr   bool
		validate  func(*testing.T, *session.Session, error)
	}{
		{
			name:      "create session with provided ID",
			appName:   "test-app",
			userID:    "test-user",
			state:     session.StateMap{"key1": "value1", "key2": 42},
			sessionID: "test-session-id",
			wantErr:   false,
			validate: func(t *testing.T, session *session.Session, err error) {
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
			state:     session.StateMap{"test": "data"},
			sessionID: "",
			wantErr:   false,
			validate: func(t *testing.T, session *session.Session, err error) {
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
			validate: func(t *testing.T, session *session.Session, err error) {
				require.NoError(t, err)
				assert.NotNil(t, session.State, "session state should not be nil")
			},
		},
		{
			name:      "create session with empty state",
			appName:   "test-app",
			userID:    "test-user",
			state:     session.StateMap{},
			sessionID: "test-session",
			wantErr:   false,
			validate: func(t *testing.T, session *session.Session, err error) {
				require.NoError(t, err)
				assert.NotNil(t, session.State, "session state should not be nil")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewSessionService()
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
	service := NewSessionService()
	ctx := context.Background()

	// Setup: create test sessions
	setupData := []struct {
		appName   string
		userID    string
		sessionID string
		state     session.StateMap
	}{
		{"app1", "user1", "session1", session.StateMap{"key1": "value1"}},
		{"app1", "user1", "session2", session.StateMap{"key2": "value2"}},
		{"app2", "user2", "session3", session.StateMap{"key3": "value3"}},
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
		opts      *session.GetSessionOpts
		wantNil   bool
		wantErr   bool
		validate  func(*testing.T, *session.Session, error)
	}{
		{
			name:      "get existing session",
			appName:   "app1",
			userID:    "user1",
			sessionID: "session1",
			opts:      nil,
			wantNil:   false,
			wantErr:   false,
			validate: func(t *testing.T, session *session.Session, err error) {
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
			opts:      &session.GetSessionOpts{NumRecentEvents: 2},
			wantNil:   false,
			wantErr:   false,
			validate: func(t *testing.T, session *session.Session, err error) {
				assert.NotNil(t, session, "expected session, got nil")
			},
		},
		{
			name:      "get session with AfterTime option",
			appName:   "app1",
			userID:    "user1",
			sessionID: "session1",
			opts:      &session.GetSessionOpts{AfterTime: time.Now().Add(-1 * time.Hour)},
			wantNil:   false,
			wantErr:   false,
			validate: func(t *testing.T, session *session.Session, err error) {
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
			testService := NewSessionService()
			ctx := context.Background()

			// Setup test data
			for _, data := range tt.setupData {
				_, err := testService.CreateSession(
					ctx,
					data.appName,
					data.userID,
					session.StateMap{"test": "data"},
					data.sessionID,
				)
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
	service := NewSessionService()
	ctx := context.Background()

	// Setup: create test session
	appName := "test-app"
	userID := "test-user"
	sessionID := "test-session"
	_, err := service.CreateSession(ctx, appName, userID, session.StateMap{"test": "data"}, sessionID)
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
	service := NewSessionService()
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
			name:        "concurrent app creation",
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
					state := session.StateMap{"id": id}

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

func TestGetOrCreateApp(t *testing.T) {
	service := NewSessionService()

	tests := []struct {
		name    string
		appName string
		want    bool // whether app should be created/retrieved successfully
	}{
		{
			name:    "create app for new application",
			appName: "new-app",
			want:    true,
		},
		{
			name:    "get existing app",
			appName: "new-app", // same as above, should return existing
			want:    true,
		},
		{
			name:    "create app for another application",
			appName: "another-app",
			want:    true,
		},
		{
			name:    "get default app",
			appName: "",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := service.getOrCreateApp(tt.appName)

			assert.Equal(t, tt.want, app != nil)

			if app != nil {
				// Verify app is properly initialized
				assert.NotNil(t, app.sessions, "app sessions map should be initialized")
				assert.NotNil(t, app.userState, "app userState map should be initialized")
				assert.NotNil(t, app.appState, "app appState map should be initialized")
			}
		})
	}
}

// Additional tests for edge cases and State functionality
func TestStateMerging(t *testing.T) {
	service := NewSessionService()
	ctx := context.Background()

	appName := "test-app"
	userID := "test-user"
	sessionID := "test-session"

	// Setup app and user state
	app := service.getOrCreateApp(appName)
	app.appState["config"] = "production"
	app.appState["version"] = "1.0.0"

	if app.userState[userID] == nil {
		app.userState[userID] = make(session.StateMap)
	}
	app.userState[userID]["preference"] = "dark_mode"
	app.userState[userID]["language"] = "zh-CN"

	// Create session
	sessionState := session.StateMap{"context": "chat"}
	sess, err := service.CreateSession(ctx, appName, userID, sessionState, sessionID)
	require.NoError(t, err)

	// Verify session is created successfully
	assert.NotNil(t, sess, "session should be created")
	assert.Equal(t, sessionID, sess.ID)
	assert.Equal(t, appName, sess.AppName)
	assert.Equal(t, userID, sess.UserID)
}

func TestAppIsolation(t *testing.T) {
	service := NewSessionService()
	ctx := context.Background()

	// Create sessions in different apps
	app1Session, err := service.CreateSession(ctx, "app1", "user1", session.StateMap{"key": "value1"}, "session1")
	require.NoError(t, err)

	app2Session, err := service.CreateSession(ctx, "app2", "user1", session.StateMap{"key": "value2"}, "session2")
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
