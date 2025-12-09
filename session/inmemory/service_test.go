//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
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
			defer service.Close()
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
			// Add Response field to make events valid for filtering
			evt.Response = &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleUser, // All test events are from user for simplicity
							Content: fmt.Sprintf("Test message from %s", e.author),
						},
					},
				},
			}
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
			defer service.Close()
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
					// Add Response field to make events valid for filtering
					evt.Response = &model.Response{
						Choices: []model.Choice{
							{
								Message: model.Message{
									Role:    model.RoleUser, // All test events are from user for simplicity
									Content: fmt.Sprintf("Test message from author_%s_%d", data.sessID, i),
								},
							},
						},
					}
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
			defer service.Close()

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
			defer service.Close()
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
			defer service.Close()
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
			defer service.Close()

			app := tt.setup(service)
			tt.validate(t, app)
		})
	}
}

// Additional tests for edge cases and State functionality
func TestStateMerging(t *testing.T) {
	service := NewSessionService()
	defer service.Close()
	ctx := context.Background()
	appName := "test-app"
	userID := "test-user"
	sessID := "test-session"

	// Setup app and user state
	app := service.getOrCreateAppSessions(appName)
	app.appState.data["config"] = []byte("production")
	app.appState.data["version"] = []byte("1.0.0")

	if app.userState[userID] == nil {
		app.userState[userID] = &stateWithTTL{
			data: make(session.StateMap),
		}
	}
	app.userState[userID].data["preference"] = []byte("dark_mode")
	app.userState[userID].data["language"] = []byte("zh-CN")

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
	service := NewSessionService()
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
	service := NewSessionService()
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

func TestSessionServiceAppendTrackEvent(t *testing.T) {
	service := NewSessionService()
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName:   "track-app",
		UserID:    "track-user",
		SessionID: "track-session",
	}
	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	eventA := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"a1"`),
		Timestamp: time.Now(),
	}
	err = service.AppendTrackEvent(ctx, sess, eventA)
	require.NoError(t, err)

	app, ok := service.getAppSessions(key.AppName)
	require.True(t, ok)

	app.mu.RLock()
	stored := app.sessions[key.UserID][key.SessionID]
	require.NotNil(t, stored)
	storedSess := stored.session
	app.mu.RUnlock()

	require.NotNil(t, storedSess.Tracks)
	require.Contains(t, storedSess.Tracks, session.Track("alpha"))
	require.Len(t, storedSess.Tracks["alpha"].Events, 1)
	assert.Equal(t, json.RawMessage(`"a1"`), storedSess.Tracks["alpha"].Events[0].Payload)

	sessTracks, err := session.TracksFromState(storedSess.State)
	require.NoError(t, err)
	assert.Equal(t, []session.Track{"alpha"}, sessTracks)

	eventB := &session.TrackEvent{
		Track:     "beta",
		Payload:   json.RawMessage(`"b1"`),
		Timestamp: time.Now(),
	}
	err = service.AppendTrackEvent(ctx, sess, eventB)
	require.NoError(t, err)

	app.mu.RLock()
	storedSess = app.sessions[key.UserID][key.SessionID].session
	app.mu.RUnlock()
	require.Len(t, storedSess.Tracks["beta"].Events, 1)
	assert.Equal(t, json.RawMessage(`"b1"`), storedSess.Tracks["beta"].Events[0].Payload)

	allTracks, err := session.TracksFromState(storedSess.State)
	require.NoError(t, err)
	assert.Equal(t, []session.Track{"alpha", "beta"}, allTracks)
}

func TestSessionServiceAppendTrackEventErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("session append failure surfaced", func(t *testing.T) {
		service := NewSessionService()
		defer service.Close()

		sess := &session.Session{
			ID:      "s1",
			AppName: "app",
			UserID:  "user",
			State: session.StateMap{
				"tracks": []byte("{"),
			},
		}
		err := service.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "alpha"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "append track event")
	})

	t.Run("invalid session key", func(t *testing.T) {
		service := NewSessionService()
		defer service.Close()

		sess := &session.Session{
			ID:     "s1",
			UserID: "user",
			State:  make(session.StateMap),
		}
		err := service.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "alpha"})
		require.Error(t, err)
		assert.ErrorIs(t, err, session.ErrAppNameRequired)
	})

	t.Run("app not found", func(t *testing.T) {
		service := NewSessionService()
		defer service.Close()

		sess := &session.Session{
			ID:      "s1",
			AppName: "missing-app",
			UserID:  "user",
			State:   make(session.StateMap),
		}
		err := service.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "alpha"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "app not found")
	})

	t.Run("user not found", func(t *testing.T) {
		service := NewSessionService()
		defer service.Close()

		app := service.getOrCreateAppSessions("app")
		app.mu.Lock()
		app.sessions["other-user"] = make(map[string]*sessionWithTTL)
		app.mu.Unlock()

		sess := &session.Session{
			ID:      "s1",
			AppName: "app",
			UserID:  "user",
			State:   make(session.StateMap),
		}
		err := service.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "alpha"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "user not found")
	})

	t.Run("session not found", func(t *testing.T) {
		service := NewSessionService()
		defer service.Close()

		app := service.getOrCreateAppSessions("app")
		app.mu.Lock()
		app.sessions["user"] = map[string]*sessionWithTTL{}
		app.mu.Unlock()

		sess := &session.Session{
			ID:      "missing-session",
			AppName: "app",
			UserID:  "user",
			State:   make(session.StateMap),
		}
		err := service.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "alpha"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})

	t.Run("session expired", func(t *testing.T) {
		service := NewSessionService(WithSessionTTL(time.Second))
		defer service.Close()

		app := service.getOrCreateAppSessions("app")
		app.mu.Lock()
		app.sessions["user"] = map[string]*sessionWithTTL{
			"expired-session": {
				session: &session.Session{
					ID:      "expired-session",
					AppName: "app",
					UserID:  "user",
					State:   make(session.StateMap),
				},
				expiredAt: time.Now().Add(-time.Minute),
			},
		}
		app.mu.Unlock()

		sess := &session.Session{
			ID:      "expired-session",
			AppName: "app",
			UserID:  "user",
			State:   make(session.StateMap),
		}
		err := service.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "alpha"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "session expired")
	})
}

func TestCopySessionTracks(t *testing.T) {
	now := time.Now()
	original := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		State: session.StateMap{
			"key": []byte("v"),
		},
		Events: []event.Event{
			{ID: "evt"},
		},
		Tracks: map[session.Track]*session.TrackEvents{
			"alpha": {
				Track: "alpha",
				Events: []session.TrackEvent{
					{
						Track:     "alpha",
						Payload:   json.RawMessage(`"payload"`),
						Timestamp: now,
					},
				},
			},
		},
		Summaries: map[string]*session.Summary{
			session.SummaryFilterKeyAllContents: {
				Summary: "original",
			},
		},
		UpdatedAt: now,
		CreatedAt: now,
	}

	copied := original.Clone()
	require.NotNil(t, copied)

	require.NotSame(t, original, copied)
	require.NotSame(t, original.Tracks["alpha"], copied.Tracks["alpha"])
	require.Len(t, copied.Tracks["alpha"].Events, 1)

	copied.Tracks["alpha"].Events[0].Payload = json.RawMessage(`"changed"`)
	assert.Equal(t, json.RawMessage(`"payload"`), original.Tracks["alpha"].Events[0].Payload)

	original.Tracks["alpha"].Events[0].Payload = json.RawMessage(`"mutated"`)
	assert.Equal(t, json.RawMessage(`"changed"`), copied.Tracks["alpha"].Events[0].Payload)

	copied.State["key"][0] = 'x'
	assert.Equal(t, byte('v'), original.State["key"][0])

	copied.Events[0].ID = "copied"
	assert.Equal(t, "evt", original.Events[0].ID)

	copied.Summaries[session.SummaryFilterKeyAllContents].Summary = "copy"
	assert.Equal(t, "original", original.Summaries[session.SummaryFilterKeyAllContents].Summary)
}

func TestCopySessionWithoutTracks(t *testing.T) {
	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		State: session.StateMap{
			"foo": []byte("bar"),
		},
	}

	copied := sess.Clone()
	require.NotNil(t, copied)
	assert.Nil(t, copied.Tracks)
	copied.State["foo"][0] = 'x'
	assert.Equal(t, byte('b'), sess.State["foo"][0])
}

func TestAppendEventHook(t *testing.T) {
	t.Run("hook modifies event before storage", func(t *testing.T) {
		hookCalled := false
		service := NewSessionService(
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				hookCalled = true
				ctx.Event.Tag = "hook_processed"
				return next()
			}),
		)
		defer service.Close()

		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
		sess, err := service.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)

		// First add a user message
		userEvt := event.New("inv0", "user")
		userEvt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "Hello"}}},
		}
		err = service.AppendEvent(context.Background(), sess, userEvt)
		require.NoError(t, err)

		// Then add assistant message
		evt := event.New("inv1", "assistant")
		evt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Hi there"}}},
		}

		err = service.AppendEvent(context.Background(), sess, evt)
		require.NoError(t, err)
		assert.True(t, hookCalled)

		retrieved, err := service.GetSession(context.Background(), key)
		require.NoError(t, err)
		require.Len(t, retrieved.Events, 2)

		// Check the assistant event has the metadata
		assert.Equal(t, "hook_processed", retrieved.Events[1].Tag)
	})

	t.Run("hook can abort event storage", func(t *testing.T) {
		service := NewSessionService(
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				return nil // skip next()
			}),
		)
		defer service.Close()

		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
		sess, err := service.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)

		evt := event.New("inv1", "assistant")
		evt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Hello"}}},
		}

		err = service.AppendEvent(context.Background(), sess, evt)
		require.NoError(t, err)

		retrieved, err := service.GetSession(context.Background(), key)
		require.NoError(t, err)
		assert.Len(t, retrieved.Events, 0)
	})

	t.Run("multiple hooks execute in order", func(t *testing.T) {
		order := []string{}
		service := NewSessionService(
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
		defer service.Close()

		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
		sess, err := service.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)

		evt := event.New("inv1", "assistant")
		evt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Hello"}}},
		}

		err = service.AppendEvent(context.Background(), sess, evt)
		require.NoError(t, err)

		assert.Equal(t, []string{"hook1_before", "hook2_before", "hook2_after", "hook1_after"}, order)
	})
}

func TestGetSessionHook(t *testing.T) {
	t.Run("hook modifies session after retrieval", func(t *testing.T) {
		hookCalled := false
		service := NewSessionService(
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
		defer service.Close()

		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
		_, err := service.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)

		retrieved, err := service.GetSession(context.Background(), key)
		require.NoError(t, err)
		assert.True(t, hookCalled)
		assert.Equal(t, []byte("true"), retrieved.State["hook_added"])
	})

	t.Run("hook can filter events", func(t *testing.T) {
		service := NewSessionService(
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
		defer service.Close()

		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
		sess, err := service.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)

		evt1 := event.New("inv1", "user")
		evt1.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "Q1"}}},
		}
		evt1.Tag = "skip"
		err = service.AppendEvent(context.Background(), sess, evt1)
		require.NoError(t, err)

		evt2 := event.New("inv2", "assistant")
		evt2.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "A1"}}},
		}
		err = service.AppendEvent(context.Background(), sess, evt2)
		require.NoError(t, err)

		retrieved, err := service.GetSession(context.Background(), key)
		require.NoError(t, err)
		assert.Len(t, retrieved.Events, 1)
		assert.Equal(t, "A1", retrieved.Events[0].Response.Choices[0].Message.Content)
	})

	t.Run("multiple hooks execute in order", func(t *testing.T) {
		order := []string{}
		service := NewSessionService(
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
		defer service.Close()

		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
		_, err := service.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)

		_, err = service.GetSession(context.Background(), key)
		require.NoError(t, err)

		assert.Equal(t, []string{"hook1_before", "hook2_before", "hook2_after", "hook1_after"}, order)
	})
}
