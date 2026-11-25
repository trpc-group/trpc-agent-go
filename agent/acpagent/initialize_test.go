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
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewInitializeRequestAdvertisesRequiredCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		terminalAuth bool
	}{
		{name: "disabled"},
		{name: "enabled", terminalAuth: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := newInitializeRequest(test.terminalAuth)
			require.Equal(t, supportedProtocolVersion, request.ProtocolVersion)
			require.Equal(t, test.terminalAuth, request.ClientCapabilities.Auth.Terminal)
			require.False(t, request.ClientCapabilities.Fs.ReadTextFile)
			require.False(t, request.ClientCapabilities.Fs.WriteTextFile)
			require.False(t, request.ClientCapabilities.Terminal)
			require.Nil(t, request.ClientCapabilities.Elicitation)
			require.NotNil(t, request.ClientCapabilities.Session)
			require.NotNil(t, request.ClientCapabilities.Session.ConfigOptions)
			require.NotNil(t, request.ClientCapabilities.Session.ConfigOptions.Boolean)
		})
	}
}

func TestConnectWithoutTerminalAuthHandlerAdvertisesUnsupported(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentForTerminalAuth(t, "wait", io.Discard, false)
	process, err := acpAgent.connections.connect(context.Background())
	require.NoError(t, err)
	require.NotNil(t, process)
	require.NoError(t, acpAgent.Close())
}

func TestCaptureACPAgentDescriptorExtractsMetadata(t *testing.T) {
	t.Parallel()

	response := helperInitializeResponse()
	descriptor := captureACPAgentDescriptor(response)
	require.True(t, descriptor.agentCapabilities.LoadSession)
	require.Len(t, descriptor.authMethods, 2)
	require.Equal(t, "helper-agent", descriptor.agentInfo.Name)

	response.AgentInfo.Name = "mutated"
	require.Equal(t, "helper-agent", descriptor.agentInfo.Name)

	response.AgentInfo = nil
	require.Nil(t, captureACPAgentDescriptor(response).agentInfo)
}

func TestConnectRejectsUnsupportedProtocolVersion(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgent(t, "bad_protocol", io.Discard)
	process, err := acpAgent.connections.connect(context.Background())
	require.Nil(t, process)
	require.ErrorIs(t, err, errUnsupportedProtocolVersion)
	require.Equal(t, testConnectionSnapshot{
		state: lifecycleStopped,
	}, snapshotConnectionManager(acpAgent.connections))
}

func TestConnectReturnsInitializeError(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgent(t, "initialize_error", io.Discard)
	process, err := acpAgent.connections.connect(context.Background())
	require.Nil(t, process)
	require.ErrorContains(t, err, "initialize connection")
	require.ErrorContains(t, err, "initialize failed")
	require.ErrorContains(t, err, "acp helper stderr")
	require.Equal(t, testConnectionSnapshot{
		state: lifecycleStopped,
	}, snapshotConnectionManager(acpAgent.connections))
}

func TestConnectInitializeHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgent(t, "hang_initialize", io.Discard)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	process, err := acpAgent.connections.connect(ctx)
	require.Nil(t, process)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, testConnectionSnapshot{
		state: lifecycleStopped,
	}, snapshotConnectionManager(acpAgent.connections))
}
