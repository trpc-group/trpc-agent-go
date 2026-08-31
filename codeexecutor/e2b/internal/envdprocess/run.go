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
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
)

var (
	errProcessTimeout      = errors.New("envd process: process timeout")
	errProcessDisconnected = errors.New("envd process: process disconnected")
)

// Request describes one non-interactive process invocation.
type Request struct {
	Cmd  string
	Args []string
	Envs map[string]string
	Cwd  string
	// User selects the sandbox user through envd's Basic authentication
	// header. An empty value omits the header and lets envd choose its default.
	User string
	// Tag optionally identifies the process for List and Connect recovery.
	Tag string
	// Stdin is initial input sent after envd reports the process PID.
	Stdin string
	// KeepStdinOpen leaves stdin open after Stdin is sent. It is intended for
	// Start callers that will continue writing through Process.SendInput. Run
	// callers should use it only when the command can finish without stdin EOF.
	KeepStdinOpen bool
	// Timeout is the remote process deadline. Start and Run send a positive
	// value to envd through Connect-Timeout-Ms; envd terminates the process when
	// that deadline expires. A non-positive value leaves the process without a
	// client-specified deadline.
	Timeout time.Duration
}

// Result is the terminal state and exact output collected from a process. PID
// is zero only when Run failed before receiving StartEvent. TimedOut is set
// only when Request.Timeout expires, not when the caller context is canceled
// or reaches its own deadline.
type Result struct {
	PID      uint32
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

func handleIncomingEvent(
	streamCtx context.Context,
	hasRemoteTimeout bool,
	state *processState,
	received receivedEvent,
	ok bool,
) runEventOutcome {
	if streamCtx.Err() != nil {
		return runEventOutcome{action: runEventStop}
	}
	if received.err != nil && isRemoteTimeout(
		hasRemoteTimeout, received.err,
	) {
		return runEventOutcome{action: runEventTimedOut}
	}
	return handleReceivedEvent(state, received, ok)
}

func finishProcessEvent(
	streamCtx context.Context,
	state *processState,
	outcome runEventOutcome,
) (error, bool) {
	switch outcome.action {
	case runEventContinue:
		return nil, false
	case runEventComplete:
		state.exitCode = int(outcome.exitCode)
		return nil, true
	case runEventStop:
		return finishStoppedProcess(streamCtx, state), true
	case runEventTimedOut:
		state.timedOut = true
		return nil, true
	case runEventFail:
		return outcome.err, true
	default:
		return errors.New(
			"envd process: unknown run event action",
		), true
	}
}

// newStartStreamContext keeps the caller deadline out of Connect-Timeout-Ms.
// The startup path and stream consumer observe the caller context directly;
// this context carries only the configured process timeout and disconnect.
func newStartStreamContext(
	ctx context.Context,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	detachedCtx := context.WithoutCancel(ctx)
	var (
		deadlineCtx    context.Context
		cancelDeadline context.CancelFunc
	)
	if timeout > 0 {
		deadlineCtx, cancelDeadline = context.WithTimeoutCause(
			detachedCtx, timeout, errProcessTimeout,
		)
	} else {
		deadlineCtx, cancelDeadline = context.WithCancel(detachedCtx)
	}
	streamCtx, cancelStream := context.WithCancelCause(deadlineCtx)
	return streamCtx, func() {
		cancelStream(errProcessDisconnected)
		cancelDeadline()
	}
}

func finishStoppedProcess(
	streamCtx context.Context,
	state *processState,
) error {
	cause := context.Cause(streamCtx)
	if errors.Is(cause, errProcessTimeout) {
		state.timedOut = true
		return nil
	}
	if errors.Is(cause, errProcessDisconnected) {
		return context.Canceled
	}
	if cause != nil {
		return cause
	}
	return streamCtx.Err()
}

func isRemoteTimeout(configured bool, err error) bool {
	return configured && connect.CodeOf(err) == connect.CodeDeadlineExceeded
}

type runEventAction uint8

const (
	runEventContinue runEventAction = iota
	runEventComplete
	runEventStop
	runEventTimedOut
	runEventFail
)

type runEventOutcome struct {
	action   runEventAction
	exitCode int32
	err      error
}

func handleReceivedEvent(
	state *processState,
	received receivedEvent,
	ok bool,
) runEventOutcome {
	if !ok {
		return failedRunEvent(
			errors.New("envd process: stream ended without EndEvent"),
		)
	}
	if received.err != nil {
		return failedRunEvent(
			fmt.Errorf("envd process: receive stream: %w", received.err),
		)
	}
	if received.event == nil {
		return failedRunEvent(errors.New("envd process: received empty event"))
	}

	switch event := received.event.Event.(type) {
	case *process.ProcessEvent_Start:
		if event.Start == nil || event.Start.Pid == 0 {
			return failedRunEvent(
				errors.New("envd process: received invalid StartEvent"),
			)
		}
		return failedRunEvent(
			errors.New("envd process: received duplicate StartEvent"),
		)
	case *process.ProcessEvent_Data:
		if err := state.appendData(event.Data); err != nil {
			return failedRunEvent(err)
		}
		return runEventOutcome{action: runEventContinue}
	case *process.ProcessEvent_End:
		if event.End == nil {
			return failedRunEvent(
				errors.New("envd process: received empty EndEvent"),
			)
		}
		if !event.End.Exited {
			return failedRunEvent(endEventError(event.End))
		}
		return runEventOutcome{
			action:   runEventComplete,
			exitCode: event.End.ExitCode,
		}
	case *process.ProcessEvent_Keepalive:
		return runEventOutcome{action: runEventContinue}
	default:
		return failedRunEvent(errors.New("envd process: received unknown event"))
	}
}

func endEventError(event *process.ProcessEvent_EndEvent) error {
	details := make([]string, 0, 2)
	if status := strings.TrimSpace(event.Status); status != "" {
		details = append(details, fmt.Sprintf("status=%q", status))
	}
	if message := strings.TrimSpace(event.GetError()); message != "" {
		details = append(details, fmt.Sprintf("error=%q", message))
	}
	if len(details) == 0 {
		return errors.New("envd process: process ended without exiting")
	}
	return fmt.Errorf(
		"envd process: process ended without exiting: %s",
		strings.Join(details, ", "),
	)
}

func (s *processState) appendData(
	event *process.ProcessEvent_DataEvent,
) error {
	if event == nil {
		return errors.New("envd process: received empty DataEvent")
	}
	switch output := event.Output.(type) {
	case *process.ProcessEvent_DataEvent_Stdout:
		_, _ = s.stdout.Write(output.Stdout)
		return nil
	case *process.ProcessEvent_DataEvent_Stderr:
		_, _ = s.stderr.Write(output.Stderr)
		return nil
	case *process.ProcessEvent_DataEvent_Pty:
		return errors.New("envd process: received PTY data for non-PTY process")
	default:
		return errors.New("envd process: received DataEvent without output")
	}
}

func failedRunEvent(err error) runEventOutcome {
	return runEventOutcome{action: runEventFail, err: err}
}

type startCall struct {
	stream *connect.ServerStreamForClient[process.StartResponse]
	err    error
}

type receivedEvent struct {
	event *process.ProcessEvent
	err   error
}

func receive(
	ctx context.Context,
	stream *connect.ServerStreamForClient[process.StartResponse],
) <-chan receivedEvent {
	events := make(chan receivedEvent)
	go func() {
		defer close(events)
		defer stream.Close()
		for stream.Receive() {
			msg := stream.Msg()
			var event *process.ProcessEvent
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
