//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package browser

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	mcptool "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

const testBrowserMCPServerDir = "./testdata/browser_mcp_server"

func TestMCPProfileDriver_Lifecycle(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	require.NoError(t, err)

	drv := newMCPProfileDriver(resolvedProfile{
		Name: defaultProfileName,
		Connection: mcptool.ConnectionConfig{
			Transport: transportStdio,
			Command:   goBinary,
			Args: []string{
				"run",
				testBrowserMCPServerDir,
			},
			Timeout: time.Minute,
		},
	})

	status, err := drv.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, stateStopped, status.State)

	status, err = drv.Start(context.Background())
	require.NoError(t, err)
	require.Equal(t, stateReady, status.State)
	require.Equal(t, 1, status.ToolCount)

	status, err = drv.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, stateReady, status.State)
	require.Equal(t, 1, status.ToolCount)

	raw, err := drv.Call(context.Background(), mcpToolTabs, map[string]any{
		"action": tabActionList,
	})
	require.NoError(t, err)
	require.Contains(t, extractText(raw), "Example")

	err = drv.Stop()
	if err != nil {
		require.Contains(t, err.Error(), "process already finished")
	}
}
