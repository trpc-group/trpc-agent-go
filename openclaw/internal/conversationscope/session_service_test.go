//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package conversationscope

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

type recordingSessionService struct {
	createKey           session.Key
	createSession       *session.Session
	createErr           error
	forceNilCreate      bool
	getKey              session.Key
	getSession          *session.Session
	getErr              error
	forceNilGet         bool
	listSessionsKey     session.UserKey
	listSessions        []*session.Session
	listSessionsErr     error
	deleteKey           session.Key
	updateAppName       string
	updateAppState      session.StateMap
	deleteAppName       string
	deleteAppKey        string
	listAppName         string
	updateUserKey       session.UserKey
	updateUserState     session.StateMap
	updateUserStateErr  error
	listUserKey         session.UserKey
	listUserStates      session.StateMap
	listUserStatesErr   error
	deleteUserKey       session.UserKey
	deleteUserStateKey  string
	deleteUserStateErr  error
	updateSessionKey    session.Key
	updateSessionState  session.StateMap
	appendSession       *session.Session
	appendEvent         *event.Event
	createSummarySess   *session.Session
	createSummaryKey    string
	createSummaryForce  bool
	enqueueSummarySess  *session.Session
	enqueueSummaryKey   string
	enqueueSummaryForce bool
	getSummarySess      *session.Session
	getSummaryText      string
	getSummaryOK        bool
	closeCalls          int
}

func (r *recordingSessionService) CreateSession(
	_ context.Context,
	key session.Key,
	_ session.StateMap,
	_ ...session.Option,
) (*session.Session, error) {
	r.createKey = key
	if r.createErr != nil {
		return nil, r.createErr
	}
	if r.createSession != nil {
		return r.createSession, nil
	}
	if r.forceNilCreate {
		return nil, nil
	}
	return &session.Session{ID: key.SessionID, UserID: key.UserID}, nil
}

func (r *recordingSessionService) GetSession(
	_ context.Context,
	key session.Key,
	_ ...session.Option,
) (*session.Session, error) {
	r.getKey = key
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.getSession != nil {
		return r.getSession, nil
	}
	if r.forceNilGet {
		return nil, nil
	}
	return &session.Session{ID: key.SessionID, UserID: key.UserID}, nil
}

func (r *recordingSessionService) ListSessions(
	_ context.Context,
	userKey session.UserKey,
	_ ...session.Option,
) ([]*session.Session, error) {
	r.listSessionsKey = userKey
	if r.listSessionsErr != nil {
		return nil, r.listSessionsErr
	}
	if r.listSessions != nil {
		return r.listSessions, nil
	}
	return []*session.Session{{ID: "sess-1", UserID: userKey.UserID}}, nil
}

func (r *recordingSessionService) DeleteSession(
	_ context.Context,
	key session.Key,
	_ ...session.Option,
) error {
	r.deleteKey = key
	return nil
}

func (r *recordingSessionService) UpdateAppState(
	_ context.Context,
	appName string,
	state session.StateMap,
) error {
	r.updateAppName = appName
	r.updateAppState = state
	return nil
}

func (r *recordingSessionService) DeleteAppState(
	_ context.Context,
	appName string,
	key string,
) error {
	r.deleteAppName = appName
	r.deleteAppKey = key
	return nil
}

func (r *recordingSessionService) ListAppStates(
	_ context.Context,
	appName string,
) (session.StateMap, error) {
	r.listAppName = appName
	return session.StateMap{"a": []byte("b")}, nil
}

func (r *recordingSessionService) UpdateUserState(
	_ context.Context,
	userKey session.UserKey,
	state session.StateMap,
) error {
	r.updateUserKey = userKey
	r.updateUserState = state
	if r.updateUserStateErr != nil {
		return r.updateUserStateErr
	}
	return nil
}

func (r *recordingSessionService) ListUserStates(
	_ context.Context,
	userKey session.UserKey,
) (session.StateMap, error) {
	r.listUserKey = userKey
	if r.listUserStatesErr != nil {
		return nil, r.listUserStatesErr
	}
	return r.listUserStates, nil
}

func (r *recordingSessionService) DeleteUserState(
	_ context.Context,
	userKey session.UserKey,
	key string,
) error {
	r.deleteUserKey = userKey
	r.deleteUserStateKey = key
	return r.deleteUserStateErr
}

func (r *recordingSessionService) UpdateSessionState(
	_ context.Context,
	key session.Key,
	state session.StateMap,
) error {
	r.updateSessionKey = key
	r.updateSessionState = state
	return nil
}

func (r *recordingSessionService) AppendEvent(
	_ context.Context,
	sess *session.Session,
	evt *event.Event,
	_ ...session.Option,
) error {
	r.appendSession = sess
	r.appendEvent = evt
	return nil
}

func (r *recordingSessionService) CreateSessionSummary(
	_ context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	r.createSummarySess = sess
	r.createSummaryKey = filterKey
	r.createSummaryForce = force
	return nil
}

func (r *recordingSessionService) EnqueueSummaryJob(
	_ context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	r.enqueueSummarySess = sess
	r.enqueueSummaryKey = filterKey
	r.enqueueSummaryForce = force
	return nil
}

func (r *recordingSessionService) GetSessionSummaryText(
	_ context.Context,
	sess *session.Session,
	_ ...session.SummaryOption,
) (string, bool) {
	r.getSummarySess = sess
	return r.getSummaryText, r.getSummaryOK
}

func (r *recordingSessionService) Close() error {
	r.closeCalls++
	return nil
}

func TestResolveStorageUserID(t *testing.T) {
	t.Parallel()

	extensions, err := conversation.MergeRequestExtension(
		nil,
		conversation.Annotation{
			StorageUserID: "chat-scope",
		},
	)
	require.NoError(t, err)

	userID, err := ResolveStorageUserID(extensions, "canonical-user")
	require.NoError(t, err)
	require.Equal(t, "chat-scope", userID)

	userID, err = ResolveStorageUserID(nil, "canonical-user")
	require.NoError(t, err)
	require.Equal(t, "canonical-user", userID)

	userID, err = ResolveStorageUserID(
		map[string]json.RawMessage{
			conversation.ExtensionKey: json.RawMessage("{"),
		},
		" canonical-user ",
	)
	require.Error(t, err)
	require.Equal(t, "canonical-user", userID)
}

func TestWithStorageUserIDAndContextFallbacks(t *testing.T) {
	t.Parallel()

	ctx := WithStorageUserID(context.Background(), " chat-scope ")
	require.Equal(t, "chat-scope", StorageUserIDFromContext(ctx, "fallback"))

	base := context.Background()
	require.True(t, WithStorageUserID(base, "   ") == base)
	require.Equal(t, "fallback", StorageUserIDFromContext(base, " fallback "))
}

func TestWrapSessionService_UsesContextStorageScopeForStorage(t *testing.T) {
	t.Parallel()

	base := sessioninmemory.NewSessionService()
	wrapped := WrapSessionService(base)
	storageCtx := WithStorageUserID(context.Background(), "chat-scope")

	storageKey := session.Key{
		AppName:   "demo-app",
		UserID:    "chat-scope",
		SessionID: "demo:thread:room-1",
	}
	stored, err := base.CreateSession(
		context.Background(),
		storageKey,
		session.StateMap{"scope": []byte("chat")},
	)
	require.NoError(t, err)
	require.NoError(
		t,
		base.AppendEvent(
			context.Background(),
			stored,
			event.NewResponseEvent(
				"inv-1",
				"user",
				&model.Response{
					Choices: []model.Choice{{
						Message: model.NewUserMessage("hello"),
					}},
				},
			),
		),
	)

	requestKey := session.Key{
		AppName:   "demo-app",
		UserID:    "canonical-user",
		SessionID: storageKey.SessionID,
	}
	sess, err := wrapped.GetSession(storageCtx, requestKey)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Equal(t, "canonical-user", sess.UserID)
	require.Equal(t, storageKey.SessionID, sess.ID)
	require.Len(t, sess.Events, 1)

	require.NoError(
		t,
		wrapped.UpdateSessionState(
			storageCtx,
			requestKey,
			session.StateMap{"migrated": []byte("yes")},
		),
	)
	require.NoError(
		t,
		wrapped.AppendEvent(
			storageCtx,
			sess,
			event.NewResponseEvent(
				"inv-2",
				"assistant",
				&model.Response{
					Choices: []model.Choice{{
						Message: model.NewAssistantMessage("ok"),
					}},
				},
			),
		),
	)

	updated, err := base.GetSession(context.Background(), storageKey)
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, "chat-scope", updated.UserID)
	require.Len(t, updated.Events, 2)
	raw, ok := updated.GetState("migrated")
	require.True(t, ok)
	require.Equal(t, []byte("yes"), raw)
}

func TestWrapSessionService_WithoutStorageOverrideKeepsCanonicalUser(t *testing.T) {
	t.Parallel()

	base := sessioninmemory.NewSessionService()
	wrapped := WrapSessionService(base)

	key := session.Key{
		AppName:   "demo-app",
		UserID:    "canonical-user",
		SessionID: "demo:thread:room-1",
	}
	_, err := wrapped.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	sess, err := base.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Equal(t, "canonical-user", sess.UserID)
}

func TestIndexedStorageUsersLifecycle(t *testing.T) {
	t.Parallel()

	svc := sessioninmemory.NewSessionService()

	require.NoError(
		t,
		RememberIndexedStorageUser(
			context.Background(),
			svc,
			"demo-app",
			"canonical-user",
			"chat-scope",
		),
	)
	require.NoError(
		t,
		RememberIndexedStorageUser(
			context.Background(),
			svc,
			"demo-app",
			"canonical-user",
			"chat-scope-2",
		),
	)

	storageUsers, err := ListIndexedStorageUsers(
		context.Background(),
		svc,
		"demo-app",
		"canonical-user",
	)
	require.NoError(t, err)
	require.Equal(t, []string{"chat-scope", "chat-scope-2"}, storageUsers)

	require.NoError(
		t,
		DeleteIndexedStorageUser(
			context.Background(),
			svc,
			"demo-app",
			"canonical-user",
			"chat-scope",
		),
	)
	storageUsers, err = ListIndexedStorageUsers(
		context.Background(),
		svc,
		"demo-app",
		"canonical-user",
	)
	require.NoError(t, err)
	require.Equal(t, []string{"chat-scope-2"}, storageUsers)
}

func TestIndexedStorageUsersHelpers_EdgeCases(t *testing.T) {
	t.Parallel()

	require.NoError(
		t,
		RememberIndexedStorageUser(
			context.Background(),
			nil,
			"demo-app",
			"canonical-user",
			"chat-scope",
		),
	)
	require.NoError(
		t,
		RememberIndexedStorageUser(
			context.Background(),
			sessioninmemory.NewSessionService(),
			" ",
			"canonical-user",
			"chat-scope",
		),
	)
	require.NoError(
		t,
		DeleteIndexedStorageUser(
			context.Background(),
			nil,
			"demo-app",
			"canonical-user",
			"chat-scope",
		),
	)

	rec := &recordingSessionService{
		listUserStatesErr:  errors.New("list boom"),
		deleteUserStateErr: errors.New("delete boom"),
	}
	storageUsers, err := ListIndexedStorageUsers(
		context.Background(),
		nil,
		"demo-app",
		"canonical-user",
	)
	require.NoError(t, err)
	require.Nil(t, storageUsers)

	storageUsers, err = ListIndexedStorageUsers(
		context.Background(),
		sessioninmemory.NewSessionService(),
		" ",
		"canonical-user",
	)
	require.NoError(t, err)
	require.Nil(t, storageUsers)

	_, err = ListIndexedStorageUsers(
		context.Background(),
		rec,
		"demo-app",
		"canonical-user",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "list boom")

	err = DeleteIndexedStorageUser(
		context.Background(),
		rec,
		"demo-app",
		"canonical-user",
		"chat-scope",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete boom")

	require.Nil(t, indexedStorageUsersFromState(nil))
	require.Equal(
		t,
		[]string{"a", "z"},
		indexedStorageUsersFromState(session.StateMap{
			storageUserStatePrefix + " z ": []byte("1"),
			storageUserStatePrefix + "a":   []byte("1"),
			storageUserStatePrefix + " a ": []byte("1"),
			storageUserStatePrefix + "b":   nil,
			"other":                        []byte("1"),
		}),
	)
	require.Equal(t, "", storageUserStateKey("   "))
}

func TestWrapSessionService_DelegatesAdministrativeMethods(t *testing.T) {
	t.Parallel()

	rec := &recordingSessionService{
		getSummaryText: "summary",
		getSummaryOK:   true,
		listSessions: []*session.Session{{
			ID:     "sess-1",
			UserID: "chat-scope",
		}},
	}
	wrapped := WrapSessionService(rec)
	ctx := WithStorageUserID(context.Background(), "chat-scope")

	listed, err := wrapped.ListSessions(
		ctx,
		session.UserKey{AppName: "demo-app", UserID: "canonical-user"},
	)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, "chat-scope", rec.listSessionsKey.UserID)
	require.Equal(t, "canonical-user", listed[0].UserID)

	require.NoError(
		t,
		wrapped.DeleteSession(
			ctx,
			session.Key{
				AppName:   "demo-app",
				UserID:    "canonical-user",
				SessionID: "sess-1",
			},
		),
	)
	require.Equal(t, "chat-scope", rec.deleteKey.UserID)

	require.NoError(
		t,
		wrapped.UpdateAppState(
			ctx,
			"demo-app",
			session.StateMap{"a": []byte("b")},
		),
	)
	require.Equal(t, "demo-app", rec.updateAppName)

	require.NoError(t, wrapped.DeleteAppState(ctx, "demo-app", "a"))
	require.Equal(t, "demo-app", rec.deleteAppName)
	require.Equal(t, "a", rec.deleteAppKey)

	appStates, err := wrapped.ListAppStates(ctx, "demo-app")
	require.NoError(t, err)
	require.Equal(t, []byte("b"), appStates["a"])
	require.Equal(t, "demo-app", rec.listAppName)

	require.NoError(
		t,
		wrapped.UpdateUserState(
			ctx,
			session.UserKey{AppName: "demo-app", UserID: "canonical-user"},
			session.StateMap{"u": []byte("1")},
		),
	)
	require.Equal(t, "canonical-user", rec.updateUserKey.UserID)

	rec.listUserStates = session.StateMap{"k": []byte("v")}
	userStates, err := wrapped.ListUserStates(
		ctx,
		session.UserKey{AppName: "demo-app", UserID: "canonical-user"},
	)
	require.NoError(t, err)
	require.Equal(t, []byte("v"), userStates["k"])
	require.Equal(t, "canonical-user", rec.listUserKey.UserID)

	require.NoError(
		t,
		wrapped.DeleteUserState(
			ctx,
			session.UserKey{AppName: "demo-app", UserID: "canonical-user"},
			"k",
		),
	)
	require.Equal(t, "canonical-user", rec.deleteUserKey.UserID)
	require.Equal(t, "k", rec.deleteUserStateKey)

	userEvt := event.New("inv", "user")
	require.NoError(
		t,
		wrapped.CreateSessionSummary(
			ctx,
			&session.Session{ID: "sess-1", UserID: "canonical-user"},
			"branch",
			true,
		),
	)
	require.Equal(t, "chat-scope", rec.createSummarySess.UserID)
	require.Equal(t, "branch", rec.createSummaryKey)
	require.True(t, rec.createSummaryForce)

	require.NoError(
		t,
		wrapped.EnqueueSummaryJob(
			ctx,
			&session.Session{ID: "sess-1", UserID: "canonical-user"},
			"branch",
			true,
		),
	)
	require.Equal(t, "chat-scope", rec.enqueueSummarySess.UserID)
	require.Equal(t, "branch", rec.enqueueSummaryKey)
	require.True(t, rec.enqueueSummaryForce)

	require.NoError(
		t,
		wrapped.AppendEvent(
			ctx,
			&session.Session{ID: "sess-1", UserID: "canonical-user"},
			userEvt,
		),
	)
	require.Equal(t, "chat-scope", rec.appendSession.UserID)
	require.Same(t, userEvt, rec.appendEvent)

	summaryText, ok := wrapped.GetSessionSummaryText(
		ctx,
		&session.Session{ID: "sess-1", UserID: "canonical-user"},
	)
	require.True(t, ok)
	require.Equal(t, "summary", summaryText)
	require.Equal(t, "chat-scope", rec.getSummarySess.UserID)

	require.NoError(t, wrapped.Close())
	require.Equal(t, 1, rec.closeCalls)
}

func TestWrapSessionService_HelperEdgeCases(t *testing.T) {
	t.Parallel()

	require.Nil(t, WrapSessionService(nil))
	require.Nil(t, rewriteSessionForStorage(context.Background(), nil))
	require.Nil(t, rewriteSessionForUser(nil, "user"))

	sess := &session.Session{ID: "sess-1", UserID: "chat-scope"}
	require.Same(
		t,
		sess,
		rewriteSessionForStorage(
			WithStorageUserID(context.Background(), "chat-scope"),
			sess,
		),
	)
}

func TestWrapSessionService_CreateAndGet_EarlyReturnPaths(t *testing.T) {
	t.Parallel()

	createRec := &recordingSessionService{
		createErr: errors.New("create boom"),
	}
	wrappedCreate := WrapSessionService(createRec)
	sess, err := wrappedCreate.CreateSession(
		WithStorageUserID(context.Background(), "chat-scope"),
		session.Key{
			AppName:   "demo-app",
			UserID:    "canonical-user",
			SessionID: "sess-1",
		},
		nil,
	)
	require.Error(t, err)
	require.Nil(t, sess)
	require.Contains(t, err.Error(), "create boom")

	getRec := &recordingSessionService{
		forceNilGet: true,
	}
	wrappedGet := WrapSessionService(getRec)
	sess, err = wrappedGet.GetSession(
		WithStorageUserID(context.Background(), "chat-scope"),
		session.Key{
			AppName:   "demo-app",
			UserID:    "canonical-user",
			SessionID: "sess-1",
		},
	)
	require.NoError(t, err)
	require.Nil(t, sess)

	getErrRec := &recordingSessionService{
		getErr: errors.New("get boom"),
	}
	wrappedGetErr := WrapSessionService(getErrRec)
	sess, err = wrappedGetErr.GetSession(
		WithStorageUserID(context.Background(), "chat-scope"),
		session.Key{
			AppName:   "demo-app",
			UserID:    "canonical-user",
			SessionID: "sess-1",
		},
	)
	require.Error(t, err)
	require.Nil(t, sess)
	require.Contains(t, err.Error(), "get boom")
}

func TestWrapSessionService_ListSessions_PropagatesErrors(t *testing.T) {
	t.Parallel()

	rec := &recordingSessionService{
		listSessionsErr: errors.New("list sessions boom"),
	}
	wrapped := WrapSessionService(rec)

	listed, err := wrapped.ListSessions(
		WithStorageUserID(context.Background(), "chat-scope"),
		session.UserKey{AppName: "demo-app", UserID: "canonical-user"},
	)
	require.Error(t, err)
	require.Nil(t, listed)
	require.Contains(t, err.Error(), "list sessions boom")
}

func TestWrapSessionService_CreateSession_PropagatesIndexingError(t *testing.T) {
	t.Parallel()

	rec := &recordingSessionService{
		updateUserStateErr: errors.New("index boom"),
	}
	wrapped := WrapSessionService(rec)

	sess, err := wrapped.CreateSession(
		WithStorageUserID(context.Background(), "chat-scope"),
		session.Key{
			AppName:   "demo-app",
			UserID:    "canonical-user",
			SessionID: "sess-1",
		},
		nil,
	)
	require.Error(t, err)
	require.Nil(t, sess)
	require.Equal(
		t,
		session.UserKey{AppName: "demo-app", UserID: "canonical-user"},
		rec.updateUserKey,
	)
	require.Contains(
		t,
		err.Error(),
		"remember indexed storage user for create session",
	)
	require.Contains(t, err.Error(), "index boom")
}

func TestWrapSessionService_GetSession_PropagatesIndexingError(t *testing.T) {
	t.Parallel()

	rec := &recordingSessionService{
		updateUserStateErr: errors.New("index boom"),
	}
	wrapped := WrapSessionService(rec)

	sess, err := wrapped.GetSession(
		WithStorageUserID(context.Background(), "chat-scope"),
		session.Key{
			AppName:   "demo-app",
			UserID:    "canonical-user",
			SessionID: "sess-1",
		},
	)
	require.Error(t, err)
	require.Nil(t, sess)
	require.Equal(
		t,
		session.UserKey{AppName: "demo-app", UserID: "canonical-user"},
		rec.updateUserKey,
	)
	require.Contains(
		t,
		err.Error(),
		"remember indexed storage user for get session",
	)
	require.Contains(t, err.Error(), "index boom")
}
