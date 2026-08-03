//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package externalization

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestWrapDisabledReturnsOriginalService(t *testing.T) {
	inner := sessioninmemory.NewSessionService()

	wrapped := Wrap(inner, artifactinmemory.NewService(), Config{})

	assert.Equal(t, inner, wrapped)
}

func TestWrapEnabledExternalizesEvents(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	inner := sessioninmemory.NewSessionService()

	wrapped := Wrap(inner, artifactinmemory.NewService(), Config{Enabled: true})
	assert.NotEqual(t, inner, wrapped)
	_, ok := wrapped.(session.WindowService)
	assert.True(t, ok, "wrapped service should preserve optional WindowService")

	sess, err := wrapped.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	msg := model.NewUserMessage("image")
	msg.AddImageData([]byte("image-bytes"), "high", "png")
	evt := event.NewResponseEvent("invocation", "user", &model.Response{
		Choices: []model.Choice{{Message: msg}},
	})

	require.NoError(t, wrapped.AppendEvent(ctx, sess, evt))
	persisted, err := inner.GetSession(ctx, key)
	require.NoError(t, err)
	part := persisted.Events[0].Response.Choices[0].Message.ContentParts[0]
	assert.Empty(t, part.Image.Data)
	require.NotNil(t, part.ContentRef)
	assert.NotEmpty(t, part.ContentRef.ArtifactName)
}

func TestWrapPreservesCoordinatedStateInitializationCapability(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	inner := sessioninmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, inner.Close()) })
	_, err := inner.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	wrapped := Wrap(
		inner,
		artifactinmemory.NewService(),
		Config{Enabled: true},
	)
	initializer, ok := wrapped.(session.StateInitializationService)
	require.True(t, ok)
	value, didInitialize, err := initializer.LoadOrInitializeSessionState(
		ctx,
		key,
		"private-state",
		func(value []byte) bool { return string(value) == "value" },
		func(context.Context) ([]byte, error) { return []byte("value"), nil },
	)
	require.NoError(t, err)
	require.True(t, didInitialize)
	require.Equal(t, "value", string(value))

	withoutCapability := Wrap(
		&requiredSessionServiceOnly{Service: inner},
		artifactinmemory.NewService(),
		Config{Enabled: true},
	)
	_, ok = withoutCapability.(session.StateInitializationService)
	require.False(t, ok)
}

type requiredSessionServiceOnly struct {
	session.Service
}
