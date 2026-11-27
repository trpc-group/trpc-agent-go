//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestProtocolClientRejectsUnsupportedCapabilities(t *testing.T) {
	t.Parallel()

	client := protocolClient{}
	_, err := client.ReadTextFile(context.Background(), acpsdk.ReadTextFileRequest{})
	requireACPMethodNotFound(t, err, acpsdk.ClientMethodFsReadTextFile)

	_, err = client.WriteTextFile(context.Background(), acpsdk.WriteTextFileRequest{})
	requireACPMethodNotFound(t, err, acpsdk.ClientMethodFsWriteTextFile)

	_, err = client.CreateTerminal(context.Background(), acpsdk.CreateTerminalRequest{})
	requireACPMethodNotFound(t, err, acpsdk.ClientMethodTerminalCreate)

	_, err = client.KillTerminal(context.Background(), acpsdk.KillTerminalRequest{})
	requireACPMethodNotFound(t, err, acpsdk.ClientMethodTerminalKill)

	_, err = client.TerminalOutput(context.Background(), acpsdk.TerminalOutputRequest{})
	requireACPMethodNotFound(t, err, acpsdk.ClientMethodTerminalOutput)

	_, err = client.ReleaseTerminal(context.Background(), acpsdk.ReleaseTerminalRequest{})
	requireACPMethodNotFound(t, err, acpsdk.ClientMethodTerminalRelease)

	_, err = client.WaitForTerminalExit(context.Background(), acpsdk.WaitForTerminalExitRequest{})
	requireACPMethodNotFound(t, err, acpsdk.ClientMethodTerminalWaitForExit)
}

func TestProtocolClientDefaults(t *testing.T) {
	t.Parallel()

	client := protocolClient{}
	response, err := client.RequestPermission(context.Background(), acpsdk.RequestPermissionRequest{})
	require.NoError(t, err)
	require.NotNil(t, response.Outcome.Cancelled)
	require.Equal(t, "cancelled", response.Outcome.Cancelled.Outcome)

	require.NoError(t, client.SessionUpdate(context.Background(), acpsdk.SessionNotification{}))
}

func requireACPMethodNotFound(t *testing.T, err error, method string) {
	t.Helper()
	require.Error(t, err)
	requestErr, ok := err.(*acpsdk.RequestError)
	require.True(t, ok)
	require.Equal(t, -32601, requestErr.Code)
	require.Equal(t, map[string]any{"method": method}, requestErr.Data)
}
