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

var testServiceOpts = ServiceOpts{}

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
			service := NewSessionService(testServiceOpts)

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
		name     string
		appName  string
		userID   string
		state    session.StateMap
		sessID   string
		wantErr  bool
		validate func(*testing.T, *session.Session, error)
	}{
		{
			name:    "create session with provided ID",
			appName: "test-app",
			userID:  "test-user",
			state:   session.StateMap{"key1": "value1", "key2": 42},
			sessID:  "test-session-id",
			wantErr: false,
			validate: func(t *testing.T, sess *session.Session, err error) {
				require.NoError(t, err)
				assert.Equal(t, "test-session-id", sess.ID)
				assert.Equal(t, "test-app", sess.AppName)
				assert.Equal(t, "test-user", sess.UserID)
			},
		},
		{
			name:    "create session with auto-generated ID",
			appName: "test-app",
			userID:  "test-user",
			state:   session.StateMap{"test": "data"},
			sessID:  "",
			wantErr: false,
			validate: func(t *testing.T, sess *session.Session, err error) {
				require.NoError(t, err)
				assert.NotEmpty(t, sess.ID, "expected auto-generated session ID")
				assert.Len(t, sess.ID, 36, "expected UUID format (36 chars)")
			},
		},
		{
			name:    "create session with nil state",
			appName: "test-app",
			userID:  "test-user",
			state:   nil,
			sessID:  "test-session",
			wantErr: false,
			validate: func(t *testing.T, sess *session.Session, err error) {
				require.NoError(t, err)
				assert.NotNil(t, sess.State, "session state should not be nil")
			},
		},
		{
			name:    "create session with empty state",
			appName: "test-app",
			userID:  "test-user",
			state:   session.StateMap{},
			sessID:  "test-session",
			wantErr: false,
			validate: func(t *testing.T, sess *session.Session, err error) {
				require.NoError(t, err)
				assert.NotNil(t, sess.State, "session state should not be nil")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewSessionService(testServiceOpts)
			ctx := context.Background()

			key := session.Key{
				AppName:   tt.appName,
				UserID:    tt.userID,
				SessionID: tt.sessID,
			}

			sess, err := service.CreateSession(ctx, key, tt.state, nil)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.validate != nil {
				tt.validate(t, sess, err)
			}
		})
	}
}

func TestGetSession(t *testing.T) {
	service := NewSessionService(testServiceOpts)
	ctx := context.Background()

	// Setup: create test sessions
	setupData := []struct {
		appName string
		userID  string
		sessID  string
		state   session.StateMap
	}{
		{"app1", "user1", "session1", session.StateMap{"key1": "value1"}},
		{"app1", "user1", "session2", session.StateMap{"key2": "value2"}},
		{"app2", "user2", "session3", session.StateMap{"key3": "value3"}},
	}

	for _, data := range setupData {
		key := session.Key{
			AppName:   data.appName,
			UserID:    data.userID,
			SessionID: data.sessID,
		}
		_, err := service.CreateSession(ctx, key, data.state, nil)
		require.NoError(t, err, "setup failed")
	}

	tests := []struct {
		name     string
		appName  string
		userID   string
		sessID   string
		opts     *session.Options
		wantNil  bool
		wantErr  bool
		validate func(*testing.T, *session.Session, error)
	}{
		{
			name:    "get existing session",
			appName: "app1",
			userID:  "user1",
			sessID:  "session1",
			opts:    nil,
			wantNil: false,
			wantErr: false,
			validate: func(t *testing.T, sess *session.Session, err error) {
				assert.Equal(t, "session1", sess.ID)
			},
		},
		{
			name:     "get non-existent session",
			appName:  "app1",
			userID:   "user1",
			sessID:   "nonexistent",
			opts:     nil,
			wantNil:  true,
			wantErr:  false,
			validate: nil,
		},
		{
			name:     "get session from non-existent app",
			appName:  "nonexistent-app",
			userID:   "user1",
			sessID:   "session1",
			opts:     nil,
			wantNil:  true,
			wantErr:  false,
			validate: nil,
		},
		{
			name:    "get session with EventNum option",
			appName: "app1",
			userID:  "user1",
			sessID:  "session1",
			opts:    &session.Options{EventNum: 2},
			wantNil: false,
			wantErr: false,
			validate: func(t *testing.T, sess *session.Session, err error) {
				assert.NotNil(t, sess, "expected session, got nil")
			},
		},
		{
			name:    "get session with EventTime option",
			appName: "app1",
			userID:  "user1",
			sessID:  "session1",
			opts:    &session.Options{EventTime: time.Now().Add(-1 * time.Hour)},
			wantNil: false,
			wantErr: false,
			validate: func(t *testing.T, sess *session.Session, err error) {
				assert.NotNil(t, sess, "expected session, got nil")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := session.Key{
				AppName:   tt.appName,
				UserID:    tt.userID,
				SessionID: tt.sessID,
			}

			sess, err := service.GetSession(ctx, key, tt.opts)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.wantNil {
				assert.Nil(t, sess)
			} else {
				assert.NotNil(t, sess)
			}

			if tt.validate != nil {
				tt.validate(t, sess, err)
			}
		})
	}
}

func TestListSessions(t *testing.T) {
	tests := []struct {
		name      string
		setupData []struct {
			appName string
			userID  string
			sessID  string
		}
		listAppName string
		listUserID  string
		wantCount   int
		wantErr     bool
	}{
		{
			name: "list sessions for user with sessions",
			setupData: []struct {
				appName string
				userID  string
				sessID  string
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
				appName string
				userID  string
				sessID  string
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
			testService := NewSessionService(testServiceOpts)
			ctx := context.Background()

			// Setup test data
			for _, data := range tt.setupData {
				key := session.Key{
					AppName:   data.appName,
					UserID:    data.userID,
					SessionID: data.sessID,
				}
				_, err := testService.CreateSession(
					ctx,
					key,
					session.StateMap{"test": "data"},
					nil,
				)
				require.NoError(t, err, "setup failed")
			}

			// Test ListSessions
			userKey := session.UserKey{
				AppName: tt.listAppName,
				UserID:  tt.listUserID,
			}
			sessions, err := testService.ListSessions(ctx, userKey, nil)

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
	service := NewSessionService(testServiceOpts)
	ctx := context.Background()

	// Setup: create test session
	appName := "test-app"
	userID := "test-user"
	sessID := "test-session"

	key := session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessID,
	}
	_, err := service.CreateSession(ctx, key, session.StateMap{"test": "data"}, nil)
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
			deleteSession: sessID,
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
			deleteKey := session.Key{
				AppName:   tt.deleteAppName,
				UserID:    tt.deleteUserID,
				SessionID: tt.deleteSession,
			}
			err := service.DeleteSession(ctx, deleteKey, nil)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Verify deletion by trying to get the original session
			if tt.name == "delete existing session" {
				getKey := session.Key{
					AppName:   appName,
					UserID:    userID,
					SessionID: sessID,
				}
				sess, err := service.GetSession(ctx, getKey, nil)
				assert.NoError(t, err, "GetSession() after delete failed")

				if tt.shouldExist {
					assert.NotNil(t, sess, "session should exist after deletion")
				} else {
					assert.Nil(t, sess, "session should not exist after deletion")
				}
			}
		})
	}
}

func TestConcurrentAccess(t *testing.T) {
	service := NewSessionService(testServiceOpts)
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

					sessID := fmt.Sprintf("session-%d", id)
					state := session.StateMap{"id": id}

					key := session.Key{
						AppName:   tt.appName,
						UserID:    tt.userID,
						SessionID: sessID,
					}

					_, err := service.CreateSession(ctx, key, state, nil)
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
			userKey := session.UserKey{
				AppName: tt.appName,
				UserID:  tt.userID,
			}
			sessions, err := service.ListSessions(ctx, userKey, nil)
			require.NoError(t, err, "ListSessions() failed")
			assert.Len(t, sessions, tt.concurrency, "expected all sessions to be created")
		})
	}
}

func TestGetOrCreateApp(t *testing.T) {
	service := NewSessionService(testServiceOpts)

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
			app := service.getOrCreateAppSessions(tt.appName)

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
	service := NewSessionService(testServiceOpts)
	ctx := context.Background()

	appName := "test-app"
	userID := "test-user"
	sessID := "test-session"

	// Setup app and user state
	app := service.getOrCreateAppSessions(appName)
	app.appState["config"] = "production"
	app.appState["version"] = "1.0.0"

	if app.userState[userID] == nil {
		app.userState[userID] = make(session.StateMap)
	}
	app.userState[userID]["preference"] = "dark_mode"
	app.userState[userID]["language"] = "zh-CN"

	// Create session
	sessionState := session.StateMap{"context": "chat"}
	key := session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessID,
	}
	sess, err := service.CreateSession(ctx, key, sessionState, nil)
	require.NoError(t, err)

	// Verify session is created successfully
	assert.NotNil(t, sess, "session should be created")
	assert.Equal(t, sessID, sess.ID)
	assert.Equal(t, appName, sess.AppName)
	assert.Equal(t, userID, sess.UserID)
}

func TestAppIsolation(t *testing.T) {
	service := NewSessionService(testServiceOpts)
	ctx := context.Background()

	// Create sessions in different apps
	key1 := session.Key{
		AppName:   "app1",
		UserID:    "user1",
		SessionID: "session1",
	}
	app1Session, err := service.CreateSession(ctx, key1, session.StateMap{"key": "value1"}, nil)
	require.NoError(t, err)

	key2 := session.Key{
		AppName:   "app2",
		UserID:    "user1",
		SessionID: "session2",
	}
	app2Session, err := service.CreateSession(ctx, key2, session.StateMap{"key": "value2"}, nil)
	require.NoError(t, err)

	// Verify sessions are isolated
	assert.Equal(t, "app1", app1Session.AppName)
	assert.Equal(t, "app2", app2Session.AppName)

	// List sessions for each app should only return sessions from that app
	userKey1 := session.UserKey{AppName: "app1", UserID: "user1"}
	app1Sessions, err := service.ListSessions(ctx, userKey1, nil)
	require.NoError(t, err)
	assert.Len(t, app1Sessions, 1)

	userKey2 := session.UserKey{AppName: "app2", UserID: "user1"}
	app2Sessions, err := service.ListSessions(ctx, userKey2, nil)
	require.NoError(t, err)
	assert.Len(t, app2Sessions, 1)
}
