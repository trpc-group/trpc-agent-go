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
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec/processconnect"
)

const protocolTestHeader = "X-Envd-Process-Test"

func TestProcessProtocolClient(t *testing.T) {
	t.Run("List", testProcessProtocolList)
	t.Run("Connect", testProcessProtocolConnect)
	t.Run("Start", testProcessProtocolStart)
	t.Run("Update", testProcessProtocolUpdate)
	t.Run("StreamInput", testProcessProtocolStreamInput)
	t.Run("SendInput", testProcessProtocolSendInput)
	t.Run("SendSignal", testProcessProtocolSendSignal)
	t.Run("CloseStdin", testProcessProtocolCloseStdin)
}

func testProcessProtocolList(t *testing.T) {
	handler := &protocolProcessHandler{}
	handler.list = func(
		_ context.Context,
		req *connect.Request[process.ListRequest],
	) (*connect.Response[process.ListResponse], error) {
		assertProtocolHeader(t, req.Header())
		return connect.NewResponse(&process.ListResponse{Processes: []*process.ProcessInfo{
			{
				Pid: 7,
				Tag: stringPointer("list-process"),
				Config: &process.ProcessConfig{
					Cmd:  "/bin/sh",
					Args: []string{"-c", "sleep 60"},
				},
			},
		}}), nil
	}

	client := newProtocolTestClient(t, handler)
	req := connect.NewRequest(&process.ListRequest{})
	setProtocolHeader(req.Header())
	resp, err := client.List(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, resp.Msg.Processes, 1)
	assert.Equal(t, uint32(7), resp.Msg.Processes[0].Pid)
	assert.Equal(t, "list-process", resp.Msg.Processes[0].GetTag())
	assert.Equal(t, "/bin/sh", resp.Msg.Processes[0].Config.Cmd)
}

func testProcessProtocolConnect(t *testing.T) {
	handler := &protocolProcessHandler{}
	handler.connect = func(
		_ context.Context,
		req *connect.Request[process.ConnectRequest],
		stream *connect.ServerStream[process.ConnectResponse],
	) error {
		assertProtocolHeader(t, req.Header())
		assert.Equal(t, "connect-process", req.Msg.Process.GetTag())
		for _, event := range []*process.ProcessEvent{
			startProcessEvent(8),
			stdoutEvent([]byte("connected\n")),
			endEvent(0),
		} {
			if err := stream.Send(&process.ConnectResponse{Event: event}); err != nil {
				return err
			}
		}
		return nil
	}

	client := newProtocolTestClient(t, handler)
	req := connect.NewRequest(&process.ConnectRequest{
		Process: tagSelector("connect-process"),
	})
	setProtocolHeader(req.Header())
	stream, err := client.Connect(context.Background(), req)
	require.NoError(t, err)
	defer stream.Close()

	var events []*process.ProcessEvent
	for stream.Receive() {
		events = append(events, stream.Msg().Event)
	}
	require.NoError(t, stream.Err())
	require.Len(t, events, 3)
	assert.Equal(t, uint32(8), events[0].GetStart().Pid)
	assert.Equal(t, []byte("connected\n"), events[1].GetData().GetStdout())
	assert.Equal(t, int32(0), events[2].GetEnd().ExitCode)
}

func testProcessProtocolStart(t *testing.T) {
	handler := &protocolProcessHandler{}
	handler.start = func(
		_ context.Context,
		req *connect.Request[process.StartRequest],
		stream *connect.ServerStream[process.StartResponse],
	) error {
		assertProtocolHeader(t, req.Header())
		assert.Equal(t, "/bin/sh", req.Msg.Process.Cmd)
		assert.Equal(t, []string{"-c", "printf started"}, req.Msg.Process.Args)
		assert.Equal(t, "start-process", req.Msg.GetTag())
		assert.True(t, req.Msg.GetStdin())
		for _, event := range []*process.ProcessEvent{
			startProcessEvent(9),
			stdoutEvent([]byte("started")),
			endEvent(0),
		} {
			if err := stream.Send(&process.StartResponse{Event: event}); err != nil {
				return err
			}
		}
		return nil
	}

	stdin := true
	tag := "start-process"
	client := newProtocolTestClient(t, handler)
	req := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "/bin/sh",
			Args: []string{"-c", "printf started"},
		},
		Tag:   &tag,
		Stdin: &stdin,
	})
	setProtocolHeader(req.Header())
	stream, err := client.Start(context.Background(), req)
	require.NoError(t, err)
	defer stream.Close()

	var events []*process.ProcessEvent
	for stream.Receive() {
		events = append(events, stream.Msg().Event)
	}
	require.NoError(t, stream.Err())
	require.Len(t, events, 3)
	assert.Equal(t, uint32(9), events[0].GetStart().Pid)
	assert.Equal(t, []byte("started"), events[1].GetData().GetStdout())
	assert.Equal(t, int32(0), events[2].GetEnd().ExitCode)
}

func testProcessProtocolUpdate(t *testing.T) {
	handler := &protocolProcessHandler{}
	handler.update = func(
		_ context.Context,
		req *connect.Request[process.UpdateRequest],
	) (*connect.Response[process.UpdateResponse], error) {
		assertProtocolHeader(t, req.Header())
		assert.Equal(t, uint32(10), req.Msg.Process.GetPid())
		if assert.NotNil(t, req.Msg.Pty) &&
			assert.NotNil(t, req.Msg.Pty.Size) {
			assert.Equal(t, uint32(100), req.Msg.Pty.Size.Cols)
			assert.Equal(t, uint32(40), req.Msg.Pty.Size.Rows)
		}
		return connect.NewResponse(&process.UpdateResponse{}), nil
	}

	client := newProtocolTestClient(t, handler)
	req := connect.NewRequest(&process.UpdateRequest{
		Process: pidSelector(10),
		Pty: &process.PTY{Size: &process.PTY_Size{
			Cols: 100,
			Rows: 40,
		}},
	})
	setProtocolHeader(req.Header())
	_, err := client.Update(context.Background(), req)
	require.NoError(t, err)
}

func testProcessProtocolStreamInput(t *testing.T) {
	type receivedStream struct {
		header   http.Header
		messages []*process.StreamInputRequest
	}
	received := make(chan receivedStream, 1)
	handler := &protocolProcessHandler{}
	handler.streamInput = func(
		_ context.Context,
		stream *connect.ClientStream[process.StreamInputRequest],
	) (*connect.Response[process.StreamInputResponse], error) {
		got := receivedStream{header: stream.RequestHeader().Clone()}
		for stream.Receive() {
			got.messages = append(got.messages, stream.Msg())
		}
		if err := stream.Err(); err != nil {
			return nil, err
		}
		received <- got
		return connect.NewResponse(&process.StreamInputResponse{}), nil
	}

	client := newProtocolTestClient(t, handler)
	stream := client.StreamInput(context.Background())
	setProtocolHeader(stream.RequestHeader())
	require.NoError(t, stream.Send(&process.StreamInputRequest{
		Event: &process.StreamInputRequest_Start{
			Start: &process.StreamInputRequest_StartEvent{
				Process: tagSelector("stream-input-process"),
			},
		},
	}))
	require.NoError(t, stream.Send(&process.StreamInputRequest{
		Event: &process.StreamInputRequest_Keepalive{
			Keepalive: &process.StreamInputRequest_KeepAlive{},
		},
	}))
	require.NoError(t, stream.Send(&process.StreamInputRequest{
		Event: &process.StreamInputRequest_Data{
			Data: &process.StreamInputRequest_DataEvent{
				Input: &process.ProcessInput{
					Input: &process.ProcessInput_Pty{Pty: []byte("echo input\n")},
				},
			},
		},
	}))
	_, err := stream.CloseAndReceive()
	require.NoError(t, err)

	got := <-received
	assertProtocolHeader(t, got.header)
	require.Len(t, got.messages, 3)
	assert.Equal(t, "stream-input-process", got.messages[0].GetStart().Process.GetTag())
	assert.NotNil(t, got.messages[1].GetKeepalive())
	assert.Equal(t, []byte("echo input\n"), got.messages[2].GetData().Input.GetPty())
}

func testProcessProtocolSendInput(t *testing.T) {
	handler := &protocolProcessHandler{}
	handler.sendInput = func(
		_ context.Context,
		req *connect.Request[process.SendInputRequest],
	) (*connect.Response[process.SendInputResponse], error) {
		assertProtocolHeader(t, req.Header())
		assert.Equal(t, uint32(11), req.Msg.Process.GetPid())
		assert.Equal(t, []byte("stdin data"), req.Msg.Input.GetStdin())
		return connect.NewResponse(&process.SendInputResponse{}), nil
	}

	client := newProtocolTestClient(t, handler)
	req := connect.NewRequest(&process.SendInputRequest{
		Process: pidSelector(11),
		Input: &process.ProcessInput{
			Input: &process.ProcessInput_Stdin{Stdin: []byte("stdin data")},
		},
	})
	setProtocolHeader(req.Header())
	_, err := client.SendInput(context.Background(), req)
	require.NoError(t, err)
}

func testProcessProtocolSendSignal(t *testing.T) {
	handler := &protocolProcessHandler{}
	handler.sendSignal = func(
		_ context.Context,
		req *connect.Request[process.SendSignalRequest],
	) (*connect.Response[process.SendSignalResponse], error) {
		assertProtocolHeader(t, req.Header())
		assert.Equal(t, "signal-process", req.Msg.Process.GetTag())
		assert.Equal(t, process.Signal_SIGNAL_SIGKILL, req.Msg.Signal)
		return connect.NewResponse(&process.SendSignalResponse{}), nil
	}

	client := newProtocolTestClient(t, handler)
	req := connect.NewRequest(&process.SendSignalRequest{
		Process: tagSelector("signal-process"),
		Signal:  process.Signal_SIGNAL_SIGKILL,
	})
	setProtocolHeader(req.Header())
	_, err := client.SendSignal(context.Background(), req)
	require.NoError(t, err)
}

func testProcessProtocolCloseStdin(t *testing.T) {
	handler := &protocolProcessHandler{}
	handler.closeStdin = func(
		_ context.Context,
		req *connect.Request[process.CloseStdinRequest],
	) (*connect.Response[process.CloseStdinResponse], error) {
		assertProtocolHeader(t, req.Header())
		assert.Equal(t, uint32(12), req.Msg.Process.GetPid())
		return connect.NewResponse(&process.CloseStdinResponse{}), nil
	}

	client := newProtocolTestClient(t, handler)
	req := connect.NewRequest(&process.CloseStdinRequest{
		Process: pidSelector(12),
	})
	setProtocolHeader(req.Header())
	_, err := client.CloseStdin(context.Background(), req)
	require.NoError(t, err)
}

type protocolProcessHandler struct {
	processconnect.UnimplementedProcessHandler
	list func(
		context.Context,
		*connect.Request[process.ListRequest],
	) (*connect.Response[process.ListResponse], error)
	connect func(
		context.Context,
		*connect.Request[process.ConnectRequest],
		*connect.ServerStream[process.ConnectResponse],
	) error
	start func(
		context.Context,
		*connect.Request[process.StartRequest],
		*connect.ServerStream[process.StartResponse],
	) error
	update func(
		context.Context,
		*connect.Request[process.UpdateRequest],
	) (*connect.Response[process.UpdateResponse], error)
	streamInput func(
		context.Context,
		*connect.ClientStream[process.StreamInputRequest],
	) (*connect.Response[process.StreamInputResponse], error)
	sendInput func(
		context.Context,
		*connect.Request[process.SendInputRequest],
	) (*connect.Response[process.SendInputResponse], error)
	sendSignal func(
		context.Context,
		*connect.Request[process.SendSignalRequest],
	) (*connect.Response[process.SendSignalResponse], error)
	closeStdin func(
		context.Context,
		*connect.Request[process.CloseStdinRequest],
	) (*connect.Response[process.CloseStdinResponse], error)
}

func (h *protocolProcessHandler) List(
	ctx context.Context,
	req *connect.Request[process.ListRequest],
) (*connect.Response[process.ListResponse], error) {
	return h.list(ctx, req)
}

func (h *protocolProcessHandler) Connect(
	ctx context.Context,
	req *connect.Request[process.ConnectRequest],
	stream *connect.ServerStream[process.ConnectResponse],
) error {
	return h.connect(ctx, req, stream)
}

func (h *protocolProcessHandler) Start(
	ctx context.Context,
	req *connect.Request[process.StartRequest],
	stream *connect.ServerStream[process.StartResponse],
) error {
	return h.start(ctx, req, stream)
}

func (h *protocolProcessHandler) Update(
	ctx context.Context,
	req *connect.Request[process.UpdateRequest],
) (*connect.Response[process.UpdateResponse], error) {
	return h.update(ctx, req)
}

func (h *protocolProcessHandler) StreamInput(
	ctx context.Context,
	stream *connect.ClientStream[process.StreamInputRequest],
) (*connect.Response[process.StreamInputResponse], error) {
	return h.streamInput(ctx, stream)
}

func (h *protocolProcessHandler) SendInput(
	ctx context.Context,
	req *connect.Request[process.SendInputRequest],
) (*connect.Response[process.SendInputResponse], error) {
	return h.sendInput(ctx, req)
}

func (h *protocolProcessHandler) SendSignal(
	ctx context.Context,
	req *connect.Request[process.SendSignalRequest],
) (*connect.Response[process.SendSignalResponse], error) {
	return h.sendSignal(ctx, req)
}

func (h *protocolProcessHandler) CloseStdin(
	ctx context.Context,
	req *connect.Request[process.CloseStdinRequest],
) (*connect.Response[process.CloseStdinResponse], error) {
	return h.closeStdin(ctx, req)
}

func newProtocolTestClient(
	t *testing.T,
	handler processconnect.ProcessHandler,
) processconnect.ProcessClient {
	t.Helper()
	_, rpcHandler := processconnect.NewProcessHandler(handler)
	server := httptest.NewServer(rpcHandler)
	t.Cleanup(server.Close)
	return processconnect.NewProcessClient(server.Client(), server.URL)
}

func setProtocolHeader(header http.Header) {
	header.Set(protocolTestHeader, "present")
}

func assertProtocolHeader(t *testing.T, header http.Header) {
	t.Helper()
	assert.Equal(t, "present", header.Get(protocolTestHeader))
}

func startProcessEvent(pid uint32) *process.ProcessEvent {
	return &process.ProcessEvent{Event: &process.ProcessEvent_Start{
		Start: &process.ProcessEvent_StartEvent{Pid: pid},
	}}
}

func stringPointer(value string) *string {
	return &value
}
