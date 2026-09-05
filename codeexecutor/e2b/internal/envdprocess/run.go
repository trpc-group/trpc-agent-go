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

// defaultProcessTimeout matches the E2B SDK command timeout. It is applied
// before processStreamCtx opens Start so envd always receives an explicit
// remote process deadline.
const defaultProcessTimeout = 60 * time.Second

// Request describes one non-interactive process invocation.
type Request struct {
	Cmd  string
	Args []string
	Envs map[string]string
	Cwd  string
	// User selects the sandbox user through envd's Basic authentication
	// header. An empty value omits the header and lets envd choose its default.
	User string
	// Stdin is initial input sent after envd reports the process PID. Unless
	// KeepStdinOpen is set, it requires CloseStdin support in envd 0.5.2 or
	// newer so the remote process observes EOF.
	Stdin string
	// KeepStdinOpen leaves stdin open after Stdin is sent. It is intended for
	// Start callers that will continue writing through Process.SendInput. Run
	// callers should use it only when the command can finish without stdin EOF.
	KeepStdinOpen bool
	// Timeout is the remote process deadline sent to envd through
	// Connect-Timeout-Ms. envd terminates the process when the deadline expires.
	// A non-positive value uses the E2B-compatible default of 60 seconds.
	Timeout time.Duration
}

// Result is the terminal state and captured output from a process. PID is zero
// only when Run failed before receiving StartEvent. TimedOut is set only when
// the configured or default remote process deadline expires, not when the
// caller context is canceled or reaches its own deadline. Truncation flags are
// set only when the corresponding Client capture limit discards output;
// capture is exact and unlimited by default.
type Result struct {
	PID             uint32
	Stdout          string
	Stderr          string
	ExitCode        int
	TimedOut        bool
	StdoutTruncated bool
	StderrTruncated bool
}

func handleIncomingEvent(
	processStreamCtx context.Context,
	hasRemoteTimeout bool,
	state *processState,
	received receivedEvent,
	ok bool,
) runEventOutcome {
	if processStreamCtx.Err() != nil {
		return runEventOutcome{action: runEventStop}
	}
	if received.err != nil && hasRemoteTimeout &&
		isRemoteTimeout(received.err) {
		return runEventOutcome{action: runEventTimedOut}
	}
	return handleReceivedEvent(state, received, ok)
}

func finishProcessEvent(
	processStreamCtx context.Context,
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
		return finishStoppedProcess(processStreamCtx, state), true
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

// newStartStreamContext creates the single context that owns the Start RPC and
// its event-stream attachment. It retains caller values but detaches the
// caller's cancellation and deadline before adding the remote process timeout;
// this prevents Connect from encoding the caller deadline as
// Connect-Timeout-Ms. Caller cancellation is then mirrored with AfterFunc so
// it disconnects the local attachment without sending a signal to the remote
// process. The timeout remains the envd-owned process deadline, while the
// returned cancel function represents an explicit local Disconnect.
func newStartStreamContext(
	callerCtx context.Context,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	detachedCtx := context.WithoutCancel(callerCtx)
	deadlineCtx, cancelDeadline := context.WithTimeoutCause(
		detachedCtx, normalizeProcessTimeout(timeout), errProcessTimeout,
	)
	processStreamCtx, cancelStream := context.WithCancelCause(deadlineCtx)
	stopCallerCancellation := context.AfterFunc(callerCtx, func() {
		cancelStream(context.Cause(callerCtx))
	})
	if cause := context.Cause(callerCtx); cause != nil {
		cancelStream(cause)
	}
	return processStreamCtx, func() {
		stopCallerCancellation()
		cancelStream(errProcessDisconnected)
		cancelDeadline()
	}
}

func normalizeProcessTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultProcessTimeout
	}
	return timeout
}

func finishStoppedProcess(
	processStreamCtx context.Context,
	state *processState,
) error {
	cause := context.Cause(processStreamCtx)
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
	return processStreamCtx.Err()
}

func isRemoteTimeout(err error) bool {
	return connect.CodeOf(err) == connect.CodeDeadlineExceeded
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
		s.stdout.append(output.Stdout)
		return nil
	case *process.ProcessEvent_DataEvent_Stderr:
		s.stderr.append(output.Stderr)
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

type receivedEvent struct {
	event *process.ProcessEvent
	err   error
}
