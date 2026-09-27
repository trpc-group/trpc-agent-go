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
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	processrpc "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
)

func TestClientListAndKill(t *testing.T) {
	handler := &testProcessHandler{}
	handler.list = func(
		_ context.Context,
		req *connect.Request[processrpc.ListRequest],
	) (*connect.Response[processrpc.ListResponse], error) {
		assert.Equal(t, "token", req.Header().Get("X-Access-Token"))
		cwd := "/tmp/list"
		tag := "listed"
		return connect.NewResponse(&processrpc.ListResponse{
			Processes: []*processrpc.ProcessInfo{{
				Pid: 203,
				Tag: &tag,
				Config: &processrpc.ProcessConfig{
					Cmd:  "/bin/sh",
					Args: []string{"-c", "sleep 60"},
					Envs: map[string]string{"MODE": "test"},
					Cwd:  &cwd,
				},
			}},
		}), nil
	}
	handler.sendSignal = func(
		_ context.Context,
		req *connect.Request[processrpc.SendSignalRequest],
	) (*connect.Response[processrpc.SendSignalResponse], error) {
		assert.Equal(t, uint32(203), req.Msg.Process.GetPid())
		return connect.NewResponse(&processrpc.SendSignalResponse{}), nil
	}

	client := newTestClient(t, handler, http.Header{
		"X-Access-Token": []string{"token"},
	})
	processes, err := client.List(context.Background())
	require.NoError(t, err)
	require.Len(t, processes, 1)
	assert.Equal(t, ProcessInfo{
		PID:  203,
		Tag:  "listed",
		Cmd:  "/bin/sh",
		Args: []string{"-c", "sleep 60"},
		Envs: map[string]string{"MODE": "test"},
		Cwd:  "/tmp/list",
	}, processes[0])

	killed, err := client.Kill(context.Background(), 203)
	require.NoError(t, err)
	assert.True(t, killed)
}

func TestClientKillMissingProcessReturnsFalse(t *testing.T) {
	handler := &testProcessHandler{}
	handler.sendSignal = func(
		context.Context,
		*connect.Request[processrpc.SendSignalRequest],
	) (*connect.Response[processrpc.SendSignalResponse], error) {
		return nil, connect.NewError(
			connect.CodeNotFound, errors.New("process is not running"),
		)
	}
	client := newTestClient(t, handler, nil)

	killed, err := client.Kill(context.Background(), 404)
	require.NoError(t, err)
	assert.False(t, killed)
}

func TestClientOperationsRejectInvalidInput(t *testing.T) {
	client := newTestClient(t, &testProcessHandler{}, nil)

	_, err := client.Connect(nil, 1)
	require.ErrorContains(t, err, "nil context")

	_, err = client.Kill(context.Background(), 0)
	require.ErrorContains(t, err, "pid is zero")

	err = client.SendInput(context.Background(), 0, []byte("input"))
	require.ErrorContains(t, err, "pid is zero")

	err = client.CloseStdin(context.Background(), 0)
	require.ErrorContains(t, err, "pid is zero")

	var uninitialized *Client
	_, err = uninitialized.Kill(context.Background(), 1)
	require.ErrorContains(t, err, "client is not initialized")
}

func TestClientCloseStdinRejectsUnsupportedEnvd(t *testing.T) {
	handler := &testProcessHandler{}
	handler.closeStdin = func(
		context.Context,
		*connect.Request[processrpc.CloseStdinRequest],
	) (*connect.Response[processrpc.CloseStdinResponse], error) {
		t.Fatal("CloseStdin RPC must not be sent to unsupported envd")
		return nil, nil
	}
	client := newTestClient(
		t,
		handler,
		nil,
		WithEnvdVersion("0.2.10"),
	)

	err := client.CloseStdin(context.Background(), 1)
	require.ErrorContains(t, err, "close stdin requires envd >= 0.5.2")
}
