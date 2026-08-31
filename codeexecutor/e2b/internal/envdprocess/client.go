//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package envdprocess runs non-interactive processes through E2B envd.
package envdprocess

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"connectrpc.com/connect"

	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec/processconnect"
)

// Client runs non-interactive processes through the envd Process service.
// A Client is safe for concurrent use.
type Client struct {
	processClient processconnect.ProcessClient
	headers       http.Header
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
		processClient: processconnect.NewProcessClient(
			httpClient,
			strings.TrimRight(baseURL, "/"),
		),
		headers: headers.Clone(),
	}, nil
}

// Run starts a process without a PTY and waits for its terminal EndEvent.
// Non-zero exits from an EndEvent with Exited set are returned in Result with a
// nil error. Transport, protocol, stdin, and failed EndEvent failures are
// returned as errors. Canceling ctx disconnects the Start stream but does not
// terminate the remote process. Set Request.Timeout to give envd a remote
// process deadline.
func (c *Client) Run(ctx context.Context, req Request) (Result, error) {
	proc, err := c.Start(ctx, req)
	if err != nil {
		if proc != nil {
			proc.Disconnect()
			result, _ := proc.snapshot()
			return result, err
		}
		if ctx != nil && ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if isRemoteTimeout(req.Timeout > 0, err) || errors.Is(
			err, context.DeadlineExceeded,
		) {
			return Result{TimedOut: true}, nil
		}
		return Result{}, err
	}
	defer proc.Disconnect()
	return proc.Wait(ctx)
}

// Start starts a non-PTY process and returns a handle after envd reports its
// PID. The returned Process can be non-nil together with an error when initial
// stdin delivery fails; callers can then use Process.Kill for cleanup.
func (c *Client) Start(ctx context.Context, req Request) (*Process, error) {
	if err := c.validateRequest(ctx, req); err != nil {
		return nil, err
	}

	streamCtx, disconnect := newStartStreamContext(ctx, req.Timeout)
	startCalls := make(chan startCall, 1)
	go c.startStream(streamCtx, req, startCalls)

	started, err := waitForStartCall(ctx, streamCtx, startCalls)
	if err != nil {
		disconnect()
		return nil, err
	}
	if started.err != nil {
		streamErr := streamContextError(ctx, streamCtx)
		disconnect()
		if streamErr != nil {
			return nil, streamErr
		}
		return nil, fmt.Errorf("envd process: start: %w", started.err)
	}
	if started.stream == nil {
		disconnect()
		return nil, errors.New("envd process: start returned a nil stream")
	}

	events := receive(streamCtx, started.stream)
	pid, err := waitForProcessStart(ctx, streamCtx, events)
	if err != nil {
		disconnect()
		return nil, err
	}
	proc := newProcess(
		c, pid, ctx, streamCtx, req.Timeout > 0, disconnect, events,
	)

	if req.Stdin != "" {
		if err := proc.SendInput(streamCtx, []byte(req.Stdin)); err != nil {
			return proc, fmt.Errorf("envd process: write stdin: %w", err)
		}
		if !req.KeepStdinOpen {
			if err := proc.CloseStdin(streamCtx); err != nil {
				return proc, fmt.Errorf("envd process: close stdin: %w", err)
			}
		}
	}
	return proc, nil
}

// Connect attaches to an existing non-PTY process by PID. Canceling ctx or
// calling Process.Disconnect closes only this event stream.
func (c *Client) Connect(ctx context.Context, pid uint32) (*Process, error) {
	if err := c.validateOperation(ctx, pid); err != nil {
		return nil, err
	}
	streamCtx, cancelStream := context.WithCancelCause(ctx)
	disconnect := func() {
		cancelStream(errProcessDisconnected)
	}
	connectReq := connect.NewRequest(&process.ConnectRequest{
		Process: pidSelector(pid),
	})
	c.addHeaders(connectReq.Header())
	stream, err := c.processClient.Connect(streamCtx, connectReq)
	if err != nil {
		disconnect()
		return nil, fmt.Errorf("envd process: connect: %w", err)
	}
	if stream == nil {
		disconnect()
		return nil, errors.New("envd process: connect returned a nil stream")
	}
	events := receiveConnected(streamCtx, stream)
	connectedPID, err := waitForProcessStart(ctx, streamCtx, events)
	if err != nil {
		disconnect()
		return nil, err
	}
	if connectedPID != pid {
		disconnect()
		return nil, fmt.Errorf(
			"envd process: connect returned PID %d, want %d",
			connectedPID, pid,
		)
	}
	return newProcess(
		c, pid, ctx, streamCtx, false, disconnect, events,
	), nil
}

// List returns the processes currently known to envd.
func (c *Client) List(ctx context.Context) ([]ProcessInfo, error) {
	if ctx == nil {
		return nil, errors.New("envd process: nil context")
	}
	if c == nil || c.processClient == nil {
		return nil, errors.New("envd process: client is not initialized")
	}
	listReq := connect.NewRequest(&process.ListRequest{})
	c.addHeaders(listReq.Header())
	resp, err := c.processClient.List(ctx, listReq)
	if err != nil {
		return nil, fmt.Errorf("envd process: list: %w", err)
	}
	if resp == nil || resp.Msg == nil {
		return nil, errors.New("envd process: list returned an empty response")
	}
	infos := make([]ProcessInfo, 0, len(resp.Msg.Processes))
	for _, item := range resp.Msg.Processes {
		if item == nil {
			continue
		}
		info := ProcessInfo{
			PID: item.Pid,
			Tag: item.GetTag(),
		}
		if config := item.Config; config != nil {
			info.Cmd = config.Cmd
			info.Args = append([]string(nil), config.Args...)
			info.Envs = cloneStrings(config.Envs)
			info.Cwd = config.GetCwd()
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// Kill sends SIGKILL to a process. It returns false without an error when envd
// reports that the process does not exist.
func (c *Client) Kill(ctx context.Context, pid uint32) (bool, error) {
	if err := c.validateOperation(ctx, pid); err != nil {
		return false, err
	}
	request := connect.NewRequest(&process.SendSignalRequest{
		Process: pidSelector(pid),
		Signal:  process.Signal_SIGNAL_SIGKILL,
	})
	c.addHeaders(request.Header())
	_, err := c.processClient.SendSignal(ctx, request)
	if connect.CodeOf(err) == connect.CodeNotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("envd process: kill %d: %w", pid, err)
	}
	return true, nil
}

// SendInput writes bytes to an open process stdin.
func (c *Client) SendInput(
	ctx context.Context,
	pid uint32,
	input []byte,
) error {
	if err := c.validateOperation(ctx, pid); err != nil {
		return err
	}
	inputReq := connect.NewRequest(&process.SendInputRequest{
		Process: pidSelector(pid),
		Input: &process.ProcessInput{
			Input: &process.ProcessInput_Stdin{
				Stdin: append([]byte(nil), input...),
			},
		},
	})
	c.addHeaders(inputReq.Header())
	if _, err := c.processClient.SendInput(ctx, inputReq); err != nil {
		return fmt.Errorf("envd process: send input to %d: %w", pid, err)
	}
	return nil
}

// CloseStdin closes a process stdin and signals EOF.
func (c *Client) CloseStdin(ctx context.Context, pid uint32) error {
	if err := c.validateOperation(ctx, pid); err != nil {
		return err
	}
	closeReq := connect.NewRequest(&process.CloseStdinRequest{
		Process: pidSelector(pid),
	})
	c.addHeaders(closeReq.Header())
	if _, err := c.processClient.CloseStdin(ctx, closeReq); err != nil {
		return fmt.Errorf("envd process: close stdin for %d: %w", pid, err)
	}
	return nil
}

func (c *Client) startStream(
	ctx context.Context,
	req Request,
	result chan<- startCall,
) {
	stdin := req.Stdin != "" || req.KeepStdinOpen
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

	startReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  req.Cmd,
			Args: args,
			Envs: envs,
			Cwd:  cwd,
		},
		Stdin: &stdin,
	})
	if req.Tag != "" {
		tag := req.Tag
		startReq.Msg.Tag = &tag
	}
	c.addHeaders(startReq.Header())
	addProcessUserHeader(startReq.Header(), req.User)
	stream, err := c.processClient.Start(ctx, startReq)
	select {
	case result <- startCall{stream: stream, err: err}:
	case <-ctx.Done():
	}
}

func (c *Client) addHeaders(target http.Header) {
	for key, values := range c.headers {
		for _, value := range values {
			target.Add(key, value)
		}
	}
}

func (c *Client) validateRequest(ctx context.Context, req Request) error {
	if ctx == nil {
		return errors.New("envd process: nil context")
	}
	if c == nil || c.processClient == nil {
		return errors.New("envd process: client is not initialized")
	}
	if req.Cmd == "" {
		return errors.New("envd process: command is empty")
	}
	return ctx.Err()
}

func (c *Client) validateOperation(ctx context.Context, pid uint32) error {
	if ctx == nil {
		return errors.New("envd process: nil context")
	}
	if c == nil || c.processClient == nil {
		return errors.New("envd process: client is not initialized")
	}
	if pid == 0 {
		return errors.New("envd process: pid is zero")
	}
	return ctx.Err()
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
