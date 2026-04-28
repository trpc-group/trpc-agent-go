//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package noop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestServiceImplementsInterfaces(t *testing.T) {
	var _ session.Service = NewService()
	var _ session.TrackService = NewService()
}

func TestCreateSessionReturnsTransientSession(t *testing.T) {
	svc := NewService()
	state := session.StateMap{"k": []byte("v")}

	sess, err := svc.CreateSession(context.Background(), session.Key{
		AppName: "app",
		UserID:  "user",
	}, state)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NotEmpty(t, sess.ID)

	got, ok := sess.GetState("k")
	require.True(t, ok)
	assert.Equal(t, []byte("v"), got)

	state["k"][0] = 'x'
	got, ok = sess.GetState("k")
	require.True(t, ok)
	assert.Equal(t, []byte("v"), got)
}

func TestServiceDoesNotPersistSessionsOrState(t *testing.T) {
	ctx := context.Background()
	svc := NewService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}

	sess, err := svc.CreateSession(ctx, key, session.StateMap{"k": []byte("v")})
	require.NoError(t, err)
	require.NotNil(t, sess)

	got, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, got)

	sessions, err := svc.ListSessions(ctx, session.UserKey{AppName: "app", UserID: "user"})
	require.NoError(t, err)
	assert.Empty(t, sessions)

	require.NoError(t, svc.UpdateAppState(ctx, "app", session.StateMap{"app": []byte("v")}))
	appState, err := svc.ListAppStates(ctx, "app")
	require.NoError(t, err)
	assert.Empty(t, appState)

	require.NoError(t, svc.UpdateUserState(ctx, session.UserKey{AppName: "app", UserID: "user"}, session.StateMap{"user": []byte("v")}))
	userState, err := svc.ListUserStates(ctx, session.UserKey{AppName: "app", UserID: "user"})
	require.NoError(t, err)
	assert.Empty(t, userState)
}

func TestAppendEventUpdatesOnlyTransientSession(t *testing.T) {
	ctx := context.Background()
	svc := NewService()
	sess := session.NewSession("app", "user", "session")
	evt := event.NewResponseEvent(
		"invocation",
		"author",
		&model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				},
			}},
		},
	)
	evt.StateDelta = session.StateMap{"answer": []byte("42")}

	err := svc.AppendEvent(ctx, sess, evt)
	require.NoError(t, err)
	assert.Equal(t, 1, sess.GetEventCount())
	got, ok := sess.GetState("answer")
	require.True(t, ok)
	assert.Equal(t, []byte("42"), got)

	stored, err := svc.GetSession(ctx, session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	})
	require.NoError(t, err)
	assert.Nil(t, stored)
}

func TestAppendTrackEventUpdatesOnlyTransientSession(t *testing.T) {
	ctx := context.Background()
	svc := NewService()
	sess := session.NewSession("app", "user", "session")

	err := svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track:   "trace",
		Payload: []byte(`{"ok":true}`),
	})
	require.NoError(t, err)

	events, err := sess.GetTrackEvents("trace")
	require.NoError(t, err)
	require.Len(t, events.Events, 1)
	assert.JSONEq(t, `{"ok":true}`, string(events.Events[0].Payload))

	stored, err := svc.GetSession(ctx, session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	})
	require.NoError(t, err)
	assert.Nil(t, stored)
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	svc := NewService()

	_, err := svc.CreateSession(ctx, session.Key{UserID: "user"}, nil)
	require.ErrorIs(t, err, session.ErrAppNameRequired)

	_, err = svc.GetSession(ctx, session.Key{AppName: "app", UserID: "user"})
	require.ErrorIs(t, err, session.ErrSessionIDRequired)

	_, err = svc.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	}, session.WithGetSessionEventPage(0, 1))
	require.ErrorIs(t, err, session.ErrEventPageUnsupported)

	_, err = svc.ListSessions(ctx, session.UserKey{
		AppName: "app",
		UserID:  "user",
	}, session.WithGetSessionEventPage(0, 1))
	require.ErrorIs(t, err, session.ErrEventPageOnlyForGetSession)

	err = svc.UpdateUserState(ctx, session.UserKey{AppName: "app", UserID: "user"}, session.StateMap{
		session.StateTempPrefix + "k": []byte("v"),
	})
	require.NoError(t, err)

	err = svc.UpdateSessionState(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	}, session.StateMap{
		session.StateUserPrefix + "k": []byte("v"),
	})
	require.NoError(t, err)

	require.ErrorIs(t, svc.AppendEvent(ctx, nil, nil), session.ErrNilSession)
	require.ErrorIs(t, svc.AppendTrackEvent(ctx, nil, nil), session.ErrNilSession)
	require.ErrorIs(t, svc.CreateSessionSummary(ctx, nil, "", false), session.ErrNilSession)
	require.ErrorIs(t, svc.EnqueueSummaryJob(ctx, nil, "", false), session.ErrNilSession)
}

func TestNoopStateMethodsValidateKeys(t *testing.T) {
	ctx := context.Background()
	svc := NewService()

	require.NoError(t, svc.DeleteSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	}))
	require.ErrorIs(t,
		svc.DeleteSession(ctx, session.Key{AppName: "app", UserID: "user"}),
		session.ErrSessionIDRequired,
	)

	require.ErrorIs(t,
		svc.UpdateAppState(ctx, "", session.StateMap{"k": []byte("v")}),
		session.ErrAppNameRequired,
	)
	require.NoError(t, svc.DeleteAppState(ctx, "app", "k"))
	require.ErrorIs(t,
		svc.DeleteAppState(ctx, "", "k"),
		session.ErrAppNameRequired,
	)
	_, err := svc.ListAppStates(ctx, "")
	require.ErrorIs(t, err, session.ErrAppNameRequired)

	userKey := session.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, svc.DeleteUserState(ctx, userKey, "k"))
	require.ErrorIs(t,
		svc.DeleteUserState(ctx, session.UserKey{AppName: "app"}, "k"),
		session.ErrUserIDRequired,
	)
	require.ErrorIs(t,
		svc.UpdateUserState(ctx, session.UserKey{UserID: "user"}, session.StateMap{}),
		session.ErrAppNameRequired,
	)
	_, err = svc.ListUserStates(ctx, session.UserKey{AppName: "app"})
	require.ErrorIs(t, err, session.ErrUserIDRequired)

	require.ErrorIs(t,
		svc.UpdateSessionState(ctx, session.Key{
			AppName: "app",
			UserID:  "user",
		}, session.StateMap{}),
		session.ErrSessionIDRequired,
	)
}

func TestNoopEventAndSummaryMethodsValidateSessions(t *testing.T) {
	ctx := context.Background()
	svc := NewService()

	invalidSession := session.NewSession("", "user", "session")
	require.ErrorIs(t,
		svc.AppendEvent(ctx, invalidSession, nil),
		session.ErrAppNameRequired,
	)
	require.ErrorIs(t,
		svc.AppendTrackEvent(ctx, invalidSession, &session.TrackEvent{Track: "trace"}),
		session.ErrAppNameRequired,
	)
	require.ErrorIs(t,
		svc.CreateSessionSummary(ctx, invalidSession, "", false),
		session.ErrAppNameRequired,
	)
	require.ErrorIs(t,
		svc.EnqueueSummaryJob(ctx, invalidSession, "", false),
		session.ErrAppNameRequired,
	)

	validSession := session.NewSession("app", "user", "session")
	require.NoError(t, svc.CreateSessionSummary(ctx, validSession, "", false))
	require.NoError(t, svc.EnqueueSummaryJob(ctx, validSession, "", false))

	text, ok := svc.GetSessionSummaryText(ctx, validSession, session.WithSummaryFilterKey("filter"))
	assert.Empty(t, text)
	assert.False(t, ok)
	require.NoError(t, svc.Close())
}

func TestAppendTrackEventPropagatesTransientSessionError(t *testing.T) {
	ctx := context.Background()
	svc := NewService()
	sess := session.NewSession("app", "user", "session")

	err := svc.AppendTrackEvent(ctx, sess, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "track event is nil")
}
