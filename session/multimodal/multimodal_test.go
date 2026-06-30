//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package multimodal

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
