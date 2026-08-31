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
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec/processconnect"
)

type testProcessHandler struct {
	processconnect.UnimplementedProcessHandler
	list       func(context.Context, *connect.Request[process.ListRequest]) (*connect.Response[process.ListResponse], error)
	connect    func(context.Context, *connect.Request[process.ConnectRequest], *connect.ServerStream[process.ConnectResponse]) error
	start      func(context.Context, *connect.Request[process.StartRequest], *connect.ServerStream[process.StartResponse]) error
	sendInput  func(context.Context, *connect.Request[process.SendInputRequest]) (*connect.Response[process.SendInputResponse], error)
	sendSignal func(context.Context, *connect.Request[process.SendSignalRequest]) (*connect.Response[process.SendSignalResponse], error)
	closeStdin func(context.Context, *connect.Request[process.CloseStdinRequest]) (*connect.Response[process.CloseStdinResponse], error)
}

func (h *testProcessHandler) List(
	ctx context.Context,
	req *connect.Request[process.ListRequest],
) (*connect.Response[process.ListResponse], error) {
	if h.list == nil {
		return h.UnimplementedProcessHandler.List(ctx, req)
	}
	return h.list(ctx, req)
}

func (h *testProcessHandler) Connect(
	ctx context.Context,
	req *connect.Request[process.ConnectRequest],
	stream *connect.ServerStream[process.ConnectResponse],
) error {
	if h.connect == nil {
		return h.UnimplementedProcessHandler.Connect(ctx, req, stream)
	}
	return h.connect(ctx, req, stream)
}

func (h *testProcessHandler) Start(
	ctx context.Context,
	req *connect.Request[process.StartRequest],
	stream *connect.ServerStream[process.StartResponse],
) error {
	return h.start(ctx, req, stream)
}

func (h *testProcessHandler) SendInput(
	ctx context.Context,
	req *connect.Request[process.SendInputRequest],
) (*connect.Response[process.SendInputResponse], error) {
	if h.sendInput == nil {
		return h.UnimplementedProcessHandler.SendInput(ctx, req)
	}
	return h.sendInput(ctx, req)
}

func (h *testProcessHandler) SendSignal(
	ctx context.Context,
	req *connect.Request[process.SendSignalRequest],
) (*connect.Response[process.SendSignalResponse], error) {
	if h.sendSignal == nil {
		return h.UnimplementedProcessHandler.SendSignal(ctx, req)
	}
	return h.sendSignal(ctx, req)
}

func (h *testProcessHandler) CloseStdin(
	ctx context.Context,
	req *connect.Request[process.CloseStdinRequest],
) (*connect.Response[process.CloseStdinResponse], error) {
	if h.closeStdin == nil {
		return h.UnimplementedProcessHandler.CloseStdin(ctx, req)
	}
	return h.closeStdin(ctx, req)
}

func newTestClient(
	t *testing.T,
	handler processconnect.ProcessHandler,
	headers http.Header,
) *Client {
	t.Helper()
	_, rpcHandler := processconnect.NewProcessHandler(handler)
	server := httptest.NewServer(rpcHandler)
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, server.Client(), headers)
	require.NoError(t, err)
	return client
}

func blockingProcessHandler(
	t *testing.T,
	stdinClosed chan struct{},
) *testProcessHandler {
	t.Helper()
	handler := &testProcessHandler{}
	handler.start = func(
		ctx context.Context,
		_ *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		if err := stream.Send(startEvent(77)); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	handler.sendInput = func(
		_ context.Context,
		_ *connect.Request[process.SendInputRequest],
	) (*connect.Response[process.SendInputResponse], error) {
		return connect.NewResponse(&process.SendInputResponse{}), nil
	}
	var closeOnce sync.Once
	handler.closeStdin = func(
		_ context.Context,
		_ *connect.Request[process.CloseStdinRequest],
	) (*connect.Response[process.CloseStdinResponse], error) {
		if stdinClosed != nil {
			closeOnce.Do(func() { close(stdinClosed) })
		}
		return connect.NewResponse(&process.CloseStdinResponse{}), nil
	}
	return handler
}

func unexpectedSendSignal(t *testing.T) func(
	context.Context,
	*connect.Request[process.SendSignalRequest],
) (*connect.Response[process.SendSignalResponse], error) {
	t.Helper()
	return func(
		context.Context,
		*connect.Request[process.SendSignalRequest],
	) (*connect.Response[process.SendSignalResponse], error) {
		t.Error("Run must not send an implicit process signal")
		return connect.NewResponse(&process.SendSignalResponse{}), nil
	}
}

func assertConnectTimeout(
	t *testing.T,
	header http.Header,
	minimum time.Duration,
	maximum time.Duration,
) {
	t.Helper()
	raw := header.Get("Connect-Timeout-Ms")
	require.NotEmpty(t, raw)
	milliseconds, err := strconv.ParseInt(raw, 10, 64)
	require.NoError(t, err)
	timeout := time.Duration(milliseconds) * time.Millisecond
	assert.GreaterOrEqual(t, timeout, minimum)
	assert.LessOrEqual(t, timeout, maximum)
}

func startEvent(pid uint32) *process.StartResponse {
	return &process.StartResponse{Event: &process.ProcessEvent{
		Event: &process.ProcessEvent_Start{
			Start: &process.ProcessEvent_StartEvent{Pid: pid},
		},
	}}
}

func stdoutEvent(output []byte) *process.ProcessEvent {
	return &process.ProcessEvent{Event: &process.ProcessEvent_Data{
		Data: &process.ProcessEvent_DataEvent{
			Output: &process.ProcessEvent_DataEvent_Stdout{Stdout: output},
		},
	}}
}

func stderrEvent(output []byte) *process.ProcessEvent {
	return &process.ProcessEvent{Event: &process.ProcessEvent_Data{
		Data: &process.ProcessEvent_DataEvent{
			Output: &process.ProcessEvent_DataEvent_Stderr{Stderr: output},
		},
	}}
}

func keepAliveEvent() *process.ProcessEvent {
	return &process.ProcessEvent{Event: &process.ProcessEvent_Keepalive{
		Keepalive: &process.ProcessEvent_KeepAlive{},
	}}
}

func endEvent(exitCode int32) *process.ProcessEvent {
	errMessage := "exit status"
	return &process.ProcessEvent{Event: &process.ProcessEvent_End{
		End: &process.ProcessEvent_EndEvent{
			ExitCode: exitCode,
			Exited:   true,
			Status:   "exit status",
			Error:    &errMessage,
		},
	}}
}
