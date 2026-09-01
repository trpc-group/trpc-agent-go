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
	"net"
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
	processClient      processconnect.ProcessClient
	headers            http.Header
	credentialsAllowed bool
}

// NewClient constructs an envd process client. Headers are snapshotted and
// added to every RPC. The supplied HTTP client configuration is copied so
// custom transports, TLS configuration, proxies, redirect policies, and
// request timeouts remain effective without mutating the caller's client.
// Remote endpoints must use HTTPS. Loopback HTTP is accepted only without
// configured headers or per-process user credentials. Every outbound request
// is restricted to the configured envd origin, including requests produced by
// redirects.
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
	if u.User != nil {
		return nil, errors.New(
			"envd process: base URL must not contain user credentials",
		)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf(
			"envd process: unsupported base URL scheme %q", u.Scheme,
		)
	}
	credentialsAllowed := u.Scheme == "https"
	if !credentialsAllowed && !isLoopbackHost(u.Hostname()) {
		return nil, errors.New(
			"envd process: remote base URL must use HTTPS",
		)
	}
	if !credentialsAllowed && len(headers) != 0 {
		return nil, errors.New(
			"envd process: configured headers require HTTPS",
		)
	}
	httpClient = newOriginBoundHTTPClient(httpClient, u)
	return &Client{
		processClient: processconnect.NewProcessClient(
			httpClient,
			strings.TrimRight(baseURL, "/"),
		),
		headers:            headers.Clone(),
		credentialsAllowed: credentialsAllowed,
	}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func newOriginBoundHTTPClient(
	httpClient *http.Client,
	baseURL *url.URL,
) *http.Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	boundClient := *httpClient
	transport := httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	boundClient.Transport = &originBoundRoundTripper{
		base:   transport,
		origin: originFromURL(baseURL),
	}
	return &boundClient
}

type originBoundRoundTripper struct {
	base   http.RoundTripper
	origin string
}

// RoundTrip checks the final request URL at the transport seam, after any
// redirect policy has run, so configured credentials cannot leave the envd
// origin even when a caller-supplied HTTP client follows redirects.
func (t *originBoundRoundTripper) RoundTrip(
	req *http.Request,
) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("envd process: request URL is nil")
	}
	if originFromURL(req.URL) != t.origin {
		return nil, errors.New(
			"envd process: refusing request outside configured origin",
		)
	}
	return t.base.RoundTrip(req)
}

func originFromURL(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return strings.ToLower(u.Scheme) + "://" +
		strings.ToLower(u.Hostname()) + ":" + port
}

// Run starts a process without a PTY and waits for its terminal EndEvent.
// Non-zero exits from an EndEvent with Exited set are returned in Result with a
// nil error. Transport, protocol, stdin, and failed EndEvent failures are
// returned as errors. Canceling ctx disconnects the Start stream but does not
// terminate the remote process. Request.Timeout controls envd's remote process
// deadline and defaults to 60 seconds when non-positive.
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
		if isRemoteTimeout(err) ||
			errors.Is(err, context.DeadlineExceeded) {
			return Result{TimedOut: true}, nil
		}
		return Result{}, err
	}
	defer proc.Disconnect()
	return proc.Wait(ctx)
}

// Start starts a non-PTY process and returns a handle after envd reports its
// PID. The returned Process can be non-nil together with an error when initial
// stdin delivery fails; callers can then use Process.Kill for cleanup. A
// non-positive Request.Timeout uses a 60-second remote process deadline.
func (c *Client) Start(ctx context.Context, req Request) (*Process, error) {
	if err := c.validateRequest(ctx, req); err != nil {
		return nil, err
	}
	// processStreamCtx owns the Start RPC, initial stdin RPCs, and event-stream
	// attachment, but not the remote process itself. Its deadline is the remote
	// process deadline, while caller cancellation only disconnects the local
	// attachment and never sends Kill to envd.
	processStreamCtx, disconnect := newStartStreamContext(ctx, req.Timeout)
	stream, err := c.startStream(processStreamCtx, req)
	if err != nil {
		streamErr := streamContextError(processStreamCtx)
		disconnect()
		if streamErr != nil {
			return nil, streamErr
		}
		return nil, fmt.Errorf("envd process: start: %w", err)
	}
	if stream == nil {
		disconnect()
		return nil, errors.New("envd process: start returned a nil stream")
	}

	eventStream := &startEventStream{stream: stream}
	pid, err := receiveProcessStart(processStreamCtx, eventStream)
	if err != nil {
		disconnect()
		_ = eventStream.Close()
		return nil, err
	}
	proc := newProcess(c, pid, disconnect)
	stdinErr := initializeProcessStdin(processStreamCtx, proc, req)
	proc.startConsumer(processStreamCtx, true, eventStream)
	if stdinErr != nil {
		return proc, stdinErr
	}
	return proc, nil
}

func initializeProcessStdin(
	ctx context.Context,
	proc *Process,
	req Request,
) error {
	if req.Stdin == "" {
		return nil
	}
	if err := proc.SendInput(ctx, []byte(req.Stdin)); err != nil {
		return fmt.Errorf("envd process: write stdin: %w", err)
	}
	if req.KeepStdinOpen {
		return nil
	}
	if err := proc.CloseStdin(ctx); err != nil {
		return fmt.Errorf("envd process: close stdin: %w", err)
	}
	return nil
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
	eventStream := &connectEventStream{stream: stream}
	connectedPID, err := receiveProcessStart(streamCtx, eventStream)
	if err != nil {
		disconnect()
		_ = eventStream.Close()
		return nil, err
	}
	if connectedPID != pid {
		disconnect()
		_ = eventStream.Close()
		return nil, fmt.Errorf(
			"envd process: connect returned PID %d, want %d",
			connectedPID, pid,
		)
	}
	proc := newProcess(c, pid, disconnect)
	proc.startConsumer(streamCtx, false, eventStream)
	return proc, nil
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

// startStream synchronously opens the server-streaming RPC. The Connect client
// observes ctx while sending the request, so an extra goroutine would not add
// cancellation semantics and could only leave an orphaned request behind.
func (c *Client) startStream(
	ctx context.Context,
	req Request,
) (*connect.ServerStreamForClient[process.StartResponse], error) {
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
	return c.processClient.Start(ctx, startReq)
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
	if req.User != "" && !c.credentialsAllowed {
		return errors.New("envd process: process user requires HTTPS")
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
