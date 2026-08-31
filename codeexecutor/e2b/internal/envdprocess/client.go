//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package envdprocess runs non-interactive processes through E2B envd.
package envdprocess

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"

	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec/processconnect"
)

const (
	defaultCleanupTimeout = 2 * time.Second
	cleanupRetryInterval  = 25 * time.Millisecond
)

// Request describes one non-interactive process invocation. Timeout bounds
// the whole invocation; a non-positive timeout leaves it to the caller context.
type Request struct {
	Cmd  string
	Args []string
	Envs map[string]string
	Cwd  string
	// User selects the sandbox user through envd's Basic authentication
	// header. An empty value omits the header and lets envd choose its default.
	User    string
	Stdin   string
	Timeout time.Duration
}

// Result is the terminal state and exact output collected from a process.
// TimedOut is set only when Request.Timeout expires, not when the caller
// context is canceled or reaches its own deadline.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

// Client runs non-interactive processes through the envd Process service.
// A Client is safe for concurrent use.
type Client struct {
	rpc            processconnect.ProcessClient
	headers        http.Header
	cleanupTimeout time.Duration
	tagSequence    atomic.Uint64
}

// NewClient constructs an envd process client. Headers are snapshotted and
// added to every RPC. The supplied HTTP client is reused so custom transports,
// TLS configuration, proxies, and request timeouts remain effective.
func NewClient(
	baseURL string,
	httpClient *http.Client,
	headers http.Header,
) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("envd process: invalid base URL %q", baseURL)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		rpc: processconnect.NewProcessClient(
			httpClient,
			strings.TrimRight(baseURL, "/"),
		),
		headers:        headers.Clone(),
		cleanupTimeout: defaultCleanupTimeout,
	}, nil
}

// Run starts a process without a PTY and waits for its terminal EndEvent.
// Non-zero exits are returned in Result with a nil error. Transport, protocol,
// stdin, and cleanup failures are returned as errors. On timeout or caller
// cancellation, Run explicitly sends SIGKILL before returning.
func (c *Client) Run(ctx context.Context, req Request) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("envd process: nil context")
	}
	if c == nil || c.rpc == nil {
		return Result{}, errors.New("envd process: client is not initialized")
	}
	if req.Cmd == "" {
		return Result{}, errors.New("envd process: command is empty")
	}

	runCtx := ctx
	cancelRun := func() {}
	if req.Timeout > 0 {
		runCtx, cancelRun = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancelRun()

	// Envd intentionally decouples process lifetime from the Start stream.
	// Keep the stream alive until this method has explicitly handled caller
	// cancellation or timeout and terminated the remote process.
	streamCtx, cancelStream := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelStream()

	tag := c.nextTag()
	startCalls := make(chan startCall, 1)
	go c.start(streamCtx, req, tag, startCalls)

	state := runState{}
	var events <-chan receivedEvent

	for {
		select {
		case started := <-startCalls:
			startCalls = nil
			if started.err != nil {
				cleanupErr := c.terminate(ctx, 0, tag, false)
				return state.result(0), errors.Join(
					fmt.Errorf("envd process: start: %w", started.err),
					cleanupErr,
				)
			}
			if started.stream == nil {
				err := errors.New("envd process: start returned a nil stream")
				return c.fail(
					ctx, state.stdout, state.stderr, state.pid, tag, false, err,
				)
			}
			state.streamReady = true
			events = receive(streamCtx, started.stream)

		case received, ok := <-events:
			outcome := c.handleReceivedEvent(
				runCtx, req.Stdin, &state, received, ok,
			)
			switch outcome.action {
			case runEventContinue:
				continue
			case runEventComplete:
				return outcome.result, nil
			case runEventStop:
				return c.stop(
					ctx, runCtx, state.stdout, state.stderr, state.pid, tag,
				)
			case runEventFail:
				return c.fail(
					ctx, state.stdout, state.stderr, state.pid, tag,
					state.streamReady, outcome.err,
				)
			}

		case <-runCtx.Done():
			return c.stop(
				ctx, runCtx, state.stdout, state.stderr, state.pid, tag,
			)
		}
	}
}

type runState struct {
	stdout      bytes.Buffer
	stderr      bytes.Buffer
	pid         uint32
	streamReady bool
}

func (s *runState) result(exitCode int32) Result {
	result := resultWithOutput(s.stdout, s.stderr)
	result.ExitCode = int(exitCode)
	return result
}

type runEventAction uint8

const (
	runEventContinue runEventAction = iota
	runEventComplete
	runEventStop
	runEventFail
)

type runEventOutcome struct {
	action runEventAction
	result Result
	err    error
}

func (c *Client) handleReceivedEvent(
	ctx context.Context,
	stdin string,
	state *runState,
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
		return c.handleStartEvent(ctx, stdin, state, event.Start)
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
		if state.pid == 0 {
			return failedRunEvent(
				errors.New("envd process: received EndEvent before StartEvent"),
			)
		}
		return runEventOutcome{
			action: runEventComplete,
			result: state.result(event.End.ExitCode),
		}
	case *process.ProcessEvent_Keepalive:
		return runEventOutcome{action: runEventContinue}
	default:
		return failedRunEvent(errors.New("envd process: received unknown event"))
	}
}

func (c *Client) handleStartEvent(
	ctx context.Context,
	stdin string,
	state *runState,
	event *process.ProcessEvent_StartEvent,
) runEventOutcome {
	if event == nil || event.Pid == 0 {
		return failedRunEvent(
			errors.New("envd process: received invalid StartEvent"),
		)
	}
	if state.pid != 0 {
		return failedRunEvent(
			errors.New("envd process: received duplicate StartEvent"),
		)
	}
	state.pid = event.Pid
	if err := c.writeStdin(ctx, state.pid, stdin); err != nil {
		if ctx.Err() != nil {
			return runEventOutcome{action: runEventStop}
		}
		return failedRunEvent(fmt.Errorf("envd process: write stdin: %w", err))
	}
	return runEventOutcome{action: runEventContinue}
}

func (s *runState) appendData(event *process.ProcessEvent_DataEvent) error {
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

func (c *Client) start(
	ctx context.Context,
	req Request,
	tag string,
	result chan<- startCall,
) {
	stdin := req.Stdin != ""
	args := append([]string(nil), req.Args...)
	envs := make(map[string]string, len(req.Envs))
	for key, value := range req.Envs {
		envs[key] = value
	}
	var cwd *string
	if req.Cwd != "" {
		cwdValue := req.Cwd
		cwd = &cwdValue
	}

	rpcReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  req.Cmd,
			Args: args,
			Envs: envs,
			Cwd:  cwd,
		},
		Tag:   &tag,
		Stdin: &stdin,
	})
	c.addHeaders(rpcReq.Header())
	addProcessUserHeader(rpcReq.Header(), req.User)
	stream, err := c.rpc.Start(ctx, rpcReq)
	select {
	case result <- startCall{stream: stream, err: err}:
	case <-ctx.Done():
	}
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

func (c *Client) writeStdin(
	ctx context.Context,
	pid uint32,
	stdin string,
) error {
	if stdin == "" {
		return nil
	}
	selector := pidSelector(pid)
	inputReq := connect.NewRequest(&process.SendInputRequest{
		Process: selector,
		Input: &process.ProcessInput{
			Input: &process.ProcessInput_Stdin{Stdin: []byte(stdin)},
		},
	})
	c.addHeaders(inputReq.Header())
	if _, err := c.rpc.SendInput(ctx, inputReq); err != nil {
		return err
	}
	closeReq := connect.NewRequest(&process.CloseStdinRequest{
		Process: selector,
	})
	c.addHeaders(closeReq.Header())
	_, err := c.rpc.CloseStdin(ctx, closeReq)
	return err
}

func (c *Client) stop(
	ctx context.Context,
	runCtx context.Context,
	stdout bytes.Buffer,
	stderr bytes.Buffer,
	pid uint32,
	tag string,
) (Result, error) {
	cleanupErr := c.terminate(ctx, pid, tag, pid == 0)
	result := resultWithOutput(stdout, stderr)
	if ctx.Err() != nil {
		return result, errors.Join(ctx.Err(), cleanupErr)
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.TimedOut = true
		return result, cleanupErr
	}
	return result, errors.Join(runCtx.Err(), cleanupErr)
}

func (c *Client) fail(
	ctx context.Context,
	stdout bytes.Buffer,
	stderr bytes.Buffer,
	pid uint32,
	tag string,
	streamReady bool,
	err error,
) (Result, error) {
	cleanupErr := c.terminate(ctx, pid, tag, streamReady)
	return resultWithOutput(stdout, stderr), errors.Join(err, cleanupErr)
}

func (c *Client) terminate(
	parent context.Context,
	pid uint32,
	tag string,
	waitForTag bool,
) error {
	cleanupCtx, cancel := context.WithTimeout(
		context.WithoutCancel(parent), c.cleanupTimeout,
	)
	defer cancel()

	selector := pidSelector(pid)
	if pid == 0 {
		selector = tagSelector(tag)
	}
	for {
		req := connect.NewRequest(&process.SendSignalRequest{
			Process: selector,
			Signal:  process.Signal_SIGNAL_SIGKILL,
		})
		c.addHeaders(req.Header())
		_, err := c.rpc.SendSignal(cleanupCtx, req)
		if err == nil {
			return nil
		}
		if connect.CodeOf(err) != connect.CodeNotFound {
			return fmt.Errorf("envd process: send SIGKILL: %w", err)
		}
		if pid != 0 || !waitForTag {
			return nil
		}
		select {
		case <-cleanupCtx.Done():
			return fmt.Errorf(
				"envd process: locate process tagged %q for cleanup: %w",
				tag, cleanupCtx.Err(),
			)
		case <-time.After(cleanupRetryInterval):
		}
	}
}

func (c *Client) nextTag() string {
	return fmt.Sprintf(
		"trpc-agent-go-%d-%d",
		time.Now().UnixNano(), c.tagSequence.Add(1),
	)
}

func (c *Client) addHeaders(target http.Header) {
	for key, values := range c.headers {
		for _, value := range values {
			target.Add(key, value)
		}
	}
}

func addProcessUserHeader(target http.Header, user string) {
	if user == "" {
		return
	}
	credentials := base64.StdEncoding.EncodeToString([]byte(user + ":"))
	target.Set("Authorization", "Basic "+credentials)
}

func pidSelector(pid uint32) *process.ProcessSelector {
	return &process.ProcessSelector{
		Selector: &process.ProcessSelector_Pid{Pid: pid},
	}
}

func tagSelector(tag string) *process.ProcessSelector {
	return &process.ProcessSelector{
		Selector: &process.ProcessSelector_Tag{Tag: tag},
	}
}

func resultWithOutput(stdout, stderr bytes.Buffer) Result {
	return Result{Stdout: stdout.String(), Stderr: stderr.String()}
}
