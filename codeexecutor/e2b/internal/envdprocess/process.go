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
	client      *Client
	pid         uint32
	disconnect  context.CancelFunc
	done        chan struct{}
	startupDone chan struct{}
	startupOnce sync.Once

	mu    sync.RWMutex
	state processState
}

type processState struct {
	stdout      captureBuffer
	stderr      captureBuffer
	exitCode    int
	timedOut    bool
	err         error
	finished    bool
	remoteEnded bool
}

func (s *processState) result(pid uint32) Result {
	return Result{
		PID:             pid,
		Stdout:          s.stdout.String(),
		Stderr:          s.stderr.String(),
		ExitCode:        s.exitCode,
		TimedOut:        s.timedOut,
		StdoutTruncated: s.stdout.truncated,
		StderrTruncated: s.stderr.truncated,
	}
}

type captureBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *captureBuffer) append(output []byte) {
	if len(output) == 0 {
		return
	}
	if b.limit == 0 {
		_, _ = b.Write(output)
		return
	}
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.truncated = true
		return
	}
	if len(output) > remaining {
		_, _ = b.Write(output[:remaining])
		b.truncated = true
		return
	}
	_, _ = b.Write(output)
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

// newProcess constructs a process handle without transferring stream
// ownership. Client.Start uses this phase to complete initial stdin setup
// before the event consumer can terminate the stream context.
func newProcess(
	client *Client,
	pid uint32,
	disconnect context.CancelFunc,
) *Process {
	return &Process{
		client:      client,
		pid:         pid,
		disconnect:  disconnect,
		done:        make(chan struct{}),
		startupDone: make(chan struct{}),
		state: processState{
			stdout: captureBuffer{limit: client.stdoutCaptureLimit},
			stderr: captureBuffer{limit: client.stderrCaptureLimit},
		},
	}
}

func (p *Process) completeStartup() {
	p.startupOnce.Do(func() { close(p.startupDone) })
}

// startConsumer transfers processStreamCtx and stream ownership to the sole
// event consumer. Start calls it before initial stdin RPCs so process output
// and stdin can make progress independently.
func (p *Process) startConsumer(
	processStreamCtx context.Context,
	hasRemoteTimeout bool,
	stream processEventStream,
) {
	go p.consume(processStreamCtx, hasRemoteTimeout, stream)
}

func (p *Process) consume(
	processStreamCtx context.Context,
	hasRemoteTimeout bool,
	stream processEventStream,
) {
	// Defers run in reverse order: disconnect the attachment and cancel its RPC
	// context before closing the response side of the stream. This keeps all
	// transport cleanup with the sole stream owner.
	defer func() {
		// A terminal response may arrive while Start is still writing initial
		// stdin. Delay stream cancellation until startup finishes so consumer
		// teardown cannot turn a successful stdin RPC into context.Canceled.
		<-p.startupDone
		p.disconnect()
		_ = stream.Close()
	}()
	for stream.Receive() {
		if p.consumeEvent(
			processStreamCtx,
			hasRemoteTimeout,
			receivedEvent{event: stream.Event()},
			true,
		) {
			return
		}
	}
	streamErr := stream.Err()
	p.consumeEvent(
		processStreamCtx,
		hasRemoteTimeout,
		receivedEvent{err: streamErr},
		streamErr != nil,
	)
}

// consumeEvent is the only processState mutation path. The single stream
// consumer guarantees that done is closed exactly once for a terminal event.
func (p *Process) consumeEvent(
	processStreamCtx context.Context,
	hasRemoteTimeout bool,
	received receivedEvent,
	ok bool,
) bool {
	p.mu.Lock()
	outcome := handleIncomingEvent(
		processStreamCtx,
		hasRemoteTimeout,
		&p.state,
		received,
		ok,
	)
	err, done := finishProcessEvent(
		processStreamCtx, &p.state, outcome,
	)
	if received.event != nil {
		_, p.state.remoteEnded = received.event.Event.(*processrpc.ProcessEvent_End)
	}
	if done {
		p.state.err = err
		p.state.finished = true
	}
	p.mu.Unlock()
	if done {
		close(p.done)
	}
	return done
}

func (p *Process) remoteExecutionFinished() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state.remoteEnded || p.state.timedOut
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

// receiveProcessStart synchronously consumes the first response because Start
// and Connect promise to return a Process with a known PID. Later responses
// are handed to the Process consumer, avoiding a separate reader goroutine and
// event channel. Canceling processStreamCtx unblocks Receive.
func receiveProcessStart(
	processStreamCtx context.Context,
	stream processEventStream,
) (uint32, error) {
	if !stream.Receive() {
		if processStreamCtx.Err() != nil {
			return 0, streamContextError(processStreamCtx)
		}
		if err := stream.Err(); err != nil {
			return 0, fmt.Errorf(
				"envd process: receive StartEvent: %w", err,
			)
		}
		return 0, errors.New(
			"envd process: stream ended before StartEvent",
		)
	}
	if processStreamCtx.Err() != nil {
		return 0, streamContextError(processStreamCtx)
	}
	event := stream.Event()
	if event == nil {
		return 0, errors.New("envd process: received empty event")
	}
	switch event := event.Event.(type) {
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
}

func streamContextError(processStreamCtx context.Context) error {
	cause := context.Cause(processStreamCtx)
	if errors.Is(cause, errProcessTimeout) {
		return errors.Join(errProcessTimeout, context.DeadlineExceeded)
	}
	if errors.Is(cause, errProcessDisconnected) {
		return context.Canceled
	}
	if cause != nil {
		return cause
	}
	return processStreamCtx.Err()
}

// processEventStream normalizes the Start and Connect response envelopes. It
// is a synchronous adapter; the Process consumer owns all stream concurrency.
type processEventStream interface {
	Receive() bool
	Event() *processrpc.ProcessEvent
	Err() error
	Close() error
}

// startEventStream unwraps ProcessEvent from the Start response envelope.
type startEventStream struct {
	stream *connect.ServerStreamForClient[processrpc.StartResponse]
}

func (s *startEventStream) Receive() bool { return s.stream.Receive() }

func (s *startEventStream) Event() *processrpc.ProcessEvent {
	if msg := s.stream.Msg(); msg != nil {
		return msg.Event
	}
	return nil
}

func (s *startEventStream) Err() error { return s.stream.Err() }

func (s *startEventStream) Close() error { return s.stream.Close() }

// connectEventStream unwraps ProcessEvent from the Connect response envelope.
type connectEventStream struct {
	stream *connect.ServerStreamForClient[processrpc.ConnectResponse]
}

func (s *connectEventStream) Receive() bool { return s.stream.Receive() }

func (s *connectEventStream) Event() *processrpc.ProcessEvent {
	if msg := s.stream.Msg(); msg != nil {
		return msg.Event
	}
	return nil
}

func (s *connectEventStream) Err() error { return s.stream.Err() }

func (s *connectEventStream) Close() error { return s.stream.Close() }
