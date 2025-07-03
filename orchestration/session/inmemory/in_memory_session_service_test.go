package inmemory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
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
		name     string
		setup    func() (session.Key, session.StateMap)
		validate func(t *testing.T, sess *session.Session, err error, key session.Key, state session.StateMap)
	}{
		{
			name: "valid_session_creation",
			setup: func() (session.Key, session.StateMap) {
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
			setup: func() (session.Key, session.StateMap) {
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
			setup: func() (session.Key, session.StateMap) {
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
			setup: func() (session.Key, session.StateMap) {
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
			setup: func() (session.Key, session.StateMap) {
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
			service := NewSessionService()
			key, state := tt.setup()
			sess, err := service.CreateSession(context.Background(), key, state)
			tt.validate(t, sess, err, key, state)
		})
	}
}

func TestGetSession(t *testing.T) {
	// setup function to create test data for each test case
	setup := func(t *testing.T, service *SessionService) time.Time {
		ctx := context.Background()

		// Setup: create test sessions
		setupData := []struct {
			appName string
			userID  string
			sessID  string
			state   session.StateMap
		}{
			{"app1", "user1", "session1", session.StateMap{"key1": []byte("value1")}},
			{"app1", "user1", "session2", session.StateMap{"key2": []byte("value2")}},
			{"app2", "user2", "session3", session.StateMap{"key3": []byte("value3")}},
		}

		for _, data := range setupData {
			key := session.Key{
				AppName:   data.appName,
				UserID:    data.userID,
				SessionID: data.sessID,
			}
			_, err := service.CreateSession(ctx, key, data.state)
			require.NoError(t, err, "setup failed")
		}

		// Add events to session1 for options testing
		baseTime := time.Now().Add(-2 * time.Hour)
		events := []struct {
			author string
			offset time.Duration
		}{
			{"author_1", 0},
			{"author_2", 30 * time.Minute},
			{"author_3", 60 * time.Minute},
			{"author_4", 90 * time.Minute},
			{"author_5", 120 * time.Minute},
		}

		for _, e := range events {
			evt := event.New("test-invocation", e.author)
			evt.Timestamp = baseTime.Add(e.offset)
			err := service.AppendEvent(ctx, &session.Session{
				AppName: "app1",
				UserID:  "user1",
				ID:      "session1",
			}, evt)
			require.NoError(t, err)
		}

		return baseTime
	}

	tests := []struct {
		name     string
		setup    func(service *SessionService, baseTime time.Time) (*session.Session, error)
		validate func(t *testing.T, sess *session.Session, err error, baseTime time.Time)
	}{
		{
			name: "get existing session",
			setup: func(service *SessionService, baseTime time.Time) (*session.Session, error) {
				return service.GetSession(
					context.Background(),
					session.Key{AppName: "app1", UserID: "user1", SessionID: "session1"},
				)
			},
			validate: func(t *testing.T, sess *session.Session, err error, baseTime time.Time) {
				require.NoError(t, err)
				assert.NotNil(t, sess)
				assert.Equal(t, "session1", sess.ID)
				assert.Len(t, sess.Events, 5) // should have all 5 events
			},
		},
		{
			name: "get non-existent session",
			setup: func(service *SessionService, baseTime time.Time) (*session.Session, error) {
				return service.GetSession(
					context.Background(),
					session.Key{AppName: "app1", UserID: "user1", SessionID: "nonexistent"},
				)
			},
			validate: func(t *testing.T, sess *session.Session, err error, baseTime time.Time) {
				assert.NoError(t, err)
				assert.Nil(t, sess)
			},
		},
		{
			name: "get session from non-existent app",
			setup: func(service *SessionService, baseTime time.Time) (*session.Session, error) {
				return service.GetSession(
					context.Background(),
					session.Key{AppName: "nonexistent-app", UserID: "user1", SessionID: "session1"},
					session.WithEventNum(1),
				)
			},
			validate: func(t *testing.T, sess *session.Session, err error, baseTime time.Time) {
				assert.NoError(t, err)
				assert.Nil(t, sess)
			},
		},
		{
			name: "get session with EventNum option",
			setup: func(service *SessionService, baseTime time.Time) (*session.Session, error) {
				return service.GetSession(
					context.Background(),
					session.Key{AppName: "app1", UserID: "user1", SessionID: "session1"},
					session.WithEventNum(3),
				)
			},
			validate: func(t *testing.T, sess *session.Session, err error, baseTime time.Time) {
				require.NoError(t, err)
				assert.NotNil(t, sess, "expected session, got nil")
				assert.Len(t, sess.Events, 3, "should return last 3 events")
				assert.Equal(t, "author_3", sess.Events[0].Author, "first event should be author_3")
			},
		},
		{
			name: "get session with EventTime option",
			setup: func(service *SessionService, baseTime time.Time) (*session.Session, error) {
				return service.GetSession(
					context.Background(),
					session.Key{AppName: "app1", UserID: "user1", SessionID: "session1"},
					session.WithEventTime(baseTime.Add(45*time.Minute)),
				)
			},
			validate: func(t *testing.T, sess *session.Session, err error, baseTime time.Time) {
				require.NoError(t, err)
				assert.NotNil(t, sess, "expected session, got nil")
				assert.Len(t, sess.Events, 3, "should return events after 45 minutes")
				assert.Equal(t, "author_3", sess.Events[0].Author, "first event should be author_3")
			},
		},
		{
			name: "get session with both EventNum and EventTime",
			setup: func(service *SessionService, baseTime time.Time) (*session.Session, error) {
				return service.GetSession(
					context.Background(),
					session.Key{AppName: "app1", UserID: "user1", SessionID: "session1"},
					session.WithEventNum(2),
					session.WithEventTime(baseTime.Add(30*time.Minute)),
				)
			},
			validate: func(t *testing.T, sess *session.Session, err error, baseTime time.Time) {
				require.NoError(t, err)
				assert.NotNil(t, sess, "expected session, got nil")
				assert.Len(t, sess.Events, 2, "should apply both filters")
				assert.Equal(t, "author_4", sess.Events[0].Author, "first event should be author_4")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewSessionService()
			baseTime := setup(t, service)

			sess, err := tt.setup(service, baseTime)
			tt.validate(t, sess, err, baseTime)
		})
	}
}

func TestListSessions(t *testing.T) {
	// setup function to create test data for each test case
	setup := func(t *testing.T, service *SessionService, setupData []struct {
		appName string
		userID  string
		sessID  string
	}, withEvents bool) {
		ctx := context.Background()

		// Setup test data
		for _, data := range setupData {
			key := session.Key{
				AppName:   data.appName,
				UserID:    data.userID,
				SessionID: data.sessID,
			}
			_, err := service.CreateSession(
				ctx,
				key,
				session.StateMap{"test": []byte("data")},
			)
			require.NoError(t, err, "setup failed")

			// Add events to each session if testing options
			if withEvents {
				for i := 0; i < 3; i++ {
					evt := event.New("test-invocation", fmt.Sprintf("author_%s_%d", data.sessID, i))
					err := service.AppendEvent(ctx, &session.Session{
						AppName: data.appName,
						UserID:  data.userID,
						ID:      data.sessID,
					}, evt)
					require.NoError(t, err)
				}
			}
		}
	}

	tests := []struct {
		name     string
		setup    func(service *SessionService) ([]*session.Session, error)
		validate func(t *testing.T, sessions []*session.Session, err error)
	}{
		{
			name: "list sessions for user with sessions",
			setup: func(service *SessionService) ([]*session.Session, error) {
				setupData := []struct {
					appName string
					userID  string
					sessID  string
				}{
					{"app1", "user1", "session1"},
					{"app1", "user1", "session2"},
					{"app1", "user1", "session3"},
					{"app1", "user2", "session4"}, // different user
					{"app2", "user1", "session5"}, // different app
				}
				setup(t, service, setupData, false)

				userKey := session.UserKey{
					AppName: "app1",
					UserID:  "user1",
				}
				return service.ListSessions(context.Background(), userKey)
			},
			validate: func(t *testing.T, sessions []*session.Session, err error) {
				require.NoError(t, err)
				assert.Len(t, sessions, 3, "should return all sessions for user1 in app1")
			},
		},
		{
			name: "list sessions for user with no sessions",
			setup: func(service *SessionService) ([]*session.Session, error) {
				// No setup data needed
				userKey := session.UserKey{
					AppName: "nonexistent-app",
					UserID:  "nonexistent-user",
				}
				return service.ListSessions(context.Background(), userKey)
			},
			validate: func(t *testing.T, sessions []*session.Session, err error) {
				require.NoError(t, err)
				assert.Len(t, sessions, 0, "should return empty list for non-existent user")
			},
		},
		{
			name: "list sessions for different user",
			setup: func(service *SessionService) ([]*session.Session, error) {
				setupData := []struct {
					appName string
					userID  string
					sessID  string
				}{
					{"app1", "user1", "session1"},
					{"app1", "user2", "session2"},
				}
				setup(t, service, setupData, false)

				userKey := session.UserKey{
					AppName: "app1",
					UserID:  "user2",
				}
				return service.ListSessions(context.Background(), userKey)
			},
			validate: func(t *testing.T, sessions []*session.Session, err error) {
				require.NoError(t, err)
				assert.Len(t, sessions, 1, "should return only sessions for specified user")
			},
		},
		{
			name: "list sessions with EventNum option",
			setup: func(service *SessionService) ([]*session.Session, error) {
				setupData := []struct {
					appName string
					userID  string
					sessID  string
				}{
					{"app1", "user1", "session1"},
					{"app1", "user1", "session2"},
				}
				setup(t, service, setupData, true) // with events

				userKey := session.UserKey{
					AppName: "app1",
					UserID:  "user1",
				}
				return service.ListSessions(context.Background(), userKey, session.WithEventNum(2))
			},
			validate: func(t *testing.T, sessions []*session.Session, err error) {
				require.NoError(t, err)
				assert.Len(t, sessions, 2, "should return sessions with filtered events")
				// Verify each session has filtered events
				for _, sess := range sessions {
					assert.Len(t, sess.Events, 2, "each session should have filtered events")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Fresh service for each test
			service := NewSessionService()

			sessions, err := tt.setup(service)
			tt.validate(t, sessions, err)
		})
	}
}

func TestDeleteSession(t *testing.T) {
	// setup function to create test data for each test case
	setup := func(t *testing.T, service *SessionService) session.Key {
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
		_, err := service.CreateSession(ctx, key, session.StateMap{"test": []byte("data")})
		require.NoError(t, err, "setup failed")
		return key
	}

	tests := []struct {
		name     string
		setup    func(service *SessionService, originalKey session.Key) error
		validate func(t *testing.T, err error, service *SessionService, originalKey session.Key)
	}{
		{
			name: "delete existing session",
			setup: func(service *SessionService, originalKey session.Key) error {
				return service.DeleteSession(context.Background(), originalKey)
			},
			validate: func(t *testing.T, err error, service *SessionService, originalKey session.Key) {
				require.NoError(t, err)
				// Verify deletion by trying to get the original session
				sess, getErr := service.GetSession(context.Background(), originalKey)
				assert.NoError(t, getErr, "GetSession() after delete failed")
				assert.Nil(t, sess, "session should not exist after deletion")
			},
		},
		{
			name: "delete non-existent session",
			setup: func(service *SessionService, originalKey session.Key) error {
				nonExistentKey := session.Key{
					AppName:   "nonexistent-app",
					UserID:    "nonexistent-user",
					SessionID: "nonexistent-session",
				}
				return service.DeleteSession(context.Background(), nonExistentKey)
			},
			validate: func(t *testing.T, err error, service *SessionService, originalKey session.Key) {
				require.NoError(t, err)
				// Original session should still exist
				sess, getErr := service.GetSession(context.Background(), originalKey)
				assert.NoError(t, getErr, "GetSession() should succeed")
				assert.NotNil(t, sess, "original session should still exist")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewSessionService()
			originalKey := setup(t, service)

			err := tt.setup(service, originalKey)
			tt.validate(t, err, service, originalKey)
		})
	}
}

func TestConcurrentAccess(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(service *SessionService) ([]*session.Session, error)
		validate func(t *testing.T, sessions []*session.Session, err error, expectedCount int)
	}{
		{
			name: "concurrent session creation",
			setup: func(service *SessionService) ([]*session.Session, error) {
				ctx := context.Background()
				concurrency := 10
				appName := "test-app"
				userID := "test-user"

				done := make(chan bool, concurrency)
				errors := make(chan error, concurrency)

				// Launch concurrent operations
				for i := 0; i < concurrency; i++ {
					go func(id int) {
						defer func() { done <- true }()

						sessID := fmt.Sprintf("session-%d", id)
						state := session.StateMap{"id": []byte(fmt.Sprintf("%d", id))}

						key := session.Key{
							AppName:   appName,
							UserID:    userID,
							SessionID: sessID,
						}

						_, err := service.CreateSession(ctx, key, state)
						if err != nil {
							errors <- err
							return
						}
					}(i)
				}

				// Wait for all operations to complete
				for i := 0; i < concurrency; i++ {
					<-done
				}

				// Check for errors
				close(errors)
				for err := range errors {
					if err != nil {
						return nil, err
					}
				}

				// Verify all sessions were created
				userKey := session.UserKey{
					AppName: appName,
					UserID:  userID,
				}
				return service.ListSessions(ctx, userKey)
			},
			validate: func(t *testing.T, sessions []*session.Session, err error, expectedCount int) {
				require.NoError(t, err, "concurrent operation failed")
				assert.Len(t, sessions, expectedCount, "expected all sessions to be created")
			},
		},
		{
			name: "concurrent app creation",
			setup: func(service *SessionService) ([]*session.Session, error) {
				ctx := context.Background()
				concurrency := 5
				appName := "concurrent-app"
				userID := "test-user"

				done := make(chan bool, concurrency)
				errors := make(chan error, concurrency)

				// Launch concurrent operations
				for i := 0; i < concurrency; i++ {
					go func(id int) {
						defer func() { done <- true }()

						sessID := fmt.Sprintf("session-%d", id)
						state := session.StateMap{"id": []byte(fmt.Sprintf("%d", id))}

						key := session.Key{
							AppName:   appName,
							UserID:    userID,
							SessionID: sessID,
						}

						_, err := service.CreateSession(ctx, key, state)
						if err != nil {
							errors <- err
							return
						}
					}(i)
				}

				// Wait for all operations to complete
				for i := 0; i < concurrency; i++ {
					<-done
				}

				// Check for errors
				close(errors)
				for err := range errors {
					if err != nil {
						return nil, err
					}
				}

				// Verify all sessions were created
				userKey := session.UserKey{
					AppName: appName,
					UserID:  userID,
				}
				return service.ListSessions(ctx, userKey)
			},
			validate: func(t *testing.T, sessions []*session.Session, err error, expectedCount int) {
				require.NoError(t, err, "concurrent operation failed")
				assert.Len(t, sessions, expectedCount, "expected all sessions to be created")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewSessionService()
			var expectedCount int
			if tt.name == "concurrent session creation" {
				expectedCount = 10
			} else {
				expectedCount = 5
			}

			sessions, err := tt.setup(service)
			tt.validate(t, sessions, err, expectedCount)
		})
	}
}

func TestGetOrCreateApp(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(service *SessionService) *appSessions
		validate func(t *testing.T, app *appSessions)
	}{
		{
			name: "create app for new application",
			setup: func(service *SessionService) *appSessions {
				return service.getOrCreateAppSessions("new-app")
			},
			validate: func(t *testing.T, app *appSessions) {
				assert.NotNil(t, app, "app should be created")
				if app != nil {
					// Verify app is properly initialized
					assert.NotNil(t, app.sessions, "app sessions map should be initialized")
					assert.NotNil(t, app.userState, "app userState map should be initialized")
					assert.NotNil(t, app.appState, "app appState map should be initialized")
				}
			},
		},
		{
			name: "get existing app",
			setup: func(service *SessionService) *appSessions {
				// First create the app
				service.getOrCreateAppSessions("existing-app")
				// Then get it again
				return service.getOrCreateAppSessions("existing-app")
			},
			validate: func(t *testing.T, app *appSessions) {
				assert.NotNil(t, app, "app should be retrieved")
				if app != nil {
					// Verify app is properly initialized
					assert.NotNil(t, app.sessions, "app sessions map should be initialized")
					assert.NotNil(t, app.userState, "app userState map should be initialized")
					assert.NotNil(t, app.appState, "app appState map should be initialized")
				}
			},
		},
		{
			name: "create app for another application",
			setup: func(service *SessionService) *appSessions {
				return service.getOrCreateAppSessions("another-app")
			},
			validate: func(t *testing.T, app *appSessions) {
				assert.NotNil(t, app, "app should be created")
				if app != nil {
					// Verify app is properly initialized
					assert.NotNil(t, app.sessions, "app sessions map should be initialized")
					assert.NotNil(t, app.userState, "app userState map should be initialized")
					assert.NotNil(t, app.appState, "app appState map should be initialized")
				}
			},
		},
		{
			name: "get default app",
			setup: func(service *SessionService) *appSessions {
				return service.getOrCreateAppSessions("")
			},
			validate: func(t *testing.T, app *appSessions) {
				assert.NotNil(t, app, "default app should be created")
				if app != nil {
					// Verify app is properly initialized
					assert.NotNil(t, app.sessions, "app sessions map should be initialized")
					assert.NotNil(t, app.userState, "app userState map should be initialized")
					assert.NotNil(t, app.appState, "app appState map should be initialized")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewSessionService()

			app := tt.setup(service)
			tt.validate(t, app)
		})
	}
}

// Additional tests for edge cases and State functionality
func TestStateMerging(t *testing.T) {
	service := NewSessionService()
	ctx := context.Background()
	appName := "test-app"
	userID := "test-user"
	sessID := "test-session"

	// Setup app and user state
	app := service.getOrCreateAppSessions(appName)
	app.appState["config"] = []byte("production")
	app.appState["version"] = []byte("1.0.0")

	if app.userState[userID] == nil {
		app.userState[userID] = make(session.StateMap)
	}
	app.userState[userID]["preference"] = []byte("dark_mode")
	app.userState[userID]["language"] = []byte("zh-CN")

	// Create session
	sessionState := session.StateMap{"context": []byte("chat")}
	key := session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessID,
	}
	sess, err := service.CreateSession(ctx, key, sessionState)

	require.NoError(t, err)
	assert.NotNil(t, sess, "session should be created")
	assert.Equal(t, "test-session", sess.ID)
	assert.Equal(t, "test-app", sess.AppName)
	assert.Equal(t, "test-user", sess.UserID)
}

func TestAppIsolation(t *testing.T) {
	service := NewSessionService()
	ctx := context.Background()

	// Create sessions in different apps
	key1 := session.Key{
		AppName:   "app1",
		UserID:    "user1",
		SessionID: "session1",
	}
	app1Session, err := service.CreateSession(ctx, key1, session.StateMap{"key": []byte("value1")})
	require.NoError(t, err)

	key2 := session.Key{
		AppName:   "app2",
		UserID:    "user1",
		SessionID: "session2",
	}
	app2Session, err := service.CreateSession(ctx, key2, session.StateMap{"key": []byte("value2")})
	require.NoError(t, err)

	// Verify sessions are isolated
	assert.Equal(t, "app1", app1Session.AppName)
	assert.Equal(t, "app2", app2Session.AppName)

	// List sessions for each app should only return sessions from that app
	userKey1 := session.UserKey{AppName: "app1", UserID: "user1"}
	app1Sessions, err := service.ListSessions(ctx, userKey1)
	require.NoError(t, err)

	userKey2 := session.UserKey{AppName: "app2", UserID: "user1"}
	app2Sessions, err := service.ListSessions(ctx, userKey2)
	require.NoError(t, err)

	assert.Len(t, app1Sessions, 1, "app1 should have 1 session")
	assert.Len(t, app2Sessions, 1, "app2 should have 1 session")

	if len(app1Sessions) > 0 {
		assert.Equal(t, "app1", app1Sessions[0].AppName)
	}
	if len(app2Sessions) > 0 {
		assert.Equal(t, "app2", app2Sessions[0].AppName)
	}
}
