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
	"os"
	"path/filepath"
	"strings"
	"testing"

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
			ServerURL: "https://example.com/mcp?apikey=secret&plain=ok",
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

	registryBytes, err := os.ReadFile(
		filepath.Join(dir, defaultRegistryFileName),
	)
	require.NoError(t, err)
	require.NotContains(t, string(registryBytes), "Bearer secret")
	require.NotContains(t, string(registryBytes), "apikey=secret")

	secretsBytes, err := os.ReadFile(
		filepath.Join(dir, defaultSecretsFileName),
	)
	require.NoError(t, err)
	require.Contains(t, string(secretsBytes), "Bearer secret")

	servers, err := store.ServerConfigs(context.Background(), runtime)
	require.NoError(t, err)
	require.Contains(t, servers, "docs")
	require.Contains(t, servers, "chat:docs")
	require.Equal(t, "https://example.com/mcp?apikey=secret&plain=ok",
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

	registryBytes, err := os.ReadFile(
		filepath.Join(dir, defaultRegistryFileName),
	)
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
