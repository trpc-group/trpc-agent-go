//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package workspacefacade

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestArtifactSaveSkipReason(t *testing.T) {
	require.Equal(t, SaveReasonNoInvocation, ArtifactSaveSkipReason(context.Background()))

	noSvc := agent.NewInvocationContext(context.Background(), agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess",
			AppName: "app",
			UserID:  "user",
		}),
	))
	require.Equal(t, SaveReasonNoService, ArtifactSaveSkipReason(noSvc))

	noSession := agent.NewInvocationContext(context.Background(), agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationArtifactService(inmemory.NewService()),
	))
	require.Equal(t, SaveReasonNoSession, ArtifactSaveSkipReason(noSession))

	incompleteSession := agent.NewInvocationContext(context.Background(), agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationArtifactService(inmemory.NewService()),
		agent.WithInvocationSession(&session.Session{ID: "sess"}),
	))
	require.Equal(t, SaveReasonNoSessionIDs, ArtifactSaveSkipReason(incompleteSession))

	complete := agent.NewInvocationContext(context.Background(), agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationArtifactService(inmemory.NewService()),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess",
			AppName: "app",
			UserID:  "user",
		}),
	))
	require.Equal(t, "", ArtifactSaveSkipReason(complete))
}

func TestWithArtifactContext(t *testing.T) {
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationArtifactService(inmemory.NewService()),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess",
			AppName: "app",
			UserID:  "user",
		}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	out := WithArtifactContext(ctx)
	_, ok := codeexecutor.ArtifactServiceFromContext(out)
	require.True(t, ok)
	_, err := codeexecutor.SaveArtifactHelper(out, "out/site.zip", []byte("payload"), "text/plain")
	require.NoError(t, err)
}
