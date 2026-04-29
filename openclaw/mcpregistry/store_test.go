//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mcpregistry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

const (
	testAppName   = "app"
	testSessionID = "sess-1"
	testUserID    = "user-1"
	testChatID    = "chat-1"
)

func TestFileStore_RedactsSensitiveValuesAndResolvesRawConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewFileStore(dir)
	runtime := testRuntimeContext()

	view, err := store.Upsert(context.Background(), UpsertRequest{
		Context:     runtime,
		Name:        "docs",
		Scope:       ScopeChat,
		Description: "Docs MCP",
		Connection: mcp.ConnectionConfig{
			Transport: "streamable_http",
			ServerURL: "https://user:pass@example.com/mcp?apikey=secret&plain=ok",
			Headers: map[string]string{
				"Authorization": "Bearer secret",
				"X-Trace":       "trace",
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, ScopeChat, view.Scope)
	require.Equal(t, "docs", view.Selector)
	require.True(t, view.HasSensitiveValues)
	require.NotContains(t, view.ServerURL, "secret")
	require.NotContains(t, view.ServerURL, "user")
	require.NotContains(t, view.ServerURL, "pass")

	persisted, ok, err := readPersistedStateFile(
		filepath.Join(dir, defaultStateFileName),
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, persisted.Registry.Entries, 1)
	entry := persisted.Registry.Entries[0]
	require.NotContains(t, entry.Connection.ServerURL, "apikey=secret")
	require.NotContains(t, entry.Connection.ServerURL, "user")
	require.NotContains(t, entry.Connection.ServerURL, "pass")
	require.Equal(t, secretRedaction, entry.Connection.Headers["Authorization"])

	raw := persisted.Secrets.Connections[entry.ID]
	require.Equal(t, "Bearer secret", raw.Headers["Authorization"])
	require.Contains(t, raw.ServerURL, "user:pass")

	servers, err := store.ServerConfigs(context.Background(), runtime)
	require.NoError(t, err)
	require.Contains(t, servers, "docs")
	require.Contains(t, servers, "chat:docs")
	require.Equal(t, "https://user:pass@example.com/mcp?apikey=secret&plain=ok",
		servers["docs"].ServerURL)
	require.Equal(t, "Bearer secret", servers["docs"].Headers["Authorization"])
}

func TestFileStore_RedactsSensitiveStdioArgs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewFileStore(dir)
	runtime := testRuntimeContext()

	_, err := store.Upsert(context.Background(), UpsertRequest{
		Context: runtime,
		Name:    "docs",
		Scope:   ScopeSession,
		Connection: mcp.ConnectionConfig{
			Transport: "stdio",
			Command:   "mcporter",
			Args: []string{
				"--token",
				"arg-secret",
				"--api-key=inline-secret",
				"--header=Authorization: Bearer header-secret",
				"https://example.com/mcp?token=url-secret",
				"--plain",
				"ok",
			},
		},
	})
	require.NoError(t, err)

	persisted, ok, err := readPersistedStateFile(
		filepath.Join(dir, defaultStateFileName),
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, persisted.Registry.Entries, 1)
	registryBytes, err := json.Marshal(persisted.Registry)
	require.NoError(t, err)
	registryText := string(registryBytes)
	require.NotContains(t, registryText, "arg-secret")
	require.NotContains(t, registryText, "inline-secret")
	require.NotContains(t, registryText, "header-secret")
	require.NotContains(t, registryText, "url-secret")
	require.Contains(t, registryText, secretRedaction)

	servers, err := store.ServerConfigs(context.Background(), runtime)
	require.NoError(t, err)
	require.Equal(t, "arg-secret", servers["docs"].Args[1])
	require.Equal(t, "--api-key=inline-secret", servers["docs"].Args[2])
}

func TestFileStore_UpdateMergesExistingConnection(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	runtime := testRuntimeContext()
	_, err := store.Upsert(context.Background(), UpsertRequest{
		Context:     runtime,
		Name:        "docs",
		Scope:       ScopeChat,
		Description: "old docs",
		Connection: mcp.ConnectionConfig{
			Transport: "streamable_http",
			ServerURL: "https://example.com/mcp?token=secret",
			Headers: map[string]string{
				"Authorization": "Bearer secret",
			},
		},
	})
	require.NoError(t, err)

	view, err := store.Upsert(context.Background(), UpsertRequest{
		Context:     runtime,
		Name:        "docs",
		Scope:       ScopeChat,
		Description: "new docs",
		UpdateOnly:  true,
		Connection:  mcp.ConnectionConfig{},
	})
	require.NoError(t, err)
	require.Equal(t, "new docs", view.Description)
	require.True(t, view.HasSensitiveValues)

	servers, err := store.ServerConfigs(context.Background(), runtime)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/mcp?token=secret",
		servers["docs"].ServerURL)
	require.Equal(t, "Bearer secret", servers["docs"].Headers["Authorization"])
}

func TestFileStore_UpdateCanClearFieldsForTransportChange(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	runtime := testRuntimeContext()
	_, err := store.Upsert(context.Background(), UpsertRequest{
		Context: runtime,
		Name:    "docs",
		Scope:   ScopeSession,
		Connection: mcp.ConnectionConfig{
			Transport: "streamable_http",
			ServerURL: "https://example.com/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer secret",
			},
		},
	})
	require.NoError(t, err)

	_, err = store.Upsert(context.Background(), UpsertRequest{
		Context:        runtime,
		Name:           "docs",
		Scope:          ScopeSession,
		UpdateOnly:     true,
		ClearServerURL: true,
		ClearHeaders:   true,
		Connection: mcp.ConnectionConfig{
			Transport: "stdio",
			Command:   "mcporter",
			Args:      []string{"serve"},
		},
	})
	require.NoError(t, err)

	servers, err := store.ServerConfigs(context.Background(), runtime)
	require.NoError(t, err)
	require.Equal(t, "stdio", servers["docs"].Transport)
	require.Equal(t, "mcporter", servers["docs"].Command)
	require.Empty(t, servers["docs"].ServerURL)
	require.Empty(t, servers["docs"].Headers)
}

func TestFileStore_ScopeVisibilityAndAliasPrecedence(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	runtime := testRuntimeContext()
	_, err := store.Upsert(context.Background(), UpsertRequest{
		Context: runtime,
		Name:    "docs",
		Scope:   ScopeGlobal,
		Connection: mcp.ConnectionConfig{
			ServerURL: "https://global.example.com/mcp",
		},
	})
	require.NoError(t, err)
	_, err = store.Upsert(context.Background(), UpsertRequest{
		Context: runtime,
		Name:    "docs",
		Scope:   ScopeChat,
		Connection: mcp.ConnectionConfig{
			ServerURL: "https://chat.example.com/mcp",
		},
	})
	require.NoError(t, err)

	servers, err := store.ServerConfigs(context.Background(), runtime)
	require.NoError(t, err)
	require.Equal(t, "https://chat.example.com/mcp", servers["docs"].ServerURL)
	require.Equal(t,
		"https://global.example.com/mcp",
		servers["global:docs"].ServerURL)

	otherRuntime := runtime
	otherRuntime.ChatID = "other-chat"
	otherRuntime.StorageUserID = "other-chat"
	servers, err = store.ServerConfigs(context.Background(), otherRuntime)
	require.NoError(t, err)
	require.Equal(t, "https://global.example.com/mcp", servers["docs"].ServerURL)
	require.NotContains(t, servers, "chat:docs")
}

func TestRuntimeContextFromContext_UsesConversationAnnotation(t *testing.T) {
	t.Parallel()

	ctx := testInvocationContext()
	runtime, err := RuntimeContextFromContext(ctx, "fallback")
	require.NoError(t, err)
	require.Equal(t, testAppName, runtime.AppName)
	require.Equal(t, testSessionID, runtime.SessionID)
	require.Equal(t, testUserID, runtime.UserID)
	require.Equal(t, testChatID, runtime.ChatID)
	require.Equal(t, "chat-storage", runtime.StorageUserID)
}

func TestRuntimeContextFromContext_UsesFallbackAppName(t *testing.T) {
	t.Parallel()

	inv := &agent.Invocation{
		Session: &session.Session{
			ID:     testSessionID,
			UserID: testUserID,
		},
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	runtime, err := RuntimeContextFromContext(ctx, testAppName)
	require.NoError(t, err)
	require.Equal(t, testAppName, runtime.AppName)
	require.Equal(t, testSessionID, runtime.SessionID)
	require.Equal(t, testUserID, runtime.UserID)
	require.Empty(t, runtime.StorageUserID)
	require.Empty(t, runtime.ChatID)
}

func TestFileStore_DeleteRemovesEntryAndSecret(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewFileStore(dir)
	runtime := testRuntimeContext()
	_, err := store.Upsert(context.Background(), UpsertRequest{
		Context: runtime,
		Name:    "docs",
		Scope:   ScopeSession,
		Connection: mcp.ConnectionConfig{
			ServerURL: "https://example.com/mcp?token=secret",
		},
	})
	require.NoError(t, err)

	removed, scope, err := store.Delete(context.Background(), DeleteRequest{
		Context: runtime,
		Name:    "docs",
		Scope:   ScopeSession,
	})
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, ScopeSession, scope)

	servers, err := store.ServerConfigs(context.Background(), runtime)
	require.NoError(t, err)
	require.Empty(t, servers)

	_, err = os.Stat(filepath.Join(dir, defaultSecretsFileName))
	require.True(t, os.IsNotExist(err))

	persisted, ok, err := readPersistedStateFile(
		filepath.Join(dir, defaultStateFileName),
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, persisted.Registry.Entries)
	require.Empty(t, persisted.Secrets.Connections)
}

func TestFileStore_LoadsLegacySplitFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewFileStore(dir)
	runtime := testRuntimeContext()
	id := entryID(ScopeSession, runtime.SessionID, "docs")
	now := time.Now()
	require.NoError(t, writeJSONFile(
		filepath.Join(dir, defaultRegistryFileName),
		registryState{Entries: []Entry{{
			ID:                 id,
			Name:               "docs",
			Scope:              ScopeSession,
			ScopeKey:           runtime.SessionID,
			Connection:         mcp.ConnectionConfig{ServerURL: secretRedaction},
			HasSensitiveValues: true,
			CreatedAt:          now,
			UpdatedAt:          now,
		}}},
		0o600,
	))
	require.NoError(t, writeJSONFile(
		filepath.Join(dir, defaultSecretsFileName),
		secretState{Connections: map[string]mcp.ConnectionConfig{
			id: {ServerURL: "https://example.com/mcp?token=secret"},
		}},
		0o600,
	))

	servers, err := store.ServerConfigs(context.Background(), runtime)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/mcp?token=secret",
		servers["docs"].ServerURL)
}

func TestFileStore_UpsertReturnsQualifiedSelectorWhenBareNameIsShadowed(
	t *testing.T,
) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	runtime := testRuntimeContext()
	_, err := store.Upsert(context.Background(), UpsertRequest{
		Context: runtime,
		Name:    "docs",
		Scope:   ScopeSession,
		Connection: mcp.ConnectionConfig{
			ServerURL: "https://session.example.com/mcp",
		},
	})
	require.NoError(t, err)

	view, err := store.Upsert(context.Background(), UpsertRequest{
		Context: runtime,
		Name:    "docs",
		Scope:   ScopeGlobal,
		Connection: mcp.ConnectionConfig{
			ServerURL: "https://global.example.com/mcp",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "global:docs", view.Selector)
	require.Equal(t, "global:docs", view.QualifiedSelector)

	views, err := store.List(context.Background(), runtime)
	require.NoError(t, err)
	viewsByScope := make(map[Scope]EntryView, len(views))
	for _, candidate := range views {
		viewsByScope[candidate.Scope] = candidate
	}
	require.Equal(t, "docs", viewsByScope[ScopeSession].Selector)
	require.Equal(t, "global:docs", viewsByScope[ScopeGlobal].Selector)
}

func TestFileStore_ExactChatEntryOverridesStorageFallback(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	runtime := testRuntimeContext()
	fallbackRuntime := runtime
	fallbackRuntime.ChatID = ""
	_, err := store.Upsert(context.Background(), UpsertRequest{
		Context: fallbackRuntime,
		Name:    "docs",
		Scope:   ScopeChat,
		Connection: mcp.ConnectionConfig{
			ServerURL: "https://fallback.example.com/mcp",
		},
	})
	require.NoError(t, err)
	_, err = store.Upsert(context.Background(), UpsertRequest{
		Context: runtime,
		Name:    "docs",
		Scope:   ScopeChat,
		Connection: mcp.ConnectionConfig{
			ServerURL: "https://chat.example.com/mcp",
		},
	})
	require.NoError(t, err)

	servers, err := store.ServerConfigs(context.Background(), runtime)
	require.NoError(t, err)
	require.Equal(t, "https://chat.example.com/mcp", servers["docs"].ServerURL)
	require.Equal(t,
		"https://chat.example.com/mcp",
		servers["chat:docs"].ServerURL)
	require.NotContains(t, servers["chat:docs"].ServerURL, "fallback")
}

func TestFileStore_DefaultDirUsesStateDir(t *testing.T) {
	t.Parallel()

	require.Equal(t, filepath.Join("/state", "mcp"), DefaultDir("/state"))
	require.Equal(t, filepath.Join("mcp"), DefaultDir(" "))
}

func TestFileStore_ListReturnsVisibleScopedViews(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	runtime := testRuntimeContext()
	runtime.WorkspaceID = "workspace-1"

	for _, item := range []struct {
		name  string
		scope Scope
		url   string
	}{
		{
			name:  "session-docs",
			scope: ScopeSession,
			url:   "https://session.example.com/mcp",
		},
		{
			name:  "user-docs",
			scope: ScopeUser,
			url:   "https://user.example.com/mcp",
		},
		{
			name:  "workspace-docs",
			scope: ScopeWorkspace,
			url:   "https://workspace.example.com/mcp",
		},
		{
			name:  "global-docs",
			scope: ScopeGlobal,
			url:   "https://global.example.com/mcp",
		},
	} {
		_, err := store.Upsert(context.Background(), UpsertRequest{
			Context: runtime,
			Name:    item.name,
			Scope:   item.scope,
			Connection: mcp.ConnectionConfig{
				ServerURL: item.url,
			},
		})
		require.NoError(t, err)
	}

	views, err := store.List(context.Background(), runtime)
	require.NoError(t, err)
	require.Len(t, views, 4)
	require.Equal(t, ScopeSession, views[0].Scope)
	require.Equal(t, ScopeGlobal, views[3].Scope)

	otherRuntime := runtime
	otherRuntime.SessionID = "session-2"
	otherRuntime.UserID = "user-2"
	otherRuntime.ChatID = "chat-2"
	otherRuntime.StorageUserID = "chat-storage-2"

	views, err = store.List(context.Background(), otherRuntime)
	require.NoError(t, err)
	require.Len(t, views, 2)
	require.Equal(t, ScopeWorkspace, views[0].Scope)
	require.Equal(t, ScopeGlobal, views[1].Scope)
}

func TestFileStore_ValidationErrors(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	runtime := testRuntimeContext()

	_, err := store.Upsert(context.Background(), UpsertRequest{
		Context: RuntimeContext{},
		Name:    "docs",
		Scope:   ScopeSession,
		Connection: mcp.ConnectionConfig{
			ServerURL: "https://example.com/mcp",
		},
	})
	require.ErrorIs(t, err, errRegistryContextUnavailable)

	_, err = store.Upsert(context.Background(), UpsertRequest{
		Context: runtime,
		Name:    "docs",
		Scope:   Scope("bad"),
		Connection: mcp.ConnectionConfig{
			ServerURL: "https://example.com/mcp",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported mcp registry scope")

	_, err = store.Upsert(context.Background(), UpsertRequest{
		Context: runtime,
		Name:    "docs",
		Scope:   ScopeWorkspace,
		Connection: mcp.ConnectionConfig{
			ServerURL: "https://example.com/mcp",
		},
	})
	require.ErrorIs(t, err, errRegistryContextUnavailable)

	_, err = store.Upsert(context.Background(), UpsertRequest{
		Context: runtime,
		Name:    "docs",
		Connection: mcp.ConnectionConfig{
			Transport: transportStdio,
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "stdio MCP requires command")

	_, err = store.Upsert(context.Background(), UpsertRequest{
		Context: runtime,
		Name:    "docs",
		Connection: mcp.ConnectionConfig{
			Transport: transportStreamable,
			ServerURL: "file:///tmp/mcp",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "http or https")

	_, err = store.Upsert(context.Background(), UpsertRequest{
		Context: runtime,
		Name:    "docs",
		Connection: mcp.ConnectionConfig{
			Transport: transportStreamable,
			ServerURL: "https://example.com/mcp",
			Command:   "mcporter",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "HTTP MCP cannot use command")
}

func TestFileStore_ReadJSONFileHandlesEmptyAndInvalidFiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte(" \n"), 0o600))

	state, err := readJSONFile[registryState](path)
	require.NoError(t, err)
	require.Empty(t, state.Entries)

	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	_, err = readJSONFile[registryState](path)
	require.Error(t, err)
}

func testRuntimeContext() RuntimeContext {
	return RuntimeContext{
		AppName:       testAppName,
		SessionID:     testSessionID,
		UserID:        testUserID,
		StorageUserID: "chat-storage",
		ChatID:        testChatID,
	}
}

func testInvocationContext() context.Context {
	inv := &agent.Invocation{
		Session: &session.Session{
			AppName: testAppName,
			ID:      testSessionID,
			UserID:  testUserID,
		},
		RunOptions: agent.RunOptions{
			RuntimeState: conversation.RuntimeState(
				conversation.Annotation{
					StorageUserID: "chat-storage",
					ChatID:        testChatID,
				},
			),
		},
	}
	return agent.NewInvocationContext(context.Background(), inv)
}

func TestNormalizeNameRejectsControlWhitespace(t *testing.T) {
	t.Parallel()

	_, err := normalizeName("bad\nname")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "whitespace"))
}
