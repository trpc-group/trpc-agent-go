//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package envdprocess

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec/processconnect"
)

func TestNewClientRejectsInvalidBaseURL(t *testing.T) {
	_, err := NewClient("not-a-url", nil, nil)
	require.Error(t, err)
}

func TestRunMapsRequestInputAndEvents(t *testing.T) {
	stdinClosed := make(chan struct{})
	startRequests := make(chan *connect.Request[process.StartRequest], 1)
	inputRequests := make(chan *connect.Request[process.SendInputRequest], 1)
	closeRequests := make(chan *connect.Request[process.CloseStdinRequest], 1)
	handler := &testProcessHandler{}
	handler.start = func(
		_ context.Context,
		req *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		startRequests <- req
		if err := stream.Send(startEvent(42)); err != nil {
			return err
		}
		<-stdinClosed
		for _, event := range []*process.ProcessEvent{
			stdoutEvent([]byte("before\n__E2B_STDOUT_END__\n")),
			keepAliveEvent(),
			stdoutEvent([]byte("after\n")),
			stderrEvent([]byte("warning\n")),
			endEvent(7),
		} {
			if err := stream.Send(&process.StartResponse{Event: event}); err != nil {
				return err
			}
		}
		return nil
	}
	handler.sendInput = func(
		_ context.Context,
		req *connect.Request[process.SendInputRequest],
	) (*connect.Response[process.SendInputResponse], error) {
		inputRequests <- req
		return connect.NewResponse(&process.SendInputResponse{}), nil
	}
	handler.closeStdin = func(
		_ context.Context,
		req *connect.Request[process.CloseStdinRequest],
	) (*connect.Response[process.CloseStdinResponse], error) {
		closeRequests <- req
		close(stdinClosed)
		return connect.NewResponse(&process.CloseStdinResponse{}), nil
	}

	client := newTestClient(t, handler, http.Header{
		"X-Access-Token":           []string{"access-token"},
		"E2B-Traffic-Access-Token": []string{"traffic-token"},
	})
	result, err := client.Run(context.Background(), Request{
		Cmd:     "/usr/bin/python3",
		Args:    []string{"script.py", "a b"},
		Envs:    map[string]string{"A": "1"},
		Cwd:     "/tmp/work",
		User:    "sandbox-user",
		Stdin:   "input\n",
		Timeout: time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, "before\n__E2B_STDOUT_END__\nafter\n", result.Stdout)
	assert.Equal(t, "warning\n", result.Stderr)
	assert.Equal(t, 7, result.ExitCode)
	assert.False(t, result.TimedOut)

	startReq := <-startRequests
	require.NotNil(t, startReq.Msg.Process)
	assert.Equal(t, "/usr/bin/python3", startReq.Msg.Process.Cmd)
	assert.Equal(t, []string{"script.py", "a b"}, startReq.Msg.Process.Args)
	assert.Equal(t, map[string]string{"A": "1"}, startReq.Msg.Process.Envs)
	assert.Equal(t, "/tmp/work", startReq.Msg.Process.GetCwd())
	assert.Nil(t, startReq.Msg.Pty)
	assert.True(t, startReq.Msg.GetStdin())
	assert.NotEmpty(t, startReq.Msg.GetTag())
	assert.Equal(t, "access-token", startReq.Header().Get("X-Access-Token"))
	assert.Equal(t, "Basic c2FuZGJveC11c2VyOg==", startReq.Header().Get("Authorization"))
	assert.Equal(
		t, "traffic-token",
		startReq.Header().Get("E2B-Traffic-Access-Token"),
	)

	inputReq := <-inputRequests
	assert.Equal(t, uint32(42), inputReq.Msg.Process.GetPid())
	assert.Equal(t, []byte("input\n"), inputReq.Msg.Input.GetStdin())
	assert.Equal(t, "access-token", inputReq.Header().Get("X-Access-Token"))

	closeReq := <-closeRequests
	assert.Equal(t, uint32(42), closeReq.Msg.Process.GetPid())
}

func TestRunWithEmptyStdinDoesNotOpenStdin(t *testing.T) {
	startRequests := make(chan *connect.Request[process.StartRequest], 1)
	handler := &testProcessHandler{}
	handler.start = func(
		_ context.Context,
		req *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		startRequests <- req
		if err := stream.Send(startEvent(11)); err != nil {
			return err
		}
		return stream.Send(&process.StartResponse{Event: endEvent(0)})
	}
	handler.sendInput = func(
		context.Context,
		*connect.Request[process.SendInputRequest],
	) (*connect.Response[process.SendInputResponse], error) {
		t.Error("SendInput must not be called for empty stdin")
		return connect.NewResponse(&process.SendInputResponse{}), nil
	}
	handler.closeStdin = func(
		context.Context,
		*connect.Request[process.CloseStdinRequest],
	) (*connect.Response[process.CloseStdinResponse], error) {
		t.Error("CloseStdin must not be called for empty stdin")
		return connect.NewResponse(&process.CloseStdinResponse{}), nil
	}

	client := newTestClient(t, handler, nil)
	result, err := client.Run(context.Background(), Request{Cmd: "true"})
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	startReq := <-startRequests
	assert.False(t, startReq.Msg.GetStdin())
	assert.Empty(t, startReq.Header().Get("Authorization"))
}

func TestRunTimeoutKillsKnownPID(t *testing.T) {
	signals := make(chan *connect.Request[process.SendSignalRequest], 1)
	handler := blockingProcessHandler(t, signals, nil)
	client := newTestClient(t, handler, nil)

	result, err := client.Run(context.Background(), Request{
		Cmd:     "sleep",
		Args:    []string{"60"},
		Timeout: 50 * time.Millisecond,
	})
	require.NoError(t, err)
	assert.True(t, result.TimedOut)

	signalReq := <-signals
	assert.Equal(t, uint32(77), signalReq.Msg.Process.GetPid())
	assert.Equal(t, process.Signal_SIGNAL_SIGKILL, signalReq.Msg.Signal)
}

func TestRunCallerCancellationKillsKnownPID(t *testing.T) {
	stdinClosed := make(chan struct{})
	signals := make(chan *connect.Request[process.SendSignalRequest], 1)
	handler := blockingProcessHandler(t, signals, stdinClosed)
	client := newTestClient(t, handler, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		<-stdinClosed
		cancel()
		close(done)
	}()
	result, err := client.Run(ctx, Request{
		Cmd:   "cat",
		Stdin: "input",
	})
	<-done
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, result.TimedOut)

	signalReq := <-signals
	assert.Equal(t, uint32(77), signalReq.Msg.Process.GetPid())
}

func TestRunCancellationBeforeStartEventKillsByTag(t *testing.T) {
	registered := make(chan string, 1)
	registeredTag := make(chan string, 1)
	signals := make(chan *connect.Request[process.SendSignalRequest], 1)
	signalAttempts := 0
	handler := &testProcessHandler{}
	handler.start = func(
		ctx context.Context,
		req *connect.Request[process.StartRequest],
		_ *connect.ServerStream[process.StartResponse],
	) error {
		registered <- req.Msg.GetTag()
		<-ctx.Done()
		return ctx.Err()
	}
	handler.sendSignal = func(
		_ context.Context,
		req *connect.Request[process.SendSignalRequest],
	) (*connect.Response[process.SendSignalResponse], error) {
		signalAttempts++
		if signalAttempts < 3 {
			return nil, connect.NewError(
				connect.CodeNotFound, errors.New("process not registered yet"),
			)
		}
		signals <- req
		return connect.NewResponse(&process.SendSignalResponse{}), nil
	}

	client := newTestClient(t, handler, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		tag := <-registered
		registeredTag <- tag
		cancel()
	}()
	_, err := client.Run(ctx, Request{Cmd: "sleep", Args: []string{"60"}})
	require.ErrorIs(t, err, context.Canceled)

	signalReq := <-signals
	assert.Zero(t, signalReq.Msg.Process.GetPid())
	assert.Equal(t, <-registeredTag, signalReq.Msg.Process.GetTag())
	assert.Equal(t, process.Signal_SIGNAL_SIGKILL, signalReq.Msg.Signal)
	assert.Equal(t, 3, signalAttempts)
}

func TestRunStreamWithoutEndEventIsProtocolError(t *testing.T) {
	signals := make(chan *connect.Request[process.SendSignalRequest], 1)
	handler := &testProcessHandler{}
	handler.start = func(
		_ context.Context,
		_ *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		if err := stream.Send(startEvent(99)); err != nil {
			return err
		}
		return stream.Send(&process.StartResponse{
			Event: stdoutEvent([]byte("partial")),
		})
	}
	handler.sendSignal = func(
		_ context.Context,
		req *connect.Request[process.SendSignalRequest],
	) (*connect.Response[process.SendSignalResponse], error) {
		signals <- req
		return connect.NewResponse(&process.SendSignalResponse{}), nil
	}

	client := newTestClient(t, handler, nil)
	result, err := client.Run(context.Background(), Request{Cmd: "broken"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "without EndEvent")
	assert.Equal(t, "partial", result.Stdout)
	assert.Equal(t, uint32(99), (<-signals).Msg.Process.GetPid())
}

func TestRunTimeoutTreatsMissingProcessAsStopped(t *testing.T) {
	handler := &testProcessHandler{}
	handler.start = func(
		ctx context.Context,
		_ *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		if err := stream.Send(startEvent(88)); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	handler.sendSignal = func(
		context.Context,
		*connect.Request[process.SendSignalRequest],
	) (*connect.Response[process.SendSignalResponse], error) {
		return nil, connect.NewError(
			connect.CodeNotFound, errors.New("process already exited"),
		)
	}

	client := newTestClient(t, handler, nil)
	result, err := client.Run(context.Background(), Request{
		Cmd:     "sleep",
		Timeout: 50 * time.Millisecond,
	})
	require.NoError(t, err)
	assert.True(t, result.TimedOut)
}

type testProcessHandler struct {
	processconnect.UnimplementedProcessHandler
	start      func(context.Context, *connect.Request[process.StartRequest], *connect.ServerStream[process.StartResponse]) error
	sendInput  func(context.Context, *connect.Request[process.SendInputRequest]) (*connect.Response[process.SendInputResponse], error)
	sendSignal func(context.Context, *connect.Request[process.SendSignalRequest]) (*connect.Response[process.SendSignalResponse], error)
	closeStdin func(context.Context, *connect.Request[process.CloseStdinRequest]) (*connect.Response[process.CloseStdinResponse], error)
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
	client.cleanupTimeout = 250 * time.Millisecond
	return client
}

func blockingProcessHandler(
	t *testing.T,
	signals chan<- *connect.Request[process.SendSignalRequest],
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
	handler.sendSignal = func(
		_ context.Context,
		req *connect.Request[process.SendSignalRequest],
	) (*connect.Response[process.SendSignalResponse], error) {
		signals <- req
		return connect.NewResponse(&process.SendSignalResponse{}), nil
	}
	return handler
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

func TestRunStdinFailureKillsProcess(t *testing.T) {
	signals := make(chan *connect.Request[process.SendSignalRequest], 1)
	handler := &testProcessHandler{}
	handler.start = func(
		ctx context.Context,
		_ *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		if err := stream.Send(startEvent(123)); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	handler.sendInput = func(
		context.Context,
		*connect.Request[process.SendInputRequest],
	) (*connect.Response[process.SendInputResponse], error) {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("down"))
	}
	handler.sendSignal = func(
		_ context.Context,
		req *connect.Request[process.SendSignalRequest],
	) (*connect.Response[process.SendSignalResponse], error) {
		signals <- req
		return connect.NewResponse(&process.SendSignalResponse{}), nil
	}

	client := newTestClient(t, handler, nil)
	_, err := client.Run(context.Background(), Request{
		Cmd:   "cat",
		Stdin: "input",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write stdin")
	assert.Equal(t, uint32(123), (<-signals).Msg.Process.GetPid())
}
