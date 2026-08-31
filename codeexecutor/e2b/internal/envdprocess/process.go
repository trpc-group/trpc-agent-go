//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package envdprocess

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"connectrpc.com/connect"

	processrpc "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
)

// Process is a handle to a started or connected envd process. A Process is
// safe for concurrent use. Canceling the context passed to Start or Connect
// disconnects its event stream without killing the remote process.
type Process struct {
	client     *Client
	pid        uint32
	disconnect context.CancelFunc
	done       chan struct{}

	mu    sync.RWMutex
	state processState
}

type processState struct {
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	exitCode int
	timedOut bool
	err      error
	finished bool
}

func (s *processState) result(pid uint32) Result {
	return Result{
		PID:      pid,
		Stdout:   s.stdout.String(),
		Stderr:   s.stderr.String(),
		ExitCode: s.exitCode,
		TimedOut: s.timedOut,
	}
}

// PID returns the envd process ID.
func (p *Process) PID() uint32 {
	if p == nil {
		return 0
	}
	return p.pid
}

// Wait waits for a terminal EndEvent and may be called concurrently. Canceling
// ctx stops only this Wait call. The context originally passed to Start or
// Connect separately owns the event stream; use Disconnect to close that
// stream or Kill to terminate the process.
func (p *Process) Wait(ctx context.Context) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("envd process: nil context")
	}
	if p == nil {
		return Result{}, errors.New("envd process: process is not initialized")
	}
	select {
	case <-p.done:
		return p.snapshot()
	case <-ctx.Done():
		select {
		case <-p.done:
			return p.snapshot()
		default:
		}
		result, _ := p.snapshot()
		return result, ctx.Err()
	}
}

// Disconnect idempotently closes this handle's event stream without killing
// the process.
func (p *Process) Disconnect() {
	if p != nil && p.disconnect != nil {
		p.disconnect()
	}
}

// Kill sends SIGKILL to this process. It returns false without an error when
// envd reports that the process does not exist. Kill does not disconnect the
// event stream.
func (p *Process) Kill(ctx context.Context) (bool, error) {
	if p == nil || p.client == nil {
		return false, errors.New("envd process: process is not initialized")
	}
	return p.client.Kill(ctx, p.pid)
}

// SendInput writes bytes to this process stdin.
func (p *Process) SendInput(ctx context.Context, input []byte) error {
	if p == nil || p.client == nil {
		return errors.New("envd process: process is not initialized")
	}
	return p.client.SendInput(ctx, p.pid, input)
}

// CloseStdin closes this process stdin and signals EOF.
func (p *Process) CloseStdin(ctx context.Context) error {
	if p == nil || p.client == nil {
		return errors.New("envd process: process is not initialized")
	}
	return p.client.CloseStdin(ctx, p.pid)
}

func newProcess(
	client *Client,
	pid uint32,
	callerCtx context.Context,
	streamCtx context.Context,
	hasRemoteTimeout bool,
	disconnect context.CancelFunc,
	events <-chan receivedEvent,
) *Process {
	proc := &Process{
		client:     client,
		pid:        pid,
		disconnect: disconnect,
		done:       make(chan struct{}),
	}
	go proc.consume(callerCtx, streamCtx, hasRemoteTimeout, events)
	return proc
}

func (p *Process) consume(
	callerCtx context.Context,
	streamCtx context.Context,
	hasRemoteTimeout bool,
	events <-chan receivedEvent,
) {
	defer p.disconnect()
	for {
		select {
		case <-callerCtx.Done():
			p.finish(callerCtx.Err())
			return
		case received, ok := <-events:
			if err := callerCtx.Err(); err != nil {
				p.finish(err)
				return
			}
			p.mu.Lock()
			outcome := handleIncomingEvent(
				streamCtx,
				hasRemoteTimeout,
				&p.state,
				received,
				ok,
			)
			err, done := finishProcessEvent(
				streamCtx, &p.state, outcome,
			)
			p.mu.Unlock()
			if done {
				p.finish(err)
				return
			}
		case <-streamCtx.Done():
			if err := callerCtx.Err(); err != nil {
				p.finish(err)
				return
			}
			p.mu.Lock()
			err := finishStoppedProcess(streamCtx, &p.state)
			p.mu.Unlock()
			p.finish(err)
			return
		}
	}
}

// finish is called only by the stream-consuming goroutine.
func (p *Process) finish(err error) {
	p.mu.Lock()
	p.state.err = err
	p.state.finished = true
	p.mu.Unlock()
	close(p.done)
}

func (p *Process) snapshot() (Result, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := p.state.result(p.pid)
	if p.state.finished {
		return result, p.state.err
	}
	return result, nil
}

func waitForStartCall(
	ctx context.Context,
	streamCtx context.Context,
	startCalls <-chan startCall,
) (startCall, error) {
	select {
	case started := <-startCalls:
		return started, nil
	case <-ctx.Done():
		return startCall{}, ctx.Err()
	case <-streamCtx.Done():
		return startCall{}, streamContextError(ctx, streamCtx)
	}
}

func waitForProcessStart(
	ctx context.Context,
	streamCtx context.Context,
	events <-chan receivedEvent,
) (uint32, error) {
	select {
	case received, ok := <-events:
		if !ok {
			return 0, errors.New(
				"envd process: stream ended before StartEvent",
			)
		}
		if received.err != nil {
			return 0, fmt.Errorf(
				"envd process: receive StartEvent: %w", received.err,
			)
		}
		if received.event == nil {
			return 0, errors.New("envd process: received empty event")
		}
		switch event := received.event.Event.(type) {
		case *processrpc.ProcessEvent_Start:
			if event.Start == nil || event.Start.Pid == 0 {
				return 0, errors.New(
					"envd process: received invalid StartEvent",
				)
			}
			return event.Start.Pid, nil
		case *processrpc.ProcessEvent_End:
			return 0, errors.New(
				"envd process: received EndEvent before StartEvent",
			)
		default:
			return 0, errors.New(
				"envd process: received unknown event before StartEvent",
			)
		}
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-streamCtx.Done():
		return 0, streamContextError(ctx, streamCtx)
	}
}

func streamContextError(
	callerCtx context.Context,
	streamCtx context.Context,
) error {
	if err := callerCtx.Err(); err != nil {
		return err
	}
	cause := context.Cause(streamCtx)
	if errors.Is(cause, errProcessTimeout) {
		return context.DeadlineExceeded
	}
	if errors.Is(cause, errProcessDisconnected) {
		return context.Canceled
	}
	if cause != nil {
		return cause
	}
	return streamCtx.Err()
}

func receiveConnected(
	ctx context.Context,
	stream *connect.ServerStreamForClient[processrpc.ConnectResponse],
) <-chan receivedEvent {
	events := make(chan receivedEvent)
	go func() {
		defer close(events)
		defer stream.Close()
		for stream.Receive() {
			msg := stream.Msg()
			var event *processrpc.ProcessEvent
			if msg != nil {
				event = msg.Event
			}
			select {
			case events <- receivedEvent{event: event}:
			case <-ctx.Done():
				return
			}
		}
		if err := stream.Err(); err != nil {
			select {
			case events <- receivedEvent{err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return events
}
