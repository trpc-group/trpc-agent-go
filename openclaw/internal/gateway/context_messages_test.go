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
	"path/filepath"
	"strings"
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

func TestMemoryFileContextMessages_UsesPersonalStorageScope(
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
		[]byte("# Memory\n\n- shared group note\n"),
		0o600,
	))

	userPath, err := store.EnsureMemory(
		context.Background(),
		"demo-app",
		"wecom:dm:T123",
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		userPath,
		[]byte("# Memory\n\n- private todo\n"),
		0o600,
	))

	aliasPath, err := store.EnsureMemory(
		context.Background(),
		"demo-app",
		"wecom:dm:wineguo",
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		aliasPath,
		[]byte("# Memory\n\n- alias-only note\n"),
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
	ctx = conversationscope.WithUserStorageID(ctx, "wecom:dm:T123")

	msgs := srv.memoryFileContextMessages(ctx, "wecom:dm:wineguo")
	require.Len(t, msgs, 2)
	require.Contains(t, msgs[0].Content, "shared group note")
	require.Contains(t, msgs[1].Content, "private todo")
	require.NotContains(t, msgs[1].Content, "alias-only note")
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

func TestMemoryFileContextMessages_RefreshesSecondaryDefaultBeforeFiltering(
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
		[]byte("# Memory\n\n- shared group note\n"),
		0o600,
	))

	userPath, err := store.MemoryPath("demo-app", "wecom:dm:user-1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(userPath), 0o700))
	require.NoError(t, os.WriteFile(
		userPath,
		[]byte(previousMemoryTemplateForTest()),
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
	require.Len(t, msgs, 1)
	require.Contains(t, msgs[0].Content, "shared group note")
	require.NotContains(t, msgs[0].Content, "Repeated working style")

	raw, err := os.ReadFile(userPath)
	require.NoError(t, err)
	require.Contains(t, string(raw), "## Saved user preferences")
	require.NotContains(t, string(raw), "Repeated working style")
}

func previousMemoryTemplateForTest() string {
	return strings.Join([]string{
		"# Memory",
		"",
		"This is a visible file for durable memory in the current scope.",
		"It is user-visible, not hidden internal state.",
		"If the user asks what is remembered here or asks to " +
			"inspect this file, the agent may quote or summarize " +
			"the relevant parts.",
		"If the user explicitly says \"remember this\" or asks " +
			"the agent to remember a durable preference, fact, " +
			"or workflow rule, update this file with a short " +
			"bullet.",
		"",
		"This file stores stable, low-volume memory about the user.",
		"",
		"The agent may update this file only when all conditions hold:",
		"- The information is likely to matter in future sessions.",
		"- The information is stable, not task-local noise.",
		"- The information can be written as a short bullet.",
		"- The information does not contain secrets.",
		"",
		"Do not store:",
		"- Secrets, credentials, or private tokens.",
		"- Large conversation summaries.",
		"- One-off debugging details.",
		"",
		"## Long-term facts",
		"",
		"Use for stable facts such as the user's name or role.",
		"",
		"## Preferences",
		"",
		"Use for durable tone, nickname, format, or persona " +
			"preferences.",
		"",
		"## Repeated working style",
		"",
		"Use for recurring workflow rules such as git, PR, or " +
			"review habits.",
		"",
	}, "\n")
}
