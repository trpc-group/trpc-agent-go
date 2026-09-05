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
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	processrpc "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
)

func TestStartReturnsControllableProcess(t *testing.T) {
	inputRequests := make(chan *connect.Request[processrpc.SendInputRequest], 1)
	closeRequests := make(chan *connect.Request[processrpc.CloseStdinRequest], 1)
	killed := make(chan struct{})
	handler := &testProcessHandler{}
	handler.start = func(
		_ context.Context,
		req *connect.Request[processrpc.StartRequest],
		stream *connect.ServerStream[processrpc.StartResponse],
	) error {
		assert.Equal(t, "handle-process", req.Msg.GetTag())
		assert.True(t, req.Msg.GetStdin())
		if err := stream.Send(startEvent(201)); err != nil {
			return err
		}
		<-killed
		return stream.Send(&processrpc.StartResponse{Event: endEvent(137)})
	}
	handler.sendInput = func(
		_ context.Context,
		req *connect.Request[processrpc.SendInputRequest],
	) (*connect.Response[processrpc.SendInputResponse], error) {
		inputRequests <- req
		return connect.NewResponse(&processrpc.SendInputResponse{}), nil
	}
	handler.closeStdin = func(
		_ context.Context,
		req *connect.Request[processrpc.CloseStdinRequest],
	) (*connect.Response[processrpc.CloseStdinResponse], error) {
		closeRequests <- req
		return connect.NewResponse(&processrpc.CloseStdinResponse{}), nil
	}
	handler.sendSignal = func(
		_ context.Context,
		req *connect.Request[processrpc.SendSignalRequest],
	) (*connect.Response[processrpc.SendSignalResponse], error) {
		assert.Equal(t, uint32(201), req.Msg.Process.GetPid())
		assert.Equal(t, processrpc.Signal_SIGNAL_SIGKILL, req.Msg.Signal)
		close(killed)
		return connect.NewResponse(&processrpc.SendSignalResponse{}), nil
	}

	client := newTestClient(t, handler, nil)
	proc, err := client.Start(context.Background(), Request{
		Cmd:           "cat",
		KeepStdinOpen: true,
	}, WithTag("handle-process"))
	require.NoError(t, err)
	assert.Equal(t, uint32(201), proc.PID())

	require.NoError(t, proc.SendInput(context.Background(), []byte("hello")))
	require.NoError(t, proc.CloseStdin(context.Background()))
	assert.Equal(t, []byte("hello"), (<-inputRequests).Msg.Input.GetStdin())
	assert.Equal(t, uint32(201), (<-closeRequests).Msg.Process.GetPid())

	killedProcess, err := proc.Kill(context.Background())
	require.NoError(t, err)
	assert.True(t, killedProcess)
	result, err := proc.Wait(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint32(201), result.PID)
	assert.Equal(t, 137, result.ExitCode)
}

func TestProcessDisconnectAndReconnect(t *testing.T) {
	disconnected := make(chan struct{})
	handler := &testProcessHandler{}
	handler.start = func(
		ctx context.Context,
		_ *connect.Request[processrpc.StartRequest],
		stream *connect.ServerStream[processrpc.StartResponse],
	) error {
		if err := stream.Send(startEvent(202)); err != nil {
			return err
		}
		<-ctx.Done()
		close(disconnected)
		return ctx.Err()
	}
	handler.connect = func(
		_ context.Context,
		req *connect.Request[processrpc.ConnectRequest],
		stream *connect.ServerStream[processrpc.ConnectResponse],
	) error {
		assert.Equal(t, uint32(202), req.Msg.Process.GetPid())
		for _, event := range []*processrpc.ProcessEvent{
			startProcessEvent(202),
			stdoutEvent([]byte("reconnected\n")),
			endEvent(0),
		} {
			if err := stream.Send(&processrpc.ConnectResponse{Event: event}); err != nil {
				return err
			}
		}
		return nil
	}

	client := newTestClient(t, handler, nil)
	proc, err := client.Start(context.Background(), Request{Cmd: "sleep"})
	require.NoError(t, err)
	proc.Disconnect()
	result, err := proc.Wait(context.Background())
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, uint32(202), result.PID)
	<-disconnected

	reconnected, err := client.Connect(context.Background(), 202)
	require.NoError(t, err)
	result, err = reconnected.Wait(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint32(202), result.PID)
	assert.Equal(t, "reconnected\n", result.Stdout)
}

func TestProcessCallerCancellationDisconnectsStream(t *testing.T) {
	disconnected := make(chan struct{})
	handler := &testProcessHandler{}
	handler.start = func(
		ctx context.Context,
		_ *connect.Request[processrpc.StartRequest],
		stream *connect.ServerStream[processrpc.StartResponse],
	) error {
		if err := stream.Send(startEvent(207)); err != nil {
			return err
		}
		<-ctx.Done()
		close(disconnected)
		return ctx.Err()
	}

	client := newTestClient(t, handler, nil)
	ctx, cancel := context.WithCancel(context.Background())
	proc, err := client.Start(ctx, Request{Cmd: "sleep"})
	require.NoError(t, err)

	cancel()
	result, err := proc.Wait(context.Background())
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, uint32(207), result.PID)
	<-disconnected
}

func TestProcessWaitCancellationDoesNotDisconnect(t *testing.T) {
	release := make(chan struct{})
	handler := &testProcessHandler{}
	handler.start = func(
		_ context.Context,
		_ *connect.Request[processrpc.StartRequest],
		stream *connect.ServerStream[processrpc.StartResponse],
	) error {
		if err := stream.Send(startEvent(206)); err != nil {
			return err
		}
		<-release
		return stream.Send(&processrpc.StartResponse{Event: endEvent(0)})
	}

	client := newTestClient(t, handler, nil)
	proc, err := client.Start(context.Background(), Request{Cmd: "sleep"})
	require.NoError(t, err)

	waitCtx, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	result, err := proc.Wait(waitCtx)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, uint32(206), result.PID)

	close(release)
	result, err = proc.Wait(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint32(206), result.PID)
	assert.Equal(t, 0, result.ExitCode)
}

func TestStartStdinFailureReturnsProcessForCleanup(t *testing.T) {
	killed := make(chan struct{})
	handler := &testProcessHandler{}
	handler.start = func(
		ctx context.Context,
		_ *connect.Request[processrpc.StartRequest],
		stream *connect.ServerStream[processrpc.StartResponse],
	) error {
		if err := stream.Send(startEvent(205)); err != nil {
			return err
		}
		select {
		case <-killed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	handler.sendInput = func(
		context.Context,
		*connect.Request[processrpc.SendInputRequest],
	) (*connect.Response[processrpc.SendInputResponse], error) {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("down"))
	}
	handler.sendSignal = func(
		context.Context,
		*connect.Request[processrpc.SendSignalRequest],
	) (*connect.Response[processrpc.SendSignalResponse], error) {
		close(killed)
		return connect.NewResponse(&processrpc.SendSignalResponse{}), nil
	}

	client := newTestClient(t, handler, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	proc, err := client.Start(ctx, Request{Cmd: "cat", Stdin: "input"})
	require.Error(t, err)
	require.NotNil(t, proc)
	assert.Equal(t, uint32(205), proc.PID())
	killedProcess, killErr := proc.Kill(context.Background())
	require.NoError(t, killErr)
	assert.True(t, killedProcess)
}

func TestUninitializedProcessMethods(t *testing.T) {
	var proc *Process
	assert.Zero(t, proc.PID())

	_, err := proc.Wait(context.Background())
	require.ErrorContains(t, err, "process is not initialized")

	_, err = (&Process{}).Wait(nil)
	require.ErrorContains(t, err, "nil context")

	_, err = proc.Kill(context.Background())
	require.ErrorContains(t, err, "process is not initialized")

	err = proc.SendInput(context.Background(), []byte("input"))
	require.ErrorContains(t, err, "process is not initialized")

	err = proc.CloseStdin(context.Background())
	require.ErrorContains(t, err, "process is not initialized")
}

func TestCaptureBufferBoundaries(t *testing.T) {
	tests := []struct {
		name          string
		limit         int
		chunks        []string
		want          string
		wantTruncated bool
	}{
		{
			name:   "UnlimitedAcrossEvents",
			chunks: []string{"ab", "cde"},
			want:   "abcde",
		},
		{
			name:   "ExactlyAtLimitAcrossEvents",
			limit:  4,
			chunks: []string{"ab", "cd"},
			want:   "abcd",
		},
		{
			name:          "OneByteOverLimit",
			limit:         4,
			chunks:        []string{"abcde"},
			want:          "abcd",
			wantTruncated: true,
		},
		{
			name:          "CrossesLimitAcrossEvents",
			limit:         4,
			chunks:        []string{"ab", "cde"},
			want:          "abcd",
			wantTruncated: true,
		},
		{
			name:          "DataAfterExactLimit",
			limit:         4,
			chunks:        []string{"abcd", "e"},
			want:          "abcd",
			wantTruncated: true,
		},
		{
			name:   "EmptyDataAfterExactLimit",
			limit:  4,
			chunks: []string{"abcd", ""},
			want:   "abcd",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buffer := captureBuffer{limit: tt.limit}
			for _, chunk := range tt.chunks {
				buffer.append([]byte(chunk))
			}
			assert.Equal(t, tt.want, buffer.String())
			assert.Equal(t, tt.wantTruncated, buffer.truncated)
		})
	}
}

func TestConnectRejectsInvalidInitialStream(t *testing.T) {
	tests := []struct {
		name    string
		connect func(
			context.Context,
			*connect.Request[processrpc.ConnectRequest],
			*connect.ServerStream[processrpc.ConnectResponse],
		) error
		wantError string
	}{
		{
			name: "EmptyStream",
			connect: func(
				context.Context,
				*connect.Request[processrpc.ConnectRequest],
				*connect.ServerStream[processrpc.ConnectResponse],
			) error {
				return nil
			},
			wantError: "stream ended before StartEvent",
		},
		{
			name: "StreamError",
			connect: func(
				context.Context,
				*connect.Request[processrpc.ConnectRequest],
				*connect.ServerStream[processrpc.ConnectResponse],
			) error {
				return connect.NewError(
					connect.CodeUnavailable, errors.New("stream unavailable"),
				)
			},
			wantError: "stream unavailable",
		},
		{
			name: "WrongPID",
			connect: func(
				_ context.Context,
				_ *connect.Request[processrpc.ConnectRequest],
				stream *connect.ServerStream[processrpc.ConnectResponse],
			) error {
				return stream.Send(&processrpc.ConnectResponse{
					Event: startProcessEvent(999),
				})
			},
			wantError: "returned PID 999, want 123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &testProcessHandler{connect: tt.connect}
			client := newTestClient(t, handler, nil)

			_, err := client.Connect(context.Background(), 123)
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}
