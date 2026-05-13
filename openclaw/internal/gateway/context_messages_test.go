//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gateway

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/conversationscope"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
)

func TestMemoryFileContextMessages_UsesStorageScopeWithUserFallback(
	t *testing.T,
) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	chatPath, err := store.EnsureMemory(
		context.Background(),
		"demo-app",
		"wecom:chat:room-1",
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		chatPath,
		[]byte("# Memory\n\n- shared rule\n"),
		0o600,
	))

	userPath, err := store.EnsureMemory(
		context.Background(),
		"demo-app",
		"wecom:dm:user-1",
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		userPath,
		[]byte("# Memory\n\n- personal preference\n"),
		0o600,
	))

	srv := &Server{
		appName:         "demo-app",
		memoryFileStore: store,
	}
	ctx := conversationscope.WithStorageUserID(
		context.Background(),
		"wecom:chat:room-1",
	)

	msgs := srv.memoryFileContextMessages(ctx, "wecom:dm:user-1")
	require.Len(t, msgs, 2)
	require.Contains(t, msgs[0].Content, "the current chat scope")
	require.Contains(t, msgs[0].Content, "shared rule")
	require.Contains(t, msgs[1].Content, "this user")
	require.Contains(t, msgs[1].Content, "personal preference")
}

func TestMemoryFileContextMessages_UsesRuntimeProfileAppName(
	t *testing.T,
) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	profilePath, err := store.EnsureMemory(
		context.Background(),
		"profile-app",
		"wecom:dm:user-1",
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		profilePath,
		[]byte("# Memory\n\n- profile memory\n"),
		0o600,
	))

	basePath, err := store.EnsureMemory(
		context.Background(),
		"demo-app",
		"wecom:dm:user-1",
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		basePath,
		[]byte("# Memory\n\n- base memory\n"),
		0o600,
	))

	srv := &Server{
		appName:         "demo-app",
		memoryFileStore: store,
	}
	ctx := runtimeprofile.WithProfile(
		context.Background(),
		runtimeprofile.Profile{AppName: "profile-app"},
	)

	msgs := srv.memoryFileContextMessages(ctx, "wecom:dm:user-1")
	require.Len(t, msgs, 1)
	require.Contains(t, msgs[0].Content, "profile memory")
	require.NotContains(t, msgs[0].Content, "base memory")
}
