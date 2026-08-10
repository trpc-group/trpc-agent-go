//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/privatestate"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestAttemptSessionService_OverlayListStateAndDelete(t *testing.T) {
	ctx := context.Background()
	base := sessioninmemory.NewSessionService()
	baseKey := session.Key{AppName: "app", UserID: "user", SessionID: "base"}
	localKey := session.Key{AppName: "app", UserID: "user", SessionID: "local"}
	_, err := base.CreateSession(ctx, baseKey, session.StateMap{"source": []byte("base")})
	require.NoError(t, err)

	scope := newAttemptSessionService(base, nil)
	baseSession, err := scope.GetSession(ctx, baseKey)
	require.NoError(t, err)
	assert.Equal(t, "base", string(baseSession.State["source"]))

	localSession, err := scope.CreateSession(ctx, localKey, session.StateMap{"source": []byte("local")})
	require.NoError(t, err)
	assert.Equal(t, "local", string(localSession.State["source"]))

	_, err = scope.CreateSession(ctx, baseKey, session.StateMap{"source": []byte("overlay")})
	require.NoError(t, err)
	sessions, err := scope.ListSessions(ctx, session.UserKey{AppName: "app", UserID: "user"})
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	byID := make(map[string]*session.Session, len(sessions))
	for _, sess := range sessions {
		byID[sess.ID] = sess
	}
	assert.Equal(t, "overlay", string(byID["base"].State["source"]))
	assert.Equal(t, "local", string(byID["local"].State["source"]))

	require.NoError(t, scope.DeleteSession(ctx, localKey))
	sessions, err = scope.ListSessions(ctx, session.UserKey{AppName: "app", UserID: "user"})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "base", sessions[0].ID)

	require.NoError(t, scope.UpdateSessionState(ctx, baseKey, session.StateMap{
		"direct": []byte("direct-value"),
	}))
	delta := scope.DirectStateDelta()
	assert.Equal(t, "direct-value", string(delta["direct"]))
	delta["direct"][0] = 'X'
	assert.Equal(t, "direct-value", string(scope.DirectStateDelta()["direct"]))

	overlaySession, err := scope.GetSession(ctx, baseKey)
	require.NoError(t, err)
	evt := event.New("invocation", "agent", event.WithStateDelta(session.StateMap{
		"direct": []byte("event-value"),
	}))
	require.NoError(t, scope.AppendEvent(ctx, overlaySession, evt))
	assert.NotContains(t, scope.DirectStateDelta(), "direct")
}

func TestAttemptSessionService_DoesNotBypassAttemptStateIsolation(t *testing.T) {
	ctx := context.Background()
	base := sessioninmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, base.Close()) })
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	root, err := base.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	scope := newAttemptSessionService(base, root.Clone())
	_, initializesState := scope.Service().(session.StateInitializationService)
	require.False(t, initializesState)
	require.NoError(t, scope.UpdateSessionState(ctx, key, session.StateMap{
		"private-state": []byte("value"),
	}))
	require.Equal(t, "value", string(scope.DirectStateDelta()["private-state"]))

	persisted, err := base.GetSession(ctx, key)
	require.NoError(t, err)
	_, present := persisted.GetState("private-state")
	require.False(t, present)
}

func TestAttemptSessionService_PrivateStateIsolatedFromDirectDelta(t *testing.T) {
	ctx := context.Background()
	base := sessioninmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, base.Close()) })
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	root, err := base.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	scope := newAttemptSessionService(base, root.Clone())
	_, privateWriter := scope.Service().(privatestate.Writer)
	require.True(t, privateWriter)
	require.NoError(t, privatestate.Update(ctx, scope.Service(), key, session.StateMap{
		"private": []byte("value"),
	}))

	assert.Empty(t, scope.DirectStateDelta())
	updates := scope.privateStateUpdates()
	require.Len(t, updates, 1)
	assert.Equal(t, key, updates[0].Key)
	assert.Equal(t, "value", string(updates[0].State["private"]))
	updates[0].State["private"][0] = 'X'
	assert.Equal(t, "value", string(scope.privateStateUpdates()[0].State["private"]))

	overlay, err := scope.GetSession(ctx, key)
	require.NoError(t, err)
	value, ok := overlay.GetState("private")
	require.True(t, ok)
	assert.Equal(t, "value", string(value))
	persisted, err := base.GetSession(ctx, key)
	require.NoError(t, err)
	_, ok = persisted.GetState("private")
	assert.False(t, ok)
}

func TestAttemptSessionService_RejectsInvalidKeysAndProtectedState(t *testing.T) {
	ctx := context.Background()
	scope := newAttemptSessionService(nil, nil)
	validKey := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	validUser := session.UserKey{AppName: "app", UserID: "user"}

	_, err := scope.CreateSession(ctx, session.Key{}, nil)
	require.Error(t, err)
	_, err = scope.GetSession(ctx, session.Key{})
	require.Error(t, err)
	_, err = scope.ListSessions(ctx, session.UserKey{})
	require.Error(t, err)
	require.Error(t, scope.DeleteSession(ctx, session.Key{}))

	err = scope.UpdateAppState(ctx, "", session.StateMap{"k": []byte("v")})
	require.ErrorIs(t, err, session.ErrAppNameRequired)
	err = scope.UpdateAppState(ctx, "app", session.StateMap{"k": []byte("v")})
	require.ErrorIs(t, err, errAttemptAppStateWriteDisabled)
	err = scope.DeleteAppState(ctx, "", "k")
	require.ErrorIs(t, err, session.ErrAppNameRequired)
	err = scope.DeleteAppState(ctx, "app", "k")
	require.ErrorIs(t, err, errAttemptAppStateWriteDisabled)
	appState, err := scope.ListAppStates(ctx, "app")
	require.NoError(t, err)
	assert.Empty(t, appState)

	err = scope.UpdateUserState(ctx, session.UserKey{}, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
	err = scope.UpdateUserState(ctx, validUser, session.StateMap{"k": []byte("v")})
	require.ErrorIs(t, err, errAttemptUserStateWriteDisabled)
	_, err = scope.ListUserStates(ctx, session.UserKey{})
	require.Error(t, err)
	userState, err := scope.ListUserStates(ctx, validUser)
	require.NoError(t, err)
	assert.Empty(t, userState)
	err = scope.DeleteUserState(ctx, session.UserKey{}, "k")
	require.Error(t, err)
	err = scope.DeleteUserState(ctx, validUser, "k")
	require.ErrorIs(t, err, errAttemptUserStateWriteDisabled)

	err = scope.UpdateSessionState(ctx, session.Key{}, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
	err = scope.UpdateSessionState(ctx, validKey, session.StateMap{
		session.StateAppPrefix + "token": []byte("v"),
	})
	require.Error(t, err)
	err = scope.UpdateSessionState(ctx, validKey, session.StateMap{
		session.StateUserPrefix + "token": []byte("v"),
	})
	require.Error(t, err)
	err = scope.UpdateSessionState(ctx, validKey, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
}

func TestAttemptSessionService_AppendEventCreatesMissingSessionAndHandlesNil(t *testing.T) {
	ctx := context.Background()
	scope := newAttemptSessionService(nil, nil)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)

	require.ErrorIs(t, scope.AppendEvent(ctx, nil, nil), session.ErrNilSession)
	require.NoError(t, scope.AppendEvent(ctx, sess, nil))
	got, err := scope.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, key.SessionID, got.ID)
}
