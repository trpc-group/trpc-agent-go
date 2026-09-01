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
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
)

func TestRunRejectsInvalidInput(t *testing.T) {
	client := newTestClient(t, &testProcessHandler{}, nil)

	_, err := client.Run(nil, Request{Cmd: "true"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil context")

	var nilClient *Client
	_, err = nilClient.Run(context.Background(), Request{Cmd: "true"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client is not initialized")

	_, err = client.Run(context.Background(), Request{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "command is empty")
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
	assert.Equal(t, uint32(42), result.PID)
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
	assert.Empty(t, startReq.Msg.GetTag())
	assertConnectTimeout(t, startReq.Header(), 500*time.Millisecond, time.Second)
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
	assertConnectTimeout(
		t, startReq.Header(), 59*time.Second, defaultProcessTimeout,
	)
}

func TestRunNegativeTimeoutUsesDefaultRemoteProcessDeadline(t *testing.T) {
	startRequests := make(chan *connect.Request[process.StartRequest], 1)
	handler := &testProcessHandler{}
	handler.start = func(
		_ context.Context,
		req *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		startRequests <- req
		if err := stream.Send(startEvent(12)); err != nil {
			return err
		}
		return stream.Send(&process.StartResponse{Event: endEvent(0)})
	}

	client := newTestClient(t, handler, nil)
	_, err := client.Run(context.Background(), Request{
		Cmd:     "true",
		Timeout: -time.Second,
	})
	require.NoError(t, err)
	assertConnectTimeout(
		t, (<-startRequests).Header(), 59*time.Second, defaultProcessTimeout,
	)
}

func TestRunTimeoutSetsRemoteProcessDeadline(t *testing.T) {
	timeoutHeaders := make(chan http.Header, 1)
	handler := blockingProcessHandler(t, nil)
	handler.start = func(
		ctx context.Context,
		req *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		timeoutHeaders <- req.Header()
		if err := stream.Send(startEvent(77)); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	handler.sendSignal = unexpectedSendSignal(t)
	client := newTestClient(t, handler, nil)

	result, err := client.Run(context.Background(), Request{
		Cmd:     "sleep",
		Args:    []string{"60"},
		Timeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	assert.True(t, result.TimedOut)
	assertConnectTimeout(
		t, <-timeoutHeaders, 50*time.Millisecond, 100*time.Millisecond,
	)
}

func TestRunCallerCancellationDisconnectsWithoutKillingProcess(t *testing.T) {
	stdinClosed := make(chan struct{})
	startRequests := make(chan *connect.Request[process.StartRequest], 1)
	handler := blockingProcessHandler(t, stdinClosed)
	originalStart := handler.start
	handler.start = func(
		ctx context.Context,
		req *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		startRequests <- req
		return originalStart(ctx, req, stream)
	}
	handler.sendSignal = unexpectedSendSignal(t)
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
	assertConnectTimeout(
		t, (<-startRequests).Header(), 59*time.Second, defaultProcessTimeout,
	)
}

func TestStartCallerCancellationCancelsInitialSendInput(t *testing.T) {
	inputStarted := make(chan struct{})
	handler := &testProcessHandler{}
	handler.start = func(
		ctx context.Context,
		_ *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		if err := stream.Send(startEvent(78)); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	handler.sendInput = func(
		ctx context.Context,
		_ *connect.Request[process.SendInputRequest],
	) (*connect.Response[process.SendInputResponse], error) {
		close(inputStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	handler.closeStdin = func(
		context.Context,
		*connect.Request[process.CloseStdinRequest],
	) (*connect.Response[process.CloseStdinResponse], error) {
		t.Error("CloseStdin must not be called after SendInput fails")
		return connect.NewResponse(&process.CloseStdinResponse{}), nil
	}

	client := newTestClient(t, handler, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	type startResult struct {
		proc *Process
		err  error
	}
	resultCh := make(chan startResult, 1)
	go func() {
		proc, err := client.Start(ctx, Request{Cmd: "cat", Stdin: "input"})
		resultCh <- startResult{proc: proc, err: err}
	}()

	<-inputStarted
	cancel()
	select {
	case result := <-resultCh:
		require.NotNil(t, result.proc)
		assert.Equal(t, uint32(78), result.proc.PID())
		require.Error(t, result.err)
		assert.ErrorIs(t, result.err, context.Canceled)
		assert.Contains(t, result.err.Error(), "write stdin")
	case <-time.After(time.Second):
		t.Fatal("Start did not return after caller cancellation")
	}
}

func TestStartCallerCancellationCancelsInitialCloseStdin(t *testing.T) {
	closeStarted := make(chan struct{})
	handler := &testProcessHandler{}
	handler.start = func(
		ctx context.Context,
		_ *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		if err := stream.Send(startEvent(79)); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	handler.sendInput = func(
		context.Context,
		*connect.Request[process.SendInputRequest],
	) (*connect.Response[process.SendInputResponse], error) {
		return connect.NewResponse(&process.SendInputResponse{}), nil
	}
	handler.closeStdin = func(
		ctx context.Context,
		_ *connect.Request[process.CloseStdinRequest],
	) (*connect.Response[process.CloseStdinResponse], error) {
		close(closeStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	client := newTestClient(t, handler, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	type startResult struct {
		proc *Process
		err  error
	}
	resultCh := make(chan startResult, 1)
	go func() {
		proc, err := client.Start(ctx, Request{Cmd: "cat", Stdin: "input"})
		resultCh <- startResult{proc: proc, err: err}
	}()

	<-closeStarted
	cancel()
	select {
	case result := <-resultCh:
		require.NotNil(t, result.proc)
		assert.Equal(t, uint32(79), result.proc.PID())
		require.Error(t, result.err)
		assert.ErrorIs(t, result.err, context.Canceled)
		assert.Contains(t, result.err.Error(), "close stdin")
	case <-time.After(time.Second):
		t.Fatal("Start did not return after caller cancellation")
	}
}

func TestRunCompletesInitialStdinBeforeConsumingImmediateEnd(t *testing.T) {
	handler := &testProcessHandler{}
	handler.start = func(
		_ context.Context,
		_ *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		if err := stream.Send(startEvent(80)); err != nil {
			return err
		}
		return stream.Send(&process.StartResponse{Event: endEvent(0)})
	}
	handler.sendInput = func(
		ctx context.Context,
		_ *connect.Request[process.SendInputRequest],
	) (*connect.Response[process.SendInputResponse], error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
			return connect.NewResponse(&process.SendInputResponse{}), nil
		}
	}
	handler.closeStdin = func(
		_ context.Context,
		_ *connect.Request[process.CloseStdinRequest],
	) (*connect.Response[process.CloseStdinResponse], error) {
		return connect.NewResponse(&process.CloseStdinResponse{}), nil
	}
	handler.sendSignal = unexpectedSendSignal(t)

	client := newTestClient(t, handler, nil)
	result, err := client.Run(context.Background(), Request{
		Cmd:   "cat",
		Stdin: "input",
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(80), result.PID)
	assert.Equal(t, 0, result.ExitCode)
}

func TestRunCallerDeadlineDoesNotShortenRemoteProcessDeadline(t *testing.T) {
	startRequests := make(chan *connect.Request[process.StartRequest], 1)
	handler := blockingProcessHandler(t, nil)
	originalStart := handler.start
	handler.start = func(
		ctx context.Context,
		req *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		startRequests <- req
		return originalStart(ctx, req, stream)
	}
	handler.sendSignal = unexpectedSendSignal(t)
	client := newTestClient(t, handler, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := client.Run(ctx, Request{
		Cmd:     "sleep",
		Args:    []string{"60"},
		Timeout: 2 * time.Second,
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, result.TimedOut)
	assertConnectTimeout(
		t, (<-startRequests).Header(), time.Second, 2*time.Second,
	)
}

func TestRunCancellationBeforeStartEventDoesNotSetTag(t *testing.T) {
	registered := make(chan struct{}, 1)
	handler := &testProcessHandler{}
	handler.start = func(
		ctx context.Context,
		req *connect.Request[process.StartRequest],
		_ *connect.ServerStream[process.StartResponse],
	) error {
		assert.Empty(t, req.Msg.GetTag())
		registered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	handler.sendSignal = unexpectedSendSignal(t)

	client := newTestClient(t, handler, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-registered
		cancel()
	}()
	_, err := client.Run(ctx, Request{Cmd: "sleep", Args: []string{"60"}})
	require.ErrorIs(t, err, context.Canceled)
}

func TestRunStreamWithoutEndEventIsProtocolError(t *testing.T) {
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
	handler.sendSignal = unexpectedSendSignal(t)

	client := newTestClient(t, handler, nil)
	result, err := client.Run(context.Background(), Request{Cmd: "broken"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "without EndEvent")
	assert.Equal(t, "partial", result.Stdout)
}

func TestRunStreamErrorIsProtocolError(t *testing.T) {
	handler := &testProcessHandler{}
	handler.start = func(
		_ context.Context,
		_ *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		if err := stream.Send(startEvent(100)); err != nil {
			return err
		}
		return connect.NewError(connect.CodeUnavailable, errors.New("stream down"))
	}
	handler.sendSignal = unexpectedSendSignal(t)

	client := newTestClient(t, handler, nil)
	_, err := client.Run(context.Background(), Request{Cmd: "broken"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "receive stream")
	assert.Contains(t, err.Error(), "stream down")
}

func TestRunFailedEndEventIsExecutionError(t *testing.T) {
	errMessage := "failed to wait for process"
	handler := &testProcessHandler{}
	handler.start = func(
		_ context.Context,
		_ *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		for _, response := range []*process.StartResponse{
			startEvent(101),
			{Event: stdoutEvent([]byte("partial output"))},
			{Event: &process.ProcessEvent{Event: &process.ProcessEvent_End{
				End: &process.ProcessEvent_EndEvent{
					Exited: false,
					Status: "wait failed",
					Error:  &errMessage,
				},
			}}},
		} {
			if err := stream.Send(response); err != nil {
				return err
			}
		}
		return nil
	}
	handler.sendSignal = unexpectedSendSignal(t)

	client := newTestClient(t, handler, nil)
	result, err := client.Run(context.Background(), Request{Cmd: "broken"})
	require.Error(t, err)
	assert.Equal(t, "partial output", result.Stdout)
	assert.Contains(t, err.Error(), `status="wait failed"`)
	assert.Contains(t, err.Error(), `error="failed to wait for process"`)
}

func TestRunRejectsMalformedEvents(t *testing.T) {
	tests := []struct {
		name      string
		responses []*process.StartResponse
		wantError string
	}{
		{
			name:      "EmptyEvent",
			responses: []*process.StartResponse{{}},
			wantError: "received empty event",
		},
		{
			name:      "InvalidStart",
			responses: []*process.StartResponse{startEvent(0)},
			wantError: "invalid StartEvent",
		},
		{
			name:      "DuplicateStart",
			responses: []*process.StartResponse{startEvent(1), startEvent(2)},
			wantError: "duplicate StartEvent",
		},
		{
			name: "DataWithoutOutputFromEmptyMessage",
			responses: []*process.StartResponse{
				startEvent(1),
				{Event: &process.ProcessEvent{Event: &process.ProcessEvent_Data{}}},
			},
			wantError: "DataEvent without output",
		},
		{
			name: "PTYData",
			responses: []*process.StartResponse{
				startEvent(1),
				{Event: &process.ProcessEvent{Event: &process.ProcessEvent_Data{
					Data: &process.ProcessEvent_DataEvent{
						Output: &process.ProcessEvent_DataEvent_Pty{Pty: []byte("pty")},
					},
				}}},
			},
			wantError: "PTY data for non-PTY process",
		},
		{
			name: "DataWithoutOutput",
			responses: []*process.StartResponse{
				startEvent(1),
				{Event: &process.ProcessEvent{Event: &process.ProcessEvent_Data{
					Data: &process.ProcessEvent_DataEvent{},
				}}},
			},
			wantError: "DataEvent without output",
		},
		{
			name: "EndWithoutExitedFromEmptyMessage",
			responses: []*process.StartResponse{
				startEvent(1),
				{Event: &process.ProcessEvent{Event: &process.ProcessEvent_End{}}},
			},
			wantError: "process ended without exiting",
		},
		{
			name: "EndBeforeStart",
			responses: []*process.StartResponse{
				{Event: endEvent(0)},
			},
			wantError: "EndEvent before StartEvent",
		},
		{
			name: "UnknownEvent",
			responses: []*process.StartResponse{
				{Event: &process.ProcessEvent{}},
			},
			wantError: "unknown event",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &testProcessHandler{}
			handler.start = func(
				_ context.Context,
				_ *connect.Request[process.StartRequest],
				stream *connect.ServerStream[process.StartResponse],
			) error {
				for _, response := range tt.responses {
					if err := stream.Send(response); err != nil {
						return err
					}
				}
				return nil
			}
			handler.sendSignal = unexpectedSendSignal(t)

			client := newTestClient(t, handler, nil)
			_, err := client.Run(context.Background(), Request{Cmd: "broken"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

func TestRunStdinFailureDisconnectsWithoutKillingProcess(t *testing.T) {
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
	handler.sendSignal = unexpectedSendSignal(t)

	client := newTestClient(t, handler, nil)
	result, err := client.Run(context.Background(), Request{
		Cmd:   "cat",
		Stdin: "input",
	})
	require.Error(t, err)
	assert.Equal(t, uint32(123), result.PID)
	assert.Contains(t, err.Error(), "write stdin")
}
