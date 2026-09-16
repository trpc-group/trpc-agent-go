//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package envdprocess

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
)

func TestWithEnvdVersionConfiguresCloseStdinCapability(t *testing.T) {
	tests := []struct {
		version   string
		supported bool
	}{
		{version: "", supported: true},
		{version: "0.2.10", supported: false},
		{version: "0.5.1", supported: false},
		{version: "0.5.2", supported: true},
		{version: "0.5.2-rc.1", supported: false},
		{version: "v0.6.0", supported: true},
		{version: "1.0.0+build", supported: true},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			client, err := NewClient(
				"https://envd.example",
				nil,
				nil,
				WithEnvdVersion(tt.version),
			)
			require.NoError(t, err)
			assert.Equal(t, tt.supported, client.supportsCloseStdin)
		})
	}
}

func TestNewClientRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name      string
		option    ClientOption
		wantError string
	}{
		{
			name:      "InvalidEnvdVersion",
			option:    WithEnvdVersion("development"),
			wantError: "invalid envd version",
		},
		{
			name:      "NegativeStdoutLimit",
			option:    WithStdoutCaptureLimit(-1),
			wantError: "stdout capture limit must not be negative",
		},
		{
			name:      "NegativeStderrLimit",
			option:    WithStderrCaptureLimit(-1),
			wantError: "stderr capture limit must not be negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient(
				"https://envd.example", nil, nil, tt.option,
			)
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestOptionsIgnoreNil(t *testing.T) {
	handler := &testProcessHandler{}
	handler.start = func(
		_ context.Context,
		_ *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		if err := stream.Send(startEvent(1)); err != nil {
			return err
		}
		return stream.Send(&process.StartResponse{Event: endEvent(0)})
	}
	var clientOption ClientOption
	client := newTestClient(t, handler, nil, clientOption)
	var launchOption LaunchOption

	proc, err := client.Start(
		context.Background(), Request{Cmd: "true"}, launchOption,
	)
	require.NoError(t, err)
	proc.Disconnect()

	_, err = client.Run(
		context.Background(), Request{Cmd: "true"}, launchOption,
	)
	require.NoError(t, err)
}
