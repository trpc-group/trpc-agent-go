//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

const (
	mcpHelperEnv       = "ACP_TEST_MCP_HELPER"
	mcpHelperExitEnv   = "ACP_TEST_MCP_EXIT"
	mcpHelperPIDEnv    = "ACP_TEST_MCP_PID_FILE"
	mcpHelperValueEnv  = "ACP_TEST_MCP_VALUE"
	mcpHelperSecretEnv = "ACP_TEST_MCP_SECRET"
	mcpHelperToolEnv   = "ACP_TEST_MCP_TOOL_NAME"
)

func TestMain(m *testing.M) {
	if os.Getenv(mcpHelperEnv) == "1" {
		runMCPTestServer()
		return
	}
	os.Exit(m.Run())
}

func runMCPTestServer() {
	if os.Getenv(mcpHelperExitEnv) == "1" {
		os.Exit(2)
	}
	pidFile := os.Getenv(mcpHelperPIDEnv)
	if pidFile != "" {
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}

	server := mcp.NewStdioServer("acp-test-server", "1.0.0")
	server.RegisterTool(
		mcp.NewTool("echo", mcp.WithDescription("Echo tool arguments")),
		func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return nil, err
			}
			return mcp.NewTextResult(string(result)), nil
		},
	)
	server.RegisterTool(
		mcp.NewTool("env", mcp.WithDescription("Read an environment variable")),
		func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, _ := request.Params.Arguments["name"].(string)
			return mcp.NewTextResult(os.Getenv(name)), nil
		},
	)
	server.RegisterTool(
		mcp.NewTool("pid", mcp.WithDescription("Return the server process ID")),
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewTextResult(strconv.Itoa(os.Getpid())), nil
		},
	)
	server.RegisterTool(
		mcp.NewTool("block", mcp.WithDescription("Block until the server stops")),
		func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)
	if toolName := os.Getenv(mcpHelperToolEnv); toolName != "" {
		server.RegisterTool(
			mcp.NewTool(toolName, mcp.WithDescription("Test collision tool")),
			func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewTextResult(toolName), nil
			},
		)
	}

	if err := server.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func TestMCPPromptUsesStdioToolsAndEnvironment(t *testing.T) {
	skipRealMCPProcessTestUnderRace(t)
	run := &testRunner{}
	server, err := New(
		run,
		withSessionIDGenerator(func() string { return "mcp-session" }),
	)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	pidFile := filepath.Join(t.TempDir(), "server.pid")
	session, err := protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{
			Cwd: "/workspace",
			McpServers: []acpsdk.McpServer{
				testStdioMCPServer("Demo Server", "session-value", pidFile),
			},
		},
	)
	require.NoError(t, err)
	t.Cleanup(protocolAgent.shutdown)

	sessionState := protocolAgent.sessions[string(session.SessionId)]
	require.NotNil(t, sessionState)
	assert.ElementsMatch(t, []string{
		"demo_server_block",
		"demo_server_echo",
		"demo_server_env",
		"demo_server_pid",
	}, toolNames(sessionState.tools))

	var environmentValue string
	var echoArguments map[string]any
	var promptToolNames []string
	run.run = func(ctx context.Context) <-chan *event.Event {
		call := run.lastCall()
		promptToolNames = toolNames(call.options.AdditionalTools)
		environmentValue = textToolResult(t, callTool(
			t,
			ctx,
			call.options.AdditionalTools,
			"demo_server_env",
			`{"name":"ACP_TEST_MCP_VALUE"}`,
		))
		echoResult := textToolResult(t, callTool(
			t,
			ctx,
			call.options.AdditionalTools,
			"demo_server_echo",
			`{"text":"hello"}`,
		))
		require.NoError(t, json.Unmarshal([]byte(echoResult), &echoArguments))
		events := make(chan *event.Event)
		close(events)
		return events
	}

	response, err := protocolAgent.Prompt(
		context.Background(),
		acpsdk.PromptRequest{
			SessionId: session.SessionId,
			Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("use the tool")},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, acpsdk.StopReasonEndTurn, response.StopReason)
	assert.Equal(t, "session-value", environmentValue)
	assert.Equal(t, map[string]any{"text": "hello"}, echoArguments)
	assert.ElementsMatch(t, []string{
		"demo_server_block",
		"demo_server_echo",
		"demo_server_env",
		"demo_server_pid",
	}, promptToolNames)

	pid := readPIDFile(t, pidFile)
	_, err = protocolAgent.CloseSession(
		context.Background(),
		acpsdk.CloseSessionRequest{SessionId: session.SessionId},
	)
	require.NoError(t, err)
	requireProcessExited(t, pid)
}

func TestMCPMultipleServersUsePrefixedIsolatedTools(t *testing.T) {
	skipRealMCPProcessTestUnderRace(t)
	server, err := New(
		&testRunner{},
		withSessionIDGenerator(func() string { return "mcp-multiple" }),
	)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	alphaPIDFile := filepath.Join(t.TempDir(), "alpha.pid")
	betaPIDFile := filepath.Join(t.TempDir(), "beta.pid")
	session, err := protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{
			Cwd: "/workspace",
			McpServers: []acpsdk.McpServer{
				testStdioMCPServer("Alpha", "alpha-value", alphaPIDFile),
				testStdioMCPServer("Beta Server", "beta-value", betaPIDFile),
			},
		},
	)
	require.NoError(t, err)
	t.Cleanup(protocolAgent.shutdown)
	sessionState := protocolAgent.sessions[string(session.SessionId)]
	require.NotNil(t, sessionState)
	assert.ElementsMatch(t, []string{
		"alpha_block",
		"alpha_echo",
		"alpha_env",
		"alpha_pid",
		"beta_server_block",
		"beta_server_echo",
		"beta_server_env",
		"beta_server_pid",
	}, toolNames(sessionState.tools))

	alphaPID := textToolResult(t, callTool(t, context.Background(), sessionState.tools, "alpha_pid", `{}`))
	betaPID := textToolResult(t, callTool(t, context.Background(), sessionState.tools, "beta_server_pid", `{}`))
	assert.NotEqual(t, alphaPID, betaPID)
	assert.Equal(t, "alpha-value", textToolResult(t, callTool(
		t,
		context.Background(),
		sessionState.tools,
		"alpha_env",
		`{"name":"ACP_TEST_MCP_VALUE"}`,
	)))
	assert.Equal(t, "beta-value", textToolResult(t, callTool(
		t,
		context.Background(),
		sessionState.tools,
		"beta_server_env",
		`{"name":"ACP_TEST_MCP_VALUE"}`,
	)))

	alphaPIDNumber := readPIDFile(t, alphaPIDFile)
	betaPIDNumber := readPIDFile(t, betaPIDFile)
	_, err = protocolAgent.CloseSession(
		context.Background(),
		acpsdk.CloseSessionRequest{SessionId: session.SessionId},
	)
	require.NoError(t, err)
	requireProcessExited(t, alphaPIDNumber)
	requireProcessExited(t, betaPIDNumber)
}

func TestMCPToolNameCollisionClosesAllServers(t *testing.T) {
	skipRealMCPProcessTestUnderRace(t)
	server, err := New(
		&testRunner{},
		withSessionIDGenerator(func() string { return "mcp-tool-collision" }),
	)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	t.Cleanup(protocolAgent.shutdown)
	firstPIDFile := filepath.Join(t.TempDir(), "first.pid")
	secondPIDFile := filepath.Join(t.TempDir(), "second.pid")

	_, err = protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{
			Cwd: "/workspace",
			McpServers: []acpsdk.McpServer{
				testStdioMCPServerWithTool("foo_bar", "first", firstPIDFile, "baz"),
				testStdioMCPServerWithTool("foo", "second", secondPIDFile, "bar_baz"),
			},
		},
	)
	require.ErrorContains(t, err, "conflict after server prefixing")
	var requestError *acpsdk.RequestError
	require.ErrorAs(t, err, &requestError)
	assert.Equal(t, jsonRPCInvalidParamsCode, requestError.Code)
	assert.Empty(t, protocolAgent.sessions)
	requireProcessExited(t, readPIDFile(t, firstPIDFile))
	requireProcessExited(t, readPIDFile(t, secondPIDFile))
}

func TestMCPSessionsUseSeparateProcesses(t *testing.T) {
	skipRealMCPProcessTestUnderRace(t)
	nextSessionID := 0
	server, err := New(
		&testRunner{},
		withSessionIDGenerator(func() string {
			nextSessionID++
			return fmt.Sprintf("mcp-session-%d", nextSessionID)
		}),
	)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	firstPIDFile := filepath.Join(t.TempDir(), "first.pid")
	secondPIDFile := filepath.Join(t.TempDir(), "second.pid")
	first, err := protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{
			Cwd:        "/workspace",
			McpServers: []acpsdk.McpServer{testStdioMCPServer("Shared", "first", firstPIDFile)},
		},
	)
	require.NoError(t, err)
	t.Cleanup(protocolAgent.shutdown)
	second, err := protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{
			Cwd:        "/workspace",
			McpServers: []acpsdk.McpServer{testStdioMCPServer("Shared", "second", secondPIDFile)},
		},
	)
	require.NoError(t, err)

	firstState := protocolAgent.sessions[string(first.SessionId)]
	secondState := protocolAgent.sessions[string(second.SessionId)]
	require.NotNil(t, firstState)
	require.NotNil(t, secondState)
	firstPID := textToolResult(t, callTool(t, context.Background(), firstState.tools, "shared_pid", `{}`))
	secondPID := textToolResult(t, callTool(t, context.Background(), secondState.tools, "shared_pid", `{}`))
	assert.NotEqual(t, firstPID, secondPID)
	assert.Equal(t, "first", textToolResult(t, callTool(
		t,
		context.Background(),
		firstState.tools,
		"shared_env",
		`{"name":"ACP_TEST_MCP_VALUE"}`,
	)))
	assert.Equal(t, "second", textToolResult(t, callTool(
		t,
		context.Background(),
		secondState.tools,
		"shared_env",
		`{"name":"ACP_TEST_MCP_VALUE"}`,
	)))

	firstPIDNumber := readPIDFile(t, firstPIDFile)
	secondPIDNumber := readPIDFile(t, secondPIDFile)
	protocolAgent.shutdown()
	requireProcessExited(t, firstPIDNumber)
	requireProcessExited(t, secondPIDNumber)
}

func TestMCPInitializationFailureCleansUpAndDoesNotUseSessionID(t *testing.T) {
	skipRealMCPProcessTestUnderRace(t)
	server, err := New(
		&testRunner{},
		withSessionIDGenerator(func() string { return "mcp-after-failure" }),
	)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	pidFile := filepath.Join(t.TempDir(), "first.pid")
	failing := testStdioMCPServer("Failing", "unused", "")
	failing.Stdio.Env = append(failing.Stdio.Env, acpsdk.EnvVariable{
		Name:  mcpHelperExitEnv,
		Value: "1",
	}, acpsdk.EnvVariable{
		Name:  mcpHelperSecretEnv,
		Value: "top-secret",
	})

	_, err = protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{
			Cwd: "/workspace",
			McpServers: []acpsdk.McpServer{
				testStdioMCPServer("First", "first", pidFile),
				failing,
			},
		},
	)
	require.Error(t, err)
	var requestError *acpsdk.RequestError
	require.ErrorAs(t, err, &requestError)
	assert.Equal(t, jsonRPCInternalErrorCode, requestError.Code)
	assert.NotContains(t, err.Error(), "top-secret")
	requireProcessExited(t, readPIDFile(t, pidFile))

	session, err := protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{Cwd: "/workspace"},
	)
	require.NoError(t, err)
	assert.Equal(t, acpsdk.SessionId("mcp-after-failure"), session.SessionId)
	protocolAgent.shutdown()
}

func TestMCPNewSessionRejectedDuringShutdownSkipsToolsetInitialization(t *testing.T) {
	server, err := New(
		&testRunner{},
		withSessionIDGenerator(func() string { return "mcp-shutdown-session" }),
	)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	t.Cleanup(protocolAgent.shutdown)
	protocolAgent.shutdownWithTimeout(time.Second)

	pidFile := filepath.Join(t.TempDir(), "server.pid")
	_, err = protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{
			Cwd:        "/workspace",
			McpServers: []acpsdk.McpServer{testStdioMCPServer("Shutdown", "value", pidFile)},
		},
	)
	require.ErrorContains(t, err, "shutting down")
	assert.Empty(t, protocolAgent.sessions)
	_, statErr := os.Stat(pidFile)
	assert.True(t, os.IsNotExist(statErr))
	server.sessionMu.Lock()
	defer server.sessionMu.Unlock()
	assert.Empty(t, server.activeSessionIDs)
}

func TestMCPPromptCancellationCancelsToolCall(t *testing.T) {
	skipRealMCPProcessTestUnderRace(t)
	run := &testRunner{}
	server, err := New(
		run,
		withSessionIDGenerator(func() string { return "mcp-cancel" }),
	)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	session, err := protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{
			Cwd:        "/workspace",
			McpServers: []acpsdk.McpServer{testStdioMCPServer("Blocking", "value", "")},
		},
	)
	require.NoError(t, err)
	t.Cleanup(protocolAgent.shutdown)
	started := make(chan struct{})
	callDone := make(chan error, 1)
	run.run = func(ctx context.Context) <-chan *event.Event {
		events := make(chan *event.Event)
		go func() {
			defer close(events)
			call := run.lastCall()
			blocking := findTool(call.options.AdditionalTools, "blocking_block")
			if blocking == nil {
				callDone <- errors.New("blocking tool not found")
				return
			}
			close(started)
			_, callErr := blocking.(tool.CallableTool).Call(ctx, []byte(`{}`))
			callDone <- callErr
		}()
		return events
	}

	promptDone := make(chan struct {
		response acpsdk.PromptResponse
		err      error
	}, 1)
	go func() {
		response, promptErr := protocolAgent.Prompt(
			context.Background(),
			acpsdk.PromptRequest{
				SessionId: session.SessionId,
				Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("block")},
			},
		)
		promptDone <- struct {
			response acpsdk.PromptResponse
			err      error
		}{response: response, err: promptErr}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("MCP tool call did not start")
	}
	require.NoError(t, protocolAgent.Cancel(
		context.Background(),
		acpsdk.CancelNotification{SessionId: session.SessionId},
	))
	select {
	case callErr := <-callDone:
		assert.ErrorIs(t, callErr, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("MCP tool call was not canceled")
	}
	select {
	case got := <-promptDone:
		require.NoError(t, got.err)
		assert.Equal(t, acpsdk.StopReasonCancelled, got.response.StopReason)
	case <-time.After(5 * time.Second):
		t.Fatal("prompt did not return after cancellation")
	}
	protocolAgent.shutdown()
}

func testStdioMCPServer(name, value, pidFile string) acpsdk.McpServer {
	env := []acpsdk.EnvVariable{
		{Name: mcpHelperEnv, Value: "1"},
		{Name: mcpHelperValueEnv, Value: value},
	}
	if pidFile != "" {
		env = append(env, acpsdk.EnvVariable{Name: mcpHelperPIDEnv, Value: pidFile})
	}
	return acpsdk.McpServer{
		Stdio: &acpsdk.McpServerStdio{
			Name:    name,
			Command: os.Args[0],
			Env:     env,
		},
	}
}

func testStdioMCPServerWithTool(
	name string,
	value string,
	pidFile string,
	toolName string,
) acpsdk.McpServer {
	server := testStdioMCPServer(name, value, pidFile)
	server.Stdio.Env = append(server.Stdio.Env, acpsdk.EnvVariable{
		Name:  mcpHelperToolEnv,
		Value: toolName,
	})
	return server
}

func findTool(tools []tool.Tool, name string) tool.Tool {
	for _, candidate := range tools {
		if candidate != nil && candidate.Declaration().Name == name {
			return candidate
		}
	}
	return nil
}

func callTool(
	t *testing.T,
	ctx context.Context,
	tools []tool.Tool,
	name string,
	arguments string,
) any {
	t.Helper()
	candidate := findTool(tools, name)
	require.NotNil(t, candidate, "tool %q not found", name)
	callable, ok := candidate.(tool.CallableTool)
	require.True(t, ok)
	result, err := callable.Call(ctx, []byte(arguments))
	require.NoError(t, err)
	return result
}

func textToolResult(t *testing.T, result any) string {
	t.Helper()
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	var contents []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	require.NoError(t, json.Unmarshal(raw, &contents))
	require.NotEmpty(t, contents)
	return contents[0].Text
}

func toolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, candidate := range tools {
		names = append(names, candidate.Declaration().Name)
	}
	return names
}

func readPIDFile(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	pid, err := strconv.Atoi(string(data))
	require.NoError(t, err)
	require.Positive(t, pid)
	return pid
}

func requireProcessExited(t *testing.T, pid int) {
	t.Helper()
	require.Eventually(t, func() bool {
		process, err := os.FindProcess(pid)
		if err != nil {
			return true
		}
		return process.Signal(syscall.Signal(0)) != nil
	}, 5*time.Second, 20*time.Millisecond, "expected process %d to exit", pid)
}

func skipRealMCPProcessTestUnderRace(t *testing.T) {
	t.Helper()
	if raceDetectorEnabled {
		t.Skip("skipping real stdio process test under race due to trpc-mcp-go v0.0.10 Cmd.Wait race")
	}
}
