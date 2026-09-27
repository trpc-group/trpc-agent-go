//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
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

func signalEndEvent(status string) *process.ProcessEvent {
	return &process.ProcessEvent{Event: &process.ProcessEvent_End{
		End: &process.ProcessEvent_EndEvent{ExitCode: -1, Status: status, Error: &status},
	}}
}

func TestRunSignalTermination(t *testing.T) {
	for _, tc := range []struct {
		status string
		exit   int
	}{
		{"signal: hangup", 129},
		{"signal: terminated", 143},
		{"signal: killed", 137},
		{"signal: segmentation fault (core dumped)", 139},
		{"signal: aborted", 134},
		{"signal: user defined signal 1", 138},
		{"signal: user defined signal 2", 140},
		{"signal: bad system call", 159},
		{"signal: signal 32", 160},
		{"signal: signal 64", 192},
	} {
		t.Run(tc.status, func(t *testing.T) {
			handler := &testProcessHandler{}
			handler.start = func(_ context.Context, _ *connect.Request[process.StartRequest], stream *connect.ServerStream[process.StartResponse]) error {
				for _, event := range []*process.ProcessEvent{
					startProcessEvent(42), stdoutEvent([]byte("before signal\n")),
					stderrEvent([]byte("diagnostic\n")), signalEndEvent(tc.status),
				} {
					if err := stream.Send(&process.StartResponse{Event: event}); err != nil {
						return err
					}
				}
				return nil
			}
			handler.sendSignal = unexpectedSendSignal(t)
			result, err := newTestClient(t, handler, nil).Run(context.Background(), Request{Cmd: "program"})
			require.NoError(t, err)
			assert.Equal(t, tc.exit, result.ExitCode)
			assert.Equal(t, "before signal\n", result.Stdout)
			assert.Equal(t, "diagnostic\n", result.Stderr)
			assert.False(t, result.TimedOut)
		})
	}
}

func TestRunRejectsUnknownTerminalStatus(t *testing.T) {
	for _, tc := range []struct {
		status string
		exit   int32
	}{
		{"wait failed", -1},
		{"", -1},
		{"stop signal: stopped", -1},
		{"signal: unknown", -1},
		{"signal: signal 0", -1},
		{"signal: signal -1", -1},
		{"signal: signal 65", -1},
		{"signal: signal invalid", -1},
		{"signal: signal 2147483648", -1},
		{"signal: signal 31", -1},
		{"signal: signal +32", -1},
		{"signal: signal 032", -1},
		{"signal: killed", 0},
	} {
		t.Run(tc.status, func(t *testing.T) {
			handler := &testProcessHandler{}
			handler.start = func(_ context.Context, _ *connect.Request[process.StartRequest], stream *connect.ServerStream[process.StartResponse]) error {
				end := signalEndEvent(tc.status)
				end.GetEnd().ExitCode = tc.exit
				for _, event := range []*process.ProcessEvent{
					startProcessEvent(42), stdoutEvent([]byte("partial output")), end,
				} {
					if err := stream.Send(&process.StartResponse{Event: event}); err != nil {
						return err
					}
				}
				return nil
			}
			handler.sendSignal = unexpectedSendSignal(t)
			result, err := newTestClient(t, handler, nil).Run(context.Background(), Request{Cmd: "program"})
			require.ErrorContains(t, err, "process ended without exiting")
			assert.Equal(t, "partial output", result.Stdout)
			assert.False(t, result.TimedOut)
		})
	}
}

func TestConnectSignalTermination(t *testing.T) {
	handler := &testProcessHandler{}
	handler.connect = func(_ context.Context, _ *connect.Request[process.ConnectRequest], stream *connect.ServerStream[process.ConnectResponse]) error {
		for _, event := range []*process.ProcessEvent{
			startProcessEvent(42), stdoutEvent([]byte("before signal\n")),
			signalEndEvent("signal: killed"),
		} {
			if err := stream.Send(&process.ConnectResponse{Event: event}); err != nil {
				return err
			}
		}
		return nil
	}
	handler.sendSignal = unexpectedSendSignal(t)
	proc, err := newTestClient(t, handler, nil).Connect(context.Background(), 42)
	require.NoError(t, err)
	defer proc.Disconnect()
	result, err := proc.Wait(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 137, result.ExitCode)
	assert.Equal(t, "before signal\n", result.Stdout)
	assert.False(t, result.TimedOut)
}
