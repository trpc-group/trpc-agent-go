//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build integration

package envdprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec/processconnect"
)

const (
	integrationAPIURLVariable                = "E2B_API_URL"
	integrationAPIKeyVariable                = "E2B_API_KEY"
	integrationDomainVariable                = "E2B_DOMAIN"
	integrationTemplateVariable              = "E2B_TEMPLATE"
	integrationEnvdUserVariable              = "E2B_ENVD_USER"
	integrationCubeSandboxCompatibleVariable = "E2B_CUBESANDBOX_COMPATIBLE"
	integrationDefaultAPIURL                 = "https://api.e2b.app"
	integrationDefaultDomain                 = "e2b.app"
	integrationDefaultTemplate               = "code-interpreter-v1"
	integrationDefaultE2BUser                = "user"
	integrationDefaultCubeSandboxUser        = "root"
	integrationEnvdPort                      = 49983
	integrationTestTimeout                   = 5 * time.Minute
	integrationOperationTimeout              = 30 * time.Second
	integrationSandboxTimeout                = 5 * time.Minute
)

// TestIntegrationEnvdProcess creates a sandbox through the E2B-compatible
// management API and validates every vendored envd Process RPC against it.
// The envd and traffic access tokens are taken only from that create response;
// they are never accepted as environment configuration.
//
// Run against E2B with:
//
//	E2B_API_KEY='<api-key>' go test -tags=integration \
//	  ./codeexecutor/e2b/internal/envdprocess \
//	  -run '^TestIntegrationEnvdProcess$' -count=1 -v
//
// E2B_API_URL, E2B_DOMAIN, E2B_TEMPLATE, and E2B_ENVD_USER override their
// standard defaults.
// Set E2B_CUBESANDBOX_COMPATIBLE=true when the data plane also requires the
// CubeSandbox-compatible Cube-Traffic-Access-Token header.
func TestIntegrationEnvdProcess(t *testing.T) {
	config := integrationConfigFromEnvironment(t)
	httpClient := &http.Client{}

	ctx, cancel := context.WithTimeout(context.Background(), integrationTestTimeout)
	t.Cleanup(cancel)
	sandbox := createIntegrationSandbox(ctx, t, httpClient, config)

	rpc := processconnect.NewProcessClient(httpClient, sandbox.baseURL)
	runner, err := NewClient(sandbox.baseURL, httpClient, sandbox.headers)
	require.NoError(t, err)
	supportsCloseStdin := integrationSupportsCloseStdin(
		ctx,
		t,
		rpc,
		sandbox.headers,
	)
	t.Logf("created integration sandbox: envd_version=%q close_stdin=%t",
		sandbox.envdVersion, supportsCloseStdin)

	testEnv := &integrationEnvironment{
		rpc:                rpc,
		runner:             runner,
		headers:            sandbox.headers,
		user:               config.envdUser,
		supportsCloseStdin: supportsCloseStdin,
	}
	t.Cleanup(func() { testEnv.cleanupProcesses(t) })

	t.Run("ProcessProtocolNonPTY", func(t *testing.T) {
		testEnv.testNonPTYProtocol(ctx, t)
	})
	t.Run("ProcessProtocolCloseStdin", func(t *testing.T) {
		testEnv.testCloseStdinProtocol(ctx, t)
	})
	t.Run("ProcessProtocolSignal", func(t *testing.T) {
		testEnv.testSignalProtocol(ctx, t)
	})
	t.Run("ProcessProtocolPTY", func(t *testing.T) {
		testEnv.testPTYProtocol(ctx, t)
	})
	t.Run("Run", func(t *testing.T) {
		testEnv.testRun(ctx, t)
	})
	t.Run("RunWithStdin", func(t *testing.T) {
		testEnv.testRunWithStdin(ctx, t)
	})
	t.Run("RunTimeout", func(t *testing.T) {
		testEnv.testRunTimeout(ctx, t)
	})
	t.Run("ProcessHandleLifecycle", func(t *testing.T) {
		testEnv.testProcessHandleLifecycle(ctx, t)
	})
}

type integrationConfig struct {
	apiURL                string
	apiKey                string
	domain                string
	template              string
	envdUser              string
	cubeSandboxCompatible bool
}

func integrationConfigFromEnvironment(t *testing.T) integrationConfig {
	t.Helper()
	apiKey := strings.TrimSpace(os.Getenv(integrationAPIKeyVariable))
	if apiKey == "" {
		t.Skipf("set %s to run the envd integration test", integrationAPIKeyVariable)
	}

	domain := strings.TrimSpace(os.Getenv(integrationDomainVariable))
	if domain == "" {
		domain = integrationDefaultDomain
	}

	apiURL := strings.TrimRight(strings.TrimSpace(os.Getenv(integrationAPIURLVariable)), "/")
	if apiURL == "" {
		if domain == integrationDefaultDomain {
			apiURL = integrationDefaultAPIURL
		} else {
			apiURL = "https://api." + domain
		}
	}
	parsed, err := url.Parse(apiURL)
	require.NoError(t, err, "%s must be a valid URL", integrationAPIURLVariable)
	require.NotEmpty(t, parsed.Scheme, "%s must include a URL scheme", integrationAPIURLVariable)
	require.NotEmpty(t, parsed.Host, "%s must include a URL host", integrationAPIURLVariable)

	template := strings.TrimSpace(os.Getenv(integrationTemplateVariable))
	if template == "" {
		template = integrationDefaultTemplate
	}

	cubeSandboxCompatible := false
	if raw := strings.TrimSpace(os.Getenv(integrationCubeSandboxCompatibleVariable)); raw != "" {
		cubeSandboxCompatible, err = strconv.ParseBool(raw)
		require.NoError(t, err, "%s must be a boolean", integrationCubeSandboxCompatibleVariable)
	}
	envdUser := strings.TrimSpace(os.Getenv(integrationEnvdUserVariable))
	if envdUser == "" {
		envdUser = integrationDefaultE2BUser
		if cubeSandboxCompatible {
			envdUser = integrationDefaultCubeSandboxUser
		}
	}

	return integrationConfig{
		apiURL:                apiURL,
		apiKey:                apiKey,
		domain:                domain,
		template:              template,
		envdUser:              envdUser,
		cubeSandboxCompatible: cubeSandboxCompatible,
	}
}

type integrationSandbox struct {
	id          string
	baseURL     string
	envdVersion string
	headers     http.Header
}

func createIntegrationSandbox(
	ctx context.Context,
	t *testing.T,
	httpClient *http.Client,
	config integrationConfig,
) integrationSandbox {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"templateID": config.template,
		"timeout":    int(integrationSandboxTimeout / time.Second),
	})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		config.apiURL+"/sandboxes",
		bytes.NewReader(payload),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", config.apiKey)

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		t.Fatalf("create integration sandbox: status=%s body=%s", resp.Status, strings.TrimSpace(string(body)))
	}

	var created struct {
		SandboxID          string `json:"sandboxID"`
		ClientID           string `json:"clientID"`
		EnvdVersion        string `json:"envdVersion"`
		EnvdAccessToken    string `json:"envdAccessToken"`
		TrafficAccessToken string `json:"trafficAccessToken"`
		Domain             string `json:"domain"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.NotEmpty(t, created.SandboxID, "create response must include sandboxID")
	sandbox := integrationSandbox{id: created.SandboxID}
	t.Cleanup(func() { sandbox.kill(t, httpClient, config) })

	domain := created.Domain
	if domain == "" {
		domain = config.domain
	}
	require.NotEmpty(t, domain, "create response or %s must provide the sandbox domain", integrationDomainVariable)

	hostID := created.SandboxID
	if created.Domain == "" && created.ClientID != "" {
		hostID += "-" + created.ClientID
	}
	scheme := "https"
	if parsed, parseErr := url.Parse(config.apiURL); parseErr == nil && parsed.Scheme == "http" {
		scheme = "http"
	}

	headers := make(http.Header)
	if created.EnvdAccessToken != "" {
		headers.Set("X-Access-Token", created.EnvdAccessToken)
	}
	if created.TrafficAccessToken != "" {
		headers.Set("E2B-Traffic-Access-Token", created.TrafficAccessToken)
		if config.cubeSandboxCompatible {
			headers.Set("Cube-Traffic-Access-Token", created.TrafficAccessToken)
		}
	}
	if config.cubeSandboxCompatible {
		require.NotEmpty(t, created.TrafficAccessToken,
			"CubeSandbox-compatible data plane requires trafficAccessToken in create response")
	}

	sandbox.baseURL = fmt.Sprintf("%s://%d-%s.%s", scheme, integrationEnvdPort, hostID, domain)
	sandbox.envdVersion = created.EnvdVersion
	sandbox.headers = headers
	return sandbox
}

func (s integrationSandbox) kill(
	t *testing.T,
	httpClient *http.Client,
	config integrationConfig,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), integrationOperationTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		config.apiURL+"/sandboxes/"+url.PathEscape(s.id),
		nil,
	)
	if err != nil {
		t.Errorf("create sandbox cleanup request: %v", err)
		return
	}
	req.Header.Set("X-API-Key", config.apiKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Errorf("kill integration sandbox %q: %v", s.id, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		t.Errorf("kill integration sandbox %q: status=%s body=%s",
			s.id, resp.Status, strings.TrimSpace(string(body)))
	}
}

type integrationEnvironment struct {
	rpc                processconnect.ProcessClient
	runner             *Client
	headers            http.Header
	user               string
	supportsCloseStdin bool

	mu   sync.Mutex
	pids map[uint32]struct{}
}

func (e *integrationEnvironment) testNonPTYProtocol(
	parent context.Context,
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(parent, integrationOperationTimeout)
	defer cancel()

	tag := integrationTag("nonpty")
	stdin := true
	startReq := integrationStartRequest(e.headers, e.user, &process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd: "/bin/sh",
			Args: []string{
				"-c",
				"IFS= read -r line; printf '__SEND_INPUT_OBSERVED__%s\\n' \"$line\"",
			},
		},
		Tag:   &tag,
		Stdin: &stdin,
	})
	startStream, err := e.rpc.Start(ctx, startReq)
	require.NoError(t, err)
	defer startStream.Close()

	pid := receiveStartPID(t, startStream)
	e.trackPID(pid)

	listResp, err := e.rpc.List(
		ctx,
		integrationRequest(e.headers, &process.ListRequest{}),
	)
	require.NoError(t, err)
	assertProcessListed(t, listResp.Msg.Processes, pid, tag)

	connectStream, err := e.rpc.Connect(ctx, integrationRequest(
		e.headers,
		&process.ConnectRequest{Process: pidSelector(pid)},
	))
	require.NoError(t, err)
	defer connectStream.Close()

	_, err = e.rpc.SendInput(ctx, integrationRequest(
		e.headers,
		&process.SendInputRequest{
			Process: pidSelector(pid),
			Input: &process.ProcessInput{
				Input: &process.ProcessInput_Stdin{
					Stdin: []byte("nonpty-marker\n"),
				},
			},
		},
	))
	require.NoError(t, err)
	startEvents := receiveRemainingStartEvents(t, startStream)
	connectEvents := receiveConnectEvents(t, connectStream)
	e.forgetPID(pid)

	startOutput := collectEventOutput(startEvents, false)
	assert.Contains(t, startOutput, "__SEND_INPUT_OBSERVED__nonpty-marker")
	assertHasEndEvent(t, startEvents)

	connectOutput := collectEventOutput(connectEvents, false)
	assert.Contains(t, connectOutput, "__SEND_INPUT_OBSERVED__")
	assertHasEndEvent(t, connectEvents)
}

func (e *integrationEnvironment) testCloseStdinProtocol(
	parent context.Context,
	t *testing.T,
) {
	if !e.supportsCloseStdin {
		t.Skip("sandbox envd does not implement Process.CloseStdin; E2B requires envd >= 0.5.2")
	}
	ctx, cancel := context.WithTimeout(parent, integrationOperationTimeout)
	defer cancel()

	tag := integrationTag("close-stdin")
	stdin := true
	startStream, err := e.rpc.Start(ctx, integrationStartRequest(
		e.headers,
		e.user,
		&process.StartRequest{
			Process: &process.ProcessConfig{
				Cmd:  "/bin/sh",
				Args: []string{"-c", "cat; printf '__CLOSE_STDIN_OBSERVED__\\n'"},
			},
			Tag:   &tag,
			Stdin: &stdin,
		},
	))
	require.NoError(t, err)
	defer startStream.Close()

	pid := receiveStartPID(t, startStream)
	e.trackPID(pid)
	_, err = e.rpc.SendInput(ctx, integrationRequest(
		e.headers,
		&process.SendInputRequest{
			Process: pidSelector(pid),
			Input: &process.ProcessInput{
				Input: &process.ProcessInput_Stdin{Stdin: []byte("close-stdin-marker\n")},
			},
		},
	))
	require.NoError(t, err)
	_, err = e.rpc.CloseStdin(ctx, integrationRequest(
		e.headers,
		&process.CloseStdinRequest{Process: pidSelector(pid)},
	))
	require.NoError(t, err)

	events := receiveRemainingStartEvents(t, startStream)
	e.forgetPID(pid)
	output := collectEventOutput(events, false)
	assert.Contains(t, output, "close-stdin-marker")
	assert.Contains(t, output, "__CLOSE_STDIN_OBSERVED__")
	assertHasEndEvent(t, events)
}

func (e *integrationEnvironment) testSignalProtocol(
	parent context.Context,
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(parent, integrationOperationTimeout)
	defer cancel()

	tag := integrationTag("signal")
	startStream, err := e.rpc.Start(ctx, integrationStartRequest(
		e.headers,
		e.user,
		&process.StartRequest{
			Process: &process.ProcessConfig{
				Cmd:  "/bin/sh",
				Args: []string{"-c", "exec sleep 60"},
			},
			Tag: &tag,
		},
	))
	require.NoError(t, err)
	defer startStream.Close()

	pid := receiveStartPID(t, startStream)
	e.trackPID(pid)
	_, err = e.rpc.SendSignal(ctx, integrationRequest(
		e.headers,
		&process.SendSignalRequest{
			Process: pidSelector(pid),
			Signal:  process.Signal_SIGNAL_SIGKILL,
		},
	))
	require.NoError(t, err)

	events := receiveRemainingStartEvents(t, startStream)
	assertHasEndEvent(t, events)
	require.NoError(t, e.waitForProcessToDisappear(ctx, pid))
	e.forgetPID(pid)
}

func (e *integrationEnvironment) testPTYProtocol(
	parent context.Context,
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(parent, integrationOperationTimeout)
	defer cancel()

	tag := integrationTag("pty")
	startStream, err := e.rpc.Start(ctx, integrationStartRequest(
		e.headers,
		e.user,
		&process.StartRequest{
			Process: &process.ProcessConfig{Cmd: "/bin/sh"},
			Pty: &process.PTY{Size: &process.PTY_Size{
				Cols: 80,
				Rows: 24,
			}},
			Tag: &tag,
		},
	))
	require.NoError(t, err)
	defer startStream.Close()

	pid := receiveStartPID(t, startStream)
	e.trackPID(pid)
	_, err = e.rpc.Update(ctx, integrationRequest(
		e.headers,
		&process.UpdateRequest{
			Process: pidSelector(pid),
			Pty: &process.PTY{Size: &process.PTY_Size{
				Cols: 100,
				Rows: 40,
			}},
		},
	))
	require.NoError(t, err)

	inputStream := e.rpc.StreamInput(ctx)
	copyHeaders(inputStream.RequestHeader(), e.headers)
	require.NoError(t, inputStream.Send(&process.StreamInputRequest{
		Event: &process.StreamInputRequest_Start{
			Start: &process.StreamInputRequest_StartEvent{
				Process: pidSelector(pid),
			},
		},
	}))
	require.NoError(t, inputStream.Send(&process.StreamInputRequest{
		Event: &process.StreamInputRequest_Keepalive{
			Keepalive: &process.StreamInputRequest_KeepAlive{},
		},
	}))
	require.NoError(t, inputStream.Send(&process.StreamInputRequest{
		Event: &process.StreamInputRequest_Data{
			Data: &process.StreamInputRequest_DataEvent{
				Input: &process.ProcessInput{
					Input: &process.ProcessInput_Pty{
						Pty: []byte("printf '__PTY_SIZE__'; stty size; printf '__PTY_DONE__\\n'; exit\n"),
					},
				},
			},
		},
	}))
	_, err = inputStream.CloseAndReceive()
	require.NoError(t, err)

	events := receiveRemainingStartEvents(t, startStream)
	e.forgetPID(pid)
	output := strings.ReplaceAll(collectEventOutput(events, true), "\r", "")
	assert.Contains(t, output, "__PTY_SIZE__40 100")
	assert.Contains(t, output, "__PTY_DONE__")
	assertHasEndEvent(t, events)
}

func (e *integrationEnvironment) testRun(
	parent context.Context,
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(parent, integrationOperationTimeout)
	defer cancel()

	result, err := e.runner.Run(ctx, Request{
		Cmd: "/bin/sh",
		Args: []string{
			"-c",
			"printf 'stdout:run-marker\\n'; " +
				"printf 'stderr:run-marker\\n' >&2; exit 7",
		},
		User:    e.user,
		Timeout: 20 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, "stdout:run-marker\n", result.Stdout)
	assert.Equal(t, "stderr:run-marker\n", result.Stderr)
	assert.Equal(t, 7, result.ExitCode)
	assert.False(t, result.TimedOut)
}

func (e *integrationEnvironment) testRunWithStdin(
	parent context.Context,
	t *testing.T,
) {
	if !e.supportsCloseStdin {
		t.Skip("sandbox envd does not implement Process.CloseStdin; E2B requires envd >= 0.5.2")
	}
	ctx, cancel := context.WithTimeout(parent, integrationOperationTimeout)
	defer cancel()

	result, err := e.runner.Run(ctx, Request{
		Cmd: "/bin/sh",
		Args: []string{
			"-c",
			"cat; printf '__RUN_STDIN_CLOSED__\\n'",
		},
		User:    e.user,
		Stdin:   "run-stdin-marker\n",
		Timeout: 20 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, "run-stdin-marker\n__RUN_STDIN_CLOSED__\n", result.Stdout)
	assert.Empty(t, result.Stderr)
	assert.Equal(t, 0, result.ExitCode)
	assert.False(t, result.TimedOut)
}

func (e *integrationEnvironment) testRunTimeout(
	parent context.Context,
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(parent, integrationOperationTimeout)
	defer cancel()

	marker := integrationTag("run-timeout")
	result, err := e.runner.Run(ctx, Request{
		Cmd:     "/bin/sh",
		Args:    []string{"-c", "exec sleep 60 # " + marker},
		User:    e.user,
		Timeout: time.Second,
	})
	require.NoError(t, err)
	assert.True(t, result.TimedOut)
	require.NoError(t, e.waitForProcessConfigToDisappear(ctx, marker))
}

func (e *integrationEnvironment) testProcessHandleLifecycle(
	parent context.Context,
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(parent, integrationOperationTimeout)
	defer cancel()

	tag := integrationTag("handle")
	proc, err := e.runner.Start(ctx, Request{
		Cmd:     "/bin/sh",
		Args:    []string{"-c", "exec sleep 60"},
		User:    e.user,
		Tag:     tag,
		Timeout: 20 * time.Second,
	})
	require.NoError(t, err)
	pid := proc.PID()
	require.NotZero(t, pid)
	e.trackPID(pid)

	infos, err := e.runner.List(ctx)
	require.NoError(t, err)
	assertProcessInfoListed(t, infos, pid, tag)

	proc.Disconnect()
	result, err := proc.Wait(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, pid, result.PID)

	reconnected, err := e.runner.Connect(ctx, pid)
	require.NoError(t, err)
	defer reconnected.Disconnect()
	killed, err := reconnected.Kill(ctx)
	require.NoError(t, err)
	assert.True(t, killed)
	require.NoError(t, e.waitForProcessToDisappear(ctx, pid))
	e.forgetPID(pid)
}

func assertProcessInfoListed(
	t *testing.T,
	infos []ProcessInfo,
	pid uint32,
	tag string,
) {
	t.Helper()
	for _, info := range infos {
		if info.PID == pid && info.Tag == tag {
			return
		}
	}
	require.Failf(
		t,
		"process not listed",
		"PID %d with tag %q was absent from Client.List",
		pid,
		tag,
	)
}

func (e *integrationEnvironment) trackPID(pid uint32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pids == nil {
		e.pids = make(map[uint32]struct{})
	}
	e.pids[pid] = struct{}{}
}

func (e *integrationEnvironment) forgetPID(pid uint32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.pids, pid)
}

func (e *integrationEnvironment) cleanupProcesses(t *testing.T) {
	t.Helper()
	e.mu.Lock()
	pids := make([]uint32, 0, len(e.pids))
	for pid := range e.pids {
		pids = append(pids, pid)
	}
	e.mu.Unlock()

	for _, pid := range pids {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := e.rpc.SendSignal(ctx, integrationRequest(
			e.headers,
			&process.SendSignalRequest{
				Process: pidSelector(pid),
				Signal:  process.Signal_SIGNAL_SIGKILL,
			},
		))
		cancel()
		if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("clean up envd process %d: %v", pid, err)
		}
	}
}

func (e *integrationEnvironment) waitForProcessToDisappear(
	ctx context.Context,
	pid uint32,
) error {
	return e.waitForProcess(ctx, func(info *process.ProcessInfo) bool {
		return info.Pid == pid
	})
}

func (e *integrationEnvironment) waitForProcessConfigToDisappear(
	ctx context.Context,
	marker string,
) error {
	return e.waitForProcess(ctx, func(info *process.ProcessInfo) bool {
		if info.Config == nil {
			return false
		}
		return strings.Contains(strings.Join(info.Config.Args, " "), marker)
	})
}

func (e *integrationEnvironment) waitForProcess(
	ctx context.Context,
	matches func(*process.ProcessInfo) bool,
) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		resp, err := e.rpc.List(
			ctx,
			integrationRequest(e.headers, &process.ListRequest{}),
		)
		if err != nil {
			return err
		}
		found := false
		for _, info := range resp.Msg.Processes {
			if matches(info) {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func integrationRequest[T any](
	headers http.Header,
	message *T,
) *connect.Request[T] {
	req := connect.NewRequest(message)
	copyHeaders(req.Header(), headers)
	return req
}

func integrationStartRequest(
	headers http.Header,
	user string,
	message *process.StartRequest,
) *connect.Request[process.StartRequest] {
	req := integrationRequest(headers, message)
	addProcessUserHeader(req.Header(), user)
	return req
}

func integrationSupportsCloseStdin(
	parent context.Context,
	t *testing.T,
	rpc processconnect.ProcessClient,
	headers http.Header,
) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, integrationOperationTimeout)
	defer cancel()
	_, err := rpc.CloseStdin(ctx, integrationRequest(
		headers,
		&process.CloseStdinRequest{
			Process: tagSelector(integrationTag("close-stdin-probe")),
		},
	))
	if err == nil || connect.CodeOf(err) == connect.CodeNotFound {
		return true
	}
	if connect.CodeOf(err) == connect.CodeUnimplemented {
		return false
	}
	require.NoError(t, err, "probe Process.CloseStdin support")
	return false
}

func copyHeaders(target, source http.Header) {
	for key, values := range source {
		for _, value := range values {
			target.Add(key, value)
		}
	}
}

func integrationTag(purpose string) string {
	return fmt.Sprintf("trpc-agent-go-it-%s-%d", purpose, time.Now().UnixNano())
}

func receiveStartPID(
	t *testing.T,
	stream *connect.ServerStreamForClient[process.StartResponse],
) uint32 {
	t.Helper()
	for stream.Receive() {
		event := stream.Msg().Event
		if event != nil && event.GetStart() != nil {
			pid := event.GetStart().Pid
			require.NotZero(t, pid)
			return pid
		}
	}
	require.NoError(t, stream.Err())
	require.FailNow(t, "envd Start stream ended without a StartEvent")
	return 0
}

func receiveRemainingStartEvents(
	t *testing.T,
	stream *connect.ServerStreamForClient[process.StartResponse],
) []*process.ProcessEvent {
	t.Helper()
	var events []*process.ProcessEvent
	for stream.Receive() {
		events = append(events, stream.Msg().Event)
	}
	require.NoError(t, stream.Err())
	return events
}

func receiveConnectEvents(
	t *testing.T,
	stream *connect.ServerStreamForClient[process.ConnectResponse],
) []*process.ProcessEvent {
	t.Helper()
	var events []*process.ProcessEvent
	for stream.Receive() {
		events = append(events, stream.Msg().Event)
	}
	require.NoError(t, stream.Err())
	return events
}

func assertProcessListed(
	t *testing.T,
	processes []*process.ProcessInfo,
	pid uint32,
	tag string,
) {
	t.Helper()
	for _, info := range processes {
		if info.Pid == pid && info.GetTag() == tag {
			return
		}
	}
	require.Failf(t, "process not listed", "PID %d with tag %q was absent", pid, tag)
}

func collectEventOutput(events []*process.ProcessEvent, pty bool) string {
	var output strings.Builder
	for _, event := range events {
		if event == nil || event.GetData() == nil {
			continue
		}
		if pty {
			output.Write(event.GetData().GetPty())
			continue
		}
		output.Write(event.GetData().GetStdout())
	}
	return output.String()
}

func assertHasEndEvent(t *testing.T, events []*process.ProcessEvent) {
	t.Helper()
	for _, event := range events {
		if event != nil && event.GetEnd() != nil {
			return
		}
	}
	require.FailNow(t, "envd process stream ended without an EndEvent")
}
