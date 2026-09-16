//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package privatestate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestUpdateUsesPrivateWriterAndCopiesState(t *testing.T) {
	ctx := context.Background()
	base := sessionmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, base.Close()) })
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	_, err := base.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	service := &recordingWriter{Service: base}
	input := session.StateMap{
		"private": []byte("value"),
		"empty":   []byte{},
		"nil":     nil,
	}

	require.NoError(t, Update(ctx, service, key, input))
	input["private"][0] = 'X'
	require.Equal(t, "value", string(service.request.State["private"]))
	require.NotNil(t, service.request.State["empty"])
	require.Empty(t, service.request.State["empty"])
	require.Nil(t, service.request.State["nil"])
	require.False(t, service.directCalled)
}

func TestUpdateFallsBackToDirectSessionState(t *testing.T) {
	ctx := context.Background()
	service := sessionmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	_, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	require.NoError(t, Update(ctx, service, key, session.StateMap{
		"private": []byte("value"),
	}))
	stored, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	value, ok := stored.GetState("private")
	require.True(t, ok)
	require.Equal(t, "value", string(value))
}

type recordingWriter struct {
	session.Service
	request      UpdateRequest
	directCalled bool
}

func (s *recordingWriter) UpdatePrivateSessionState(
	_ context.Context,
	request UpdateRequest,
) error {
	s.request = request
	return nil
}

func (s *recordingWriter) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	s.directCalled = true
	return s.Service.UpdateSessionState(ctx, key, state)
}
