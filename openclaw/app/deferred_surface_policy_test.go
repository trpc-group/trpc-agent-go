//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestDefaultedDirectToolSurfaceNames(t *testing.T) {
	t.Parallel()

	got := directToolSurfaceNames(
		true,
		[]string{
			configKeyExecCommand,
			configKeyMessage,
			" ",
			configKeyMessage,
		},
	)
	require.Equal(t, []string{
		configKeyExecCommand,
		configKeyWriteStdin,
		configKeyKillSession,
		configKeyMessage,
	}, got)
}

func TestDirectToolSurfaceNamesCanSkipDefaults(t *testing.T) {
	t.Parallel()

	got := directToolSurfaceNames(false, []string{
		configKeyMessage,
		configKeyMessage,
	})
	require.Equal(t, []string{configKeyMessage}, got)
}

func TestResolveDeferredToolSurfaceKeepsDefaultDirectTools(
	t *testing.T,
) {
	t.Parallel()

	enabled, direct, err := resolveDeferredToolSurface(
		agentConfig{DeferToolSurface: true},
		[]tool.Tool{
			stubTool{name: configKeyMessage},
			stubTool{name: configKeyExecCommand},
			stubTool{name: configKeyWriteStdin},
			stubTool{name: configKeyKillSession},
		},
		nil,
	)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, []string{
		configKeyExecCommand,
		configKeyWriteStdin,
		configKeyKillSession,
	}, testToolNames(direct))
}

func TestResolveDeferredToolSurfaceCanDisableDefaultDirectTools(
	t *testing.T,
) {
	t.Parallel()

	enabled, direct, err := resolveDeferredToolSurface(
		agentConfig{
			DeferToolSurface:                   true,
			DeferToolSurfaceDefaultDirectTools: boolPtr(false),
		},
		[]tool.Tool{
			stubTool{name: configKeyMessage},
			stubTool{name: configKeyExecCommand},
			stubTool{name: configKeyWriteStdin},
			stubTool{name: configKeyKillSession},
		},
		nil,
	)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Empty(t, testToolNames(direct))
}

func testToolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, toolDeclName(t))
	}
	return names
}
