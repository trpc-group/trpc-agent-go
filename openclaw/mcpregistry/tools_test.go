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
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testDocsName        = "docs"
	testDocsDescription = "Docs MCP"
	testDocsURL         = "https://example.com/mcp?token=secret"
	testUpdatedDocsDesc = "Updated docs"
	testRegistryCommand = "mcporter"
)

func TestNewTools_ReturnsRegistryManagementTools(t *testing.T) {
	t.Parallel()

	tools := NewTools(NewFileStore(t.TempDir()), " "+testAppName+" ")
	names := make(map[string]struct{}, len(tools))
	for _, tl := range tools {
		names[tl.Declaration().Name] = struct{}{}
	}

	require.Contains(t, names, ToolAdd)
	require.Contains(t, names, ToolUpdate)
	require.Contains(t, names, ToolRemove)
	require.Contains(t, names, ToolList)
	require.Contains(t, names, ToolTest)
}

func TestRegistryTools_ManageVisibleEntries(t *testing.T) {
	t.Parallel()

	tools := registryTools{
		store:   NewFileStore(t.TempDir()),
		appName: testAppName,
	}
	ctx := testInvocationContext()

	added, err := tools.add(ctx, upsertInput{
		Name:        testDocsName,
		Scope:       string(ScopeChat),
		Description: testDocsDescription,
		ServerURL:   testDocsURL,
		Headers: map[string]string{
			"Authorization": "Bearer secret",
		},
	})
	require.NoError(t, err)
	require.Equal(t, ScopeChat, added.Scope)
	require.Equal(t, testDocsName, added.Selector)
	require.Equal(t, testDocsDescription, added.Description)
	require.True(t, added.HasSensitiveValues)
	require.NotContains(t, added.ServerURL, "secret")

	listed, err := tools.list(ctx, listInput{})
	require.NoError(t, err)
	require.Len(t, listed.Servers, 1)
	require.Equal(t, testDocsName, listed.Servers[0].Name)

	visible, err := tools.test(ctx, testInput{Name: testDocsName})
	require.NoError(t, err)
	require.True(t, visible.Accessible)
	require.Equal(t, testDocsName, visible.Selector)
	require.Contains(t, visible.Message, "mcp_list_tools")

	notInUserScope, err := tools.test(ctx, testInput{
		Name:  testDocsName,
		Scope: string(ScopeUser),
	})
	require.NoError(t, err)
	require.False(t, notInUserScope.Accessible)

	updated, err := tools.update(ctx, upsertInput{
		Name:             testDocsName,
		Scope:            string(ScopeChat),
		Description:      testUpdatedDocsDesc,
		Transport:        transportStdio,
		Command:          testRegistryCommand,
		Args:             []string{"serve"},
		TimeoutMS:        1000,
		ClearServerURL:   true,
		ClearHeaders:     true,
		ClearDescription: true,
	})
	require.NoError(t, err)
	require.Equal(t, testRegistryCommand, updated.Command)
	require.Equal(t, transportStdio, updated.Transport)
	require.Equal(t, testUpdatedDocsDesc, updated.Description)
	require.False(t, updated.HasSensitiveValues)

	removed, err := tools.remove(ctx, removeInput{
		Name:  testDocsName,
		Scope: string(ScopeChat),
	})
	require.NoError(t, err)
	require.True(t, removed.Removed)
	require.Equal(t, testDocsName, removed.Name)
	require.Equal(t, string(ScopeChat), removed.Scope)

	missing, err := tools.test(ctx, testInput{Name: testDocsName})
	require.NoError(t, err)
	require.False(t, missing.Accessible)
}

func TestRegistryTools_ErrorPaths(t *testing.T) {
	t.Parallel()

	tools := registryTools{
		store:   NewFileStore(t.TempDir()),
		appName: testAppName,
	}
	ctx := testInvocationContext()

	_, err := tools.list(context.Background(), listInput{})
	require.ErrorIs(t, err, errRegistryContextUnavailable)

	_, err = tools.add(context.Background(), upsertInput{
		Name:      testDocsName,
		ServerURL: "https://example.com/mcp",
	})
	require.ErrorIs(t, err, errRegistryContextUnavailable)

	_, err = tools.remove(context.Background(), removeInput{
		Name: testDocsName,
	})
	require.ErrorIs(t, err, errRegistryContextUnavailable)

	_, err = tools.test(context.Background(), testInput{
		Name: testDocsName,
	})
	require.ErrorIs(t, err, errRegistryContextUnavailable)

	_, err = tools.add(ctx, upsertInput{Name: testDocsName})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported MCP transport")

	_, err = tools.update(ctx, upsertInput{
		Name:      testDocsName,
		ServerURL: "https://example.com/mcp",
	})
	require.ErrorIs(t, err, errRegistryEntryNotFound)

	_, err = tools.remove(ctx, removeInput{Name: "bad\nname"})
	require.Error(t, err)

	_, err = tools.test(ctx, testInput{Name: "bad\nname"})
	require.Error(t, err)
}
