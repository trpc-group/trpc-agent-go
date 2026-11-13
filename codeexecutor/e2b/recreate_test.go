//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2b

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	ci "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/codeinterpreter"
	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec/processconnect"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/workspaceexec"
)

func TestWithAutoRecreateRejectsExternalSandbox(t *testing.T) {
	t.Setenv("E2B_API_KEY", "test-key")
	orders := [][]Option{
		{WithAutoRecreate(true), WithSandboxID("external-id")},
		{WithSandboxID("external-id"), WithAutoRecreate(true)},
	}
	for _, opts := range orders {
		_, err := NewWithContext(context.Background(), append([]Option{
			WithAPIKey("test-key"),
		}, opts...)...)
		require.Error(t, err)
		assert.ErrorContains(t, err, "WithSandboxID")
	}
}

func TestLossCandidateIgnoresUncertainErrors(t *testing.T) {
	timeoutNet := &url.Error{Op: "POST", URL: "https://sandbox", Err: timeoutNetError{}}
	require.False(t, lossCandidate(timeoutNet))
	require.False(t, lossCandidate(&ci.TimeoutError{Message: "client deadline"}))
	require.False(t, lossCandidate(&ci.TimeoutError{
		StatusCode: http.StatusInternalServerError, Message: "not a gateway",
	}))
	require.True(t, lossCandidate(&ci.TimeoutError{
		StatusCode: http.StatusBadGateway,
		Message:    `{"code":330502,"message":"Cannot connect to the sandbox service"}`,
	}))
	require.True(t, lossCandidate(&ci.TimeoutError{
		StatusCode: http.StatusGatewayTimeout, Message: "gateway timeout",
	}))
	require.False(t, lossCandidate(context.DeadlineExceeded))
	require.False(t, lossCandidate(&ci.SandboxError{StatusCode: 500, Message: "unavailable"}))
	require.False(t, lossCandidate(io.ErrUnexpectedEOF))
	require.True(t, lossCandidate(connect.NewError(
		connect.CodeUnimplemented, errors.New("HTTP status 404 Not Found"),
	)))
	require.False(t, lossCandidate(connect.NewError(
		connect.CodeInternal, errors.New("HTTP status 500 Internal Server Error"),
	)))
	require.False(t, terminalSandboxState("running"))
	require.False(t, terminalSandboxState("paused"))
	require.False(t, terminalSandboxState(""))
	require.True(t, terminalSandboxState(" Killed "))
	require.True(t, lossCandidate(&ci.NotFoundError{Message: "sandbox"}))
	require.True(t, lossCandidate(connect.NewError(
		connect.CodeNotFound, errors.New("process not found"),
	)))
}

func TestAutoRecreateDefaultLeavesDeadSandboxInPlace(t *testing.T) {
	srv := newLifeServer(t)
	executor := srv.executor(t, WithAutoRecreate(false))
	first := srv.sandboxIDs()[0]
	before := executor.Sandbox()
	srv.setInfo(first, http.StatusNotFound, "", "")
	srv.setExecute(func(string, string) (int, string) {
		return http.StatusNotFound, "sandbox not found"
	})

	for i := 0; i < 2; i++ {
		_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Language: "python", Code: "print(1)"}},
		})
		require.Error(t, err)
		assert.False(t, errors.Is(err, codeexecutor.ErrWorkspaceStale))
	}
	counts := srv.counts()
	assert.Equal(t, []string{first}, counts.ids)
	assert.Empty(t, counts.infoCalls)
	assert.Len(t, counts.executeCodes, 2)
	assert.Equal(t, first, executor.SandboxID())
	assert.Same(t, before, executor.Sandbox())
}

func TestAutoRecreateConfirmedDeath(t *testing.T) {
	srv := newLifeServer(t)
	executor := srv.executor(t)
	first := srv.sandboxIDs()[0]
	snapshot := executor.Sandbox()
	srv.setInfo(first, http.StatusNotFound, "", "2000-01-01T00:00:00Z")
	srv.setExecute(func(host, code string) (int, string) {
		if hostHasSandbox(host, first) {
			return http.StatusNotFound, "sandbox not found"
		}
		return http.StatusOK, ndjsonLines(stdoutMsg("recovered\n"))
	})

	_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{Language: "python", Code: "print('once')"}},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	require.False(t, codeexecutor.IsWorkspaceRetrySafe(err))
	var notFound *ci.NotFoundError
	require.ErrorAs(t, err, &notFound)
	require.Len(t, srv.sandboxIDs(), 2)
	second := srv.sandboxIDs()[1]
	assert.Contains(t, srv.counts().kills, first)
	assert.NotContains(t, srv.counts().kills, second)

	result, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{Language: "python", Code: "print('next')"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "recovered\n", result.Output)
	assert.Equal(t, second, executor.SandboxID())
	assert.Equal(t, first, snapshot.SandboxID())
	assert.NotSame(t, snapshot, executor.Sandbox())
	codes := srv.counts().executeCodes
	assert.Equal(t, 1, stringsCount(codes, "print('once')"))
	assert.Equal(t, 1, stringsCount(codes, "print('next')"))
}

func TestAutoRecreateTerminalStateAndCreationOptions(t *testing.T) {
	srv := newLifeServer(t)
	envVars := map[string]string{"MODE": "recreate"}
	metadata := map[string]string{"owner": "executor"}
	headers := map[string]string{"X-Tenant": "team-a"}
	executor := srv.executor(t,
		WithTemplate("tmpl-recreate"),
		WithSandboxTimeout(2*time.Minute),
		WithEnvVars(envVars),
		WithMetadata(metadata),
		WithHeaders(headers),
	)
	first := srv.sandboxIDs()[0]
	// Caller-owned maps may be reused after construction. Replacements must
	// retain the executor's original configuration.
	envVars["MODE"] = "changed"
	metadata["owner"] = "caller"
	headers["X-Tenant"] = "team-b"
	srv.setInfo(first, http.StatusOK, "killed", "2099-01-01T00:00:00Z")
	srv.setExecute(func(host, _ string) (int, string) {
		if hostHasSandbox(host, first) {
			return http.StatusNotFound, "route missing"
		}
		return http.StatusOK, ndjsonLines(stdoutMsg("ok\n"))
	})

	_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{Code: "first"}},
	})
	require.Error(t, err)
	_, err = executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{Code: "second"}},
	})
	require.NoError(t, err)

	counts := srv.counts()
	require.Len(t, counts.bodies, 2)
	for _, body := range counts.bodies {
		assert.Equal(t, "tmpl-recreate", body.TemplateID)
		assert.Equal(t, 120, body.Timeout)
		assert.Equal(t, map[string]string{"MODE": "recreate"}, body.EnvVars)
		assert.Equal(t, map[string]string{"owner": "executor"}, body.Metadata)
	}
	require.Len(t, counts.headers, 2)
	for _, header := range counts.headers {
		assert.Equal(t, "test-key", header.Get("X-API-Key"))
		assert.Equal(t, "team-a", header.Get("X-Tenant"))
	}
	assert.NotEqual(t, first, executor.SandboxID())
}

func TestAutoRecreateKeepsResolvedConnectionSettings(t *testing.T) {
	t.Setenv("E2B_API_KEY", "initial-key")
	t.Setenv("E2B_DOMAIN", "initial.test")
	t.Setenv("E2B_API_URL", "")
	t.Setenv("E2B_ACCESS_TOKEN", "")
	t.Setenv("E2B_DEBUG", "false")
	srv := newLifeServer(t)
	ctx := context.Background()
	executor, err := NewWithContext(ctx,
		WithHTTPClient(srv.client()),
		WithAutoRecreate(true),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = executor.Close() })
	first := executor.SandboxID()

	t.Setenv("E2B_API_KEY", "changed-key")
	t.Setenv("E2B_DOMAIN", "changed.test")
	t.Setenv("E2B_API_URL", "http://changed.test")
	t.Setenv("E2B_ACCESS_TOKEN", "changed-token")
	t.Setenv("E2B_DEBUG", "true")
	srv.setInfo(first, http.StatusNotFound, "", "")
	srv.setExecute(func(host, _ string) (int, string) {
		if hostHasSandbox(host, first) {
			return http.StatusNotFound, "sandbox not found"
		}
		return http.StatusOK, ndjsonLines(stdoutMsg("recovered\n"))
	})

	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{Code: "print('ok')"}},
	}
	_, err = executor.ExecuteCode(ctx, input)
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceRetryUnsafe)
	result, err := executor.ExecuteCode(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, "recovered\n", result.Output)
	assert.NotEqual(t, first, executor.SandboxID())

	counts := srv.counts()
	require.Len(t, counts.headers, 2)
	for _, header := range counts.headers {
		assert.Equal(t, "https", header.Get("X-Original-Scheme"))
		assert.Equal(t, "api.initial.test", header.Get("X-Original-Host"))
		assert.Equal(t, "initial-key", header.Get("X-API-Key"))
		assert.Empty(t, header.Get("X-Access-Token"))
	}
}

func TestAutoRecreateDoesNotTreatOrdinaryFailuresAsDeletion(t *testing.T) {
	t.Run("management API unavailable", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t)
		first := executor.SandboxID()
		srv.setInfo(first, http.StatusServiceUnavailable, "", "")
		srv.setExecute(func(string, string) (int, string) {
			return http.StatusNotFound, "sandbox endpoint unavailable"
		})

		_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "print('hello')"}},
		})
		var notFound *ci.NotFoundError
		require.ErrorAs(t, err, &notFound)
		require.NotErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
		assert.Equal(t, first, executor.SandboxID())
		counts := srv.counts()
		assert.Equal(t, []string{first}, counts.infoCalls)
		assert.Equal(t, 1, counts.attempts)
	})

	t.Run("file 404 and past end time", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t)
		first := srv.sandboxIDs()[0]
		srv.setInfo(first, http.StatusOK, "running", "2000-01-01T00:00:00Z")
		srv.setExecute(func(string, string) (int, string) {
			return http.StatusNotFound, "no such file"
		})
		_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "read missing"}},
		})
		require.Error(t, err)
		var notFound *ci.NotFoundError
		require.ErrorAs(t, err, &notFound)
		assert.False(t, errors.Is(err, codeexecutor.ErrWorkspaceStale))
		assert.Equal(t, []string{first}, srv.sandboxIDs())
		assert.NotEmpty(t, srv.counts().infoCalls)
		assert.Equal(t, first, executor.SandboxID())
	})

	t.Run("process 404", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t)
		first := srv.sandboxIDs()[0]
		srv.setInfo(first, http.StatusOK, "running", "")
		srv.setProcess(func(string, string) error {
			return connect.NewError(connect.CodeNotFound, errors.New("process not found"))
		})
		ws := srv.mustWorkspace(t, executor)
		_, err := executor.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
			Cmd: "missing-process", Timeout: time.Second,
		})
		require.Error(t, err)
		assert.False(t, errors.Is(err, codeexecutor.ErrWorkspaceStale))
		assert.Equal(t, []string{first}, srv.sandboxIDs())
		assert.Len(t, srv.counts().processLines, 1)
	})

	t.Run("transport timeout", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t)
		first := srv.sandboxIDs()[0]
		srv.setInfo(first, http.StatusNotFound, "", "")
		srv.timeoutExecute.Store(true)
		_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "sleep"}},
		})
		require.Error(t, err)
		assert.False(t, errors.Is(err, codeexecutor.ErrWorkspaceStale))
		assert.Equal(t, []string{first}, srv.sandboxIDs())
		assert.Empty(t, srv.counts().infoCalls)
		assert.Empty(t, srv.counts().executeCodes)
	})

	t.Run("http 500", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t)
		first := srv.sandboxIDs()[0]
		srv.setInfo(first, http.StatusOK, "killed", "")
		srv.setExecute(func(string, string) (int, string) {
			return http.StatusInternalServerError, "backend failed"
		})
		_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "boom"}},
		})
		require.Error(t, err)
		var sandboxErr *ci.SandboxError
		require.ErrorAs(t, err, &sandboxErr)
		assert.Equal(t, []string{first}, srv.sandboxIDs())
		assert.Empty(t, srv.counts().infoCalls)
	})

	t.Run("paused", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t)
		first := srv.sandboxIDs()[0]
		srv.setInfo(first, http.StatusOK, "paused", "")
		srv.setExecute(func(string, string) (int, string) {
			return http.StatusNotFound, "sandbox not found"
		})
		_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "paused"}},
		})
		require.Error(t, err)
		assert.Equal(t, []string{first}, srv.sandboxIDs())
		assert.NotEmpty(t, srv.counts().infoCalls)
	})
}

func TestAutoRecreateDoesNotReplayExecutedBlocks(t *testing.T) {
	srv := newLifeServer(t)
	executor := srv.executor(t)
	first := srv.sandboxIDs()[0]
	srv.setInfo(first, http.StatusNotFound, "", "")
	srv.setExecute(func(host, code string) (int, string) {
		if strings.Contains(code, "BLOCK_A") {
			return http.StatusOK, ndjsonLines(stdoutMsg("A\n"))
		}
		return http.StatusNotFound, "sandbox deleted"
	})

	result, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Code: "BLOCK_A"},
			{Code: "BLOCK_B"},
		},
	})
	require.Error(t, err)
	assert.Empty(t, result.Output)
	assert.Equal(t, 1, stringsCount(srv.counts().executeCodes, "BLOCK_A"))
	assert.Equal(t, 1, stringsCount(srv.counts().executeCodes, "BLOCK_B"))
	assert.Len(t, srv.sandboxIDs(), 2)
}

const gatewaySandboxBody = `{"code":330502,"message":"Cannot connect to the sandbox service. Ensure the service is running and listening on the requested port"}`

func TestAutoRecreateRunCodeGatewayTimeout(t *testing.T) {
	t.Run("502 confirms missing sandbox without replay", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t)
		first := srv.sandboxIDs()[0]
		srv.setInfo(first, http.StatusNotFound, "", "")
		srv.setExecute(func(host, code string) (int, string) {
			if hostHasSandbox(host, first) || strings.Contains(code, "print('once')") {
				return http.StatusBadGateway, gatewaySandboxBody
			}
			return http.StatusOK, ndjsonLines(stdoutMsg("recovered\n"))
		})

		_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "print('once')"}},
		})
		require.Error(t, err)
		require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
		require.False(t, codeexecutor.IsWorkspaceRetrySafe(err))
		var timeoutErr *ci.TimeoutError
		require.ErrorAs(t, err, &timeoutErr)
		assert.Equal(t, http.StatusBadGateway, timeoutErr.StatusCode)
		assert.Contains(t, timeoutErr.Message, "330502")
		require.Len(t, srv.sandboxIDs(), 2)
		assert.Equal(t, 1, stringsCount(srv.counts().executeCodes, "print('once')"))
		assert.NotEmpty(t, srv.counts().infoCalls)

		result, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "print('next')"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "recovered\n", result.Output)
		assert.Equal(t, 1, stringsCount(srv.counts().executeCodes, "print('next')"))
		assert.NotEqual(t, first, executor.SandboxID())
	})

	t.Run("502 on a running sandbox does not recreate", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t)
		first := srv.sandboxIDs()[0]
		srv.setInfo(first, http.StatusOK, "running", "")
		srv.setExecute(func(string, string) (int, string) {
			return http.StatusBadGateway, gatewaySandboxBody
		})
		_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "gateway"}},
		})
		require.Error(t, err)
		var timeoutErr *ci.TimeoutError
		require.ErrorAs(t, err, &timeoutErr)
		assert.Equal(t, http.StatusBadGateway, timeoutErr.StatusCode)
		assert.False(t, errors.Is(err, codeexecutor.ErrWorkspaceStale))
		assert.Equal(t, []string{first}, srv.sandboxIDs())
		assert.Equal(t, []string{first}, srv.counts().infoCalls)
		assert.Equal(t, first, executor.SandboxID())
	})

	t.Run("disabled option does not confirm a 502", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t, WithAutoRecreate(false))
		first := srv.sandboxIDs()[0]
		srv.setInfo(first, http.StatusNotFound, "", "")
		srv.setExecute(func(string, string) (int, string) {
			return http.StatusBadGateway, gatewaySandboxBody
		})
		_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "gateway"}},
		})
		require.Error(t, err)
		var timeoutErr *ci.TimeoutError
		require.ErrorAs(t, err, &timeoutErr)
		assert.False(t, errors.Is(err, codeexecutor.ErrWorkspaceStale))
		assert.Equal(t, []string{first}, srv.sandboxIDs())
		assert.Empty(t, srv.counts().infoCalls)
	})
}

func TestAutoRecreateEnvdHTTP404(t *testing.T) {
	t.Run("raw 404 confirms missing sandbox without replay", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t)
		first := srv.sandboxIDs()[0]
		ws := srv.mustWorkspace(t, executor)
		srv.setInfo(first, http.StatusNotFound, "", "")
		srv.setProcessStatus(http.StatusNotFound)

		_, err := executor.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
			Cmd: "die", Timeout: time.Second,
		})
		require.Error(t, err)
		require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
		require.False(t, codeexecutor.IsWorkspaceRetrySafe(err))
		assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
		assert.Contains(t, err.Error(), "HTTP status 404 Not Found")
		require.Len(t, srv.sandboxIDs(), 2)
		assert.NotEmpty(t, srv.counts().infoCalls)
		lines := len(srv.counts().processLines)
		assert.Positive(t, lines)

		_, err = executor.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
			Cmd: "die", Timeout: time.Second,
		})
		require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
		require.True(t, codeexecutor.IsWorkspaceRetrySafe(err))
		assert.Len(t, srv.counts().processLines, lines)

		srv.setProcessStatus(0)
		replacement := srv.mustWorkspace(t, executor)
		result, err := executor.RunProgram(context.Background(), replacement, codeexecutor.RunProgramSpec{
			Cmd: "true", Timeout: time.Second,
		})
		require.NoError(t, err)
		assert.Equal(t, "ok\n", result.Stdout)
		assert.Contains(t, replacement.Path, "/inst/2/")
	})

	t.Run("raw 404 on a running sandbox does not recreate", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t)
		first := srv.sandboxIDs()[0]
		ws := srv.mustWorkspace(t, executor)
		srv.setInfo(first, http.StatusOK, "running", "")
		srv.setProcessStatus(http.StatusNotFound)
		_, err := executor.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
			Cmd: "still-there", Timeout: time.Second,
		})
		require.Error(t, err)
		assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
		assert.Contains(t, err.Error(), "HTTP status 404 Not Found")
		assert.False(t, errors.Is(err, codeexecutor.ErrWorkspaceStale))
		assert.Equal(t, []string{first}, srv.sandboxIDs())
		assert.Equal(t, []string{first}, srv.counts().infoCalls)
	})

	t.Run("unimplemented method on a running sandbox does not recreate", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t)
		first := srv.sandboxIDs()[0]
		ws := srv.mustWorkspace(t, executor)
		srv.setInfo(first, http.StatusOK, "running", "")
		srv.setProcess(func(string, string) error {
			return connect.NewError(
				connect.CodeUnimplemented,
				errors.New("process.Process.Start is not implemented"),
			)
		})
		_, err := executor.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
			Cmd: "unsupported", Timeout: time.Second,
		})
		require.Error(t, err)
		assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
		assert.False(t, errors.Is(err, codeexecutor.ErrWorkspaceStale))
		assert.Equal(t, []string{first}, srv.sandboxIDs())
		assert.NotEmpty(t, srv.counts().infoCalls)
	})
}

func TestAutoRecreateRunProgramRecovery(t *testing.T) {
	srv := newLifeServer(t)
	executor := srv.executor(t)
	first := srv.sandboxIDs()[0]
	ws := srv.mustWorkspace(t, executor)
	srv.setInfo(first, http.StatusNotFound, "", "")
	srv.setProcess(func(host, line string) error {
		if hostHasSandbox(host, first) && strings.Contains(line, " die") {
			return connect.NewError(connect.CodeUnavailable, errors.New("sandbox unavailable"))
		}
		return nil
	})

	_, err := executor.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd: "die", Timeout: time.Second,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceRetryUnsafe)
	assert.ErrorContains(t, err, "sandbox was recreated")
	assert.ErrorContains(t, err, "Check the required files and runtime environment before continuing")
	require.Len(t, srv.sandboxIDs(), 2)

	_, err = executor.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd: "die", Timeout: time.Second,
	})
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	require.True(t, codeexecutor.IsWorkspaceRetrySafe(err))
	assert.NotContains(t, err.Error(), "sandbox was recreated")
	assert.Equal(t, 1, stringsCount(srv.counts().processLines, " die"))

	replacement := srv.mustWorkspace(t, executor)
	result, err := executor.RunProgram(context.Background(), replacement, codeexecutor.RunProgramSpec{
		Cmd: "true", Timeout: time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, "ok\n", result.Stdout)
	assert.Contains(t, replacement.Path, "/inst/2/")
}

func TestAutoRecreateReportsReplacementInWorkspaceToolResult(t *testing.T) {
	srv := newLifeServer(t)
	executor := srv.executor(t)
	first := executor.SandboxID()
	srv.setInfo(first, http.StatusNotFound, "", "")
	srv.setProcess(func(host, _ string) error {
		if hostHasSandbox(host, first) {
			return connect.NewError(connect.CodeUnavailable, errors.New("sandbox unavailable"))
		}
		return nil
	})

	execTool := workspaceexec.NewExecTool(executor, workspaceexec.WithAllowedCommands("true"))
	toolCallID := "call-workspace"
	response := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID: toolCallID,
					Function: model.FunctionDefinitionParam{
						Name:      "workspace_exec",
						Arguments: []byte(`{"command":"true"}`),
					},
				}},
			},
		}},
	}
	invocation := &agent.Invocation{
		InvocationID: "workspace-recovery",
		AgentName:    "test-agent",
	}
	request := &model.Request{Tools: map[string]tool.Tool{"workspace_exec": execTool}}
	events := make(chan *event.Event, 4)
	proc := processor.NewFunctionCallResponseProcessor(false, nil,
		processor.WithToolCallRetryPolicy(&tool.RetryPolicy{
			MaxAttempts: 2,
			RetryOn:     func(context.Context, *tool.RetryInfo) (bool, error) { return true, nil },
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	proc.ProcessResponse(ctx, invocation, request, response, events)
	close(events)

	var messages []model.Message
	for ev := range events {
		if ev.Response == nil {
			continue
		}
		for _, choice := range ev.Response.Choices {
			if choice.Message.Role == model.RoleTool && choice.Message.ToolID == toolCallID {
				messages = append(messages, choice.Message)
			}
		}
	}
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Content, "sandbox unavailable")
	assert.Contains(t, messages[0].Content, "sandbox was recreated")
	assert.Contains(t, messages[0].Content, "Check the required files and runtime environment before continuing")
	assert.NotEqual(t, first, executor.SandboxID())
	assert.Len(t, srv.counts().processLines, 1, "the original command must not be replayed")
}

func TestAutoRecreateWhenCreatingWorkspace(t *testing.T) {
	srv := newLifeServer(t)
	executor := srv.executor(t)
	first := executor.SandboxID()
	srv.setInfo(first, http.StatusNotFound, "", "")
	srv.setExecute(func(host, _ string) (int, string) {
		if hostHasSandbox(host, first) {
			return http.StatusNotFound, "sandbox deleted"
		}
		return http.StatusOK, ndjsonLines(stdoutMsg("ok\n"))
	})

	ctx := context.Background()
	_, err := executor.CreateWorkspace(ctx, "session", codeexecutor.WorkspacePolicy{})
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	require.False(t, codeexecutor.IsWorkspaceRetrySafe(err))
	require.NotEqual(t, first, executor.SandboxID())
	assert.Len(t, srv.counts().executeCodes, 1, "failed setup must not be replayed")

	workspace, err := executor.CreateWorkspace(ctx, "session", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	assert.Equal(t, "session", workspace.ID)
	counts := srv.counts()
	require.Len(t, counts.executeCodes, 2)
	assert.Contains(t, counts.executeCodes[1], workspace.Path)
	assert.Equal(t, 2, counts.attempts)
}

func TestAutoRecreateStageInputsPreservesUnsafeMetadataError(t *testing.T) {
	srv := newLifeServer(t)
	executor := srv.executor(t)
	ws := srv.mustWorkspace(t, executor)
	first := executor.SandboxID()
	before := len(srv.counts().executeCodes)
	srv.setInfo(first, http.StatusNotFound, "", "")
	srv.setExecute(func(host, _ string) (int, string) {
		if hostHasSandbox(host, first) {
			return http.StatusNotFound, "sandbox deleted"
		}
		return http.StatusOK, ""
	})

	err := executor.StageInputs(context.Background(), ws, []codeexecutor.InputSpec{{
		From: "host://" + t.TempDir(), To: "work/inputs",
	}})
	require.NotEqual(t, first, executor.SandboxID())
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	assert.ErrorIs(t, err, codeexecutor.ErrWorkspaceRetryUnsafe)
	assert.False(t, codeexecutor.IsWorkspaceRetrySafe(err))
	var notFound *ci.NotFoundError
	assert.ErrorAs(t, err, &notFound)
	counts := srv.counts()
	require.Len(t, counts.ids, 2)
	require.Len(t, counts.executeCodes, before+1, "failed metadata read must stop input staging")
	assert.Contains(t, counts.executeCodes[before], "__E2B_SIZE__")
}

func TestAutoRecreateStageInputsWithMissingMetadata(t *testing.T) {
	srv := newLifeServer(t)
	executor := srv.executor(t)
	ws := srv.mustWorkspace(t, executor)
	first := executor.SandboxID()
	srv.setExecute(func(_ string, code string) (int, string) {
		if strings.Contains(code, "__E2B_SIZE__") {
			return http.StatusOK, ndjsonLines(errorMsg("ShellError", "metadata file not found", ""))
		}
		return http.StatusOK, ""
	})

	err := executor.StageInputs(context.Background(), ws, []codeexecutor.InputSpec{{
		From: "host://" + t.TempDir(), To: "work/inputs",
	}})
	require.NoError(t, err)
	assert.Equal(t, first, executor.SandboxID())
	counts := srv.counts()
	assert.Len(t, counts.ids, 1)
	assert.Empty(t, counts.infoCalls)
	assert.Equal(t, 1, stringsCount(counts.executeCodes, "__E2B_SIZE__"))
}

func TestAutoRecreateWorkspaceRebuildAndStaleHandles(t *testing.T) {
	srv := newLifeServer(t)
	executor := srv.executor(t, WithWorkspacePersistence(WorkspacePersistencePerSession))
	first := srv.sandboxIDs()[0]
	var (
		hookMu    sync.Mutex
		hookPaths []string
	)
	hook := func(_ context.Context, env codeexecutor.WorkspaceInitEnv) error {
		hookMu.Lock()
		hookPaths = append(hookPaths, env.Workspace.Path)
		hookMu.Unlock()
		_, err := env.Runner.RunProgram(context.Background(), env.Workspace, codeexecutor.RunProgramSpec{
			Cmd: "true", Timeout: time.Second,
		})
		return err
	}
	wrapped, err := codeexecutor.NewWorkspaceInitExecutor(executor, hook)
	require.NoError(t, err)
	provider, ok := wrapped.(codeexecutor.EngineProvider)
	require.True(t, ok)
	engine := provider.Engine()
	registry := codeexecutor.NewWorkspaceRegistry()
	ctx := context.Background()
	firstHandle, err := registry.AcquireHandle(ctx, engine.Manager(), "session-a")
	require.NoError(t, err)
	require.NotEmpty(t, firstHandle.InstanceID)
	manager := engine.Manager()
	providerID, ok := manager.(codeexecutor.WorkspaceInstanceProvider)
	require.True(t, ok)
	stableID, err := providerID.InstanceID(ctx)
	require.NoError(t, err)
	require.Equal(t, firstHandle.InstanceID, stableID)

	srv.setInfo(first, http.StatusNotFound, "", "")
	srv.setProcess(func(host, line string) error {
		if hostHasSandbox(host, first) && strings.Contains(line, " die") {
			return connect.NewError(connect.CodeUnavailable, errors.New("sandbox unavailable"))
		}
		return nil
	})
	_, err = executor.RunProgram(ctx, firstHandle.Workspace, codeexecutor.RunProgramSpec{
		Cmd: "die", Timeout: time.Second,
	})
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceRetryUnsafe)
	registry.Invalidate(firstHandle)

	secondHandle, err := registry.AcquireHandle(ctx, engine.Manager(), "session-a")
	require.NoError(t, err)
	require.NotEqual(t, firstHandle.InstanceID, secondHandle.InstanceID)
	replacedID, err := providerID.InstanceID(ctx)
	require.NoError(t, err)
	require.Equal(t, secondHandle.InstanceID, replacedID)
	require.NotEqual(t, firstHandle.Workspace.Path, secondHandle.Workspace.Path)
	hookMu.Lock()
	require.Equal(t, []string{firstHandle.Workspace.Path, secondHandle.Workspace.Path}, hookPaths)
	hookMu.Unlock()
	var rebuilt bool
	for _, code := range srv.counts().executeCodes {
		if strings.Contains(code, secondHandle.Workspace.Path) &&
			strings.Contains(code, codeexecutor.DirSkills) &&
			strings.Contains(code, codeexecutor.DirWork) &&
			strings.Contains(code, codeexecutor.DirRuns) &&
			strings.Contains(code, codeexecutor.DirOut) {
			rebuilt = true
		}
	}
	assert.True(t, rebuilt, "replacement workspace was not initialized")

	before := srv.counts()
	err = executor.Cleanup(ctx, firstHandle.Workspace)
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	require.True(t, codeexecutor.IsWorkspaceRetrySafe(err))
	err = executor.PutFiles(ctx, firstHandle.Workspace, []codeexecutor.PutFile{{
		Path: "work/a.txt", Content: []byte("x"),
	}})
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	_, err = executor.Collect(ctx, firstHandle.Workspace, []string{"**/*"})
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	_, err = executor.CollectOutputs(ctx, firstHandle.Workspace, codeexecutor.OutputSpec{
		Globs: []string{"out/**"},
	})
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	_, err = executor.RunProgram(ctx, firstHandle.Workspace, codeexecutor.RunProgramSpec{
		Cmd: "true", Timeout: time.Second,
	})
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	after := srv.counts()
	assert.Equal(t, len(before.executeCodes), len(after.executeCodes))
	assert.Equal(t, len(before.processLines), len(after.processLines))

	beforeInline := len(srv.counts().processLines)
	inline, err := executor.ExecuteInline(ctx, "inline", []codeexecutor.CodeBlock{{
		Language: "python", Code: "print('inline')",
	}}, time.Second)
	require.NoError(t, err)
	assert.Equal(t, "ok\n", inline.Stdout)
	lines := srv.counts().processLines
	require.Greater(t, len(lines), beforeInline)
	joined := strings.Join(lines[beforeInline:], "\n")
	assert.NotContains(t, joined, firstHandle.Workspace.Path)
	assert.Contains(t, joined, "/inst/2/")
}

func TestAutoRecreateConcurrentCallersShareOneAllocation(t *testing.T) {
	srv := newLifeServer(t)
	executor := srv.executor(t, WithRequestTimeout(-1))
	first := srv.sandboxIDs()[0]
	srv.setInfo(first, http.StatusNotFound, "", "")
	entered := make(chan struct{})
	release := make(chan struct{})
	const callers = 8
	srv.setExecute(func(host, code string) (int, string) {
		if hostHasSandbox(host, first) && strings.Contains(code, "WAVE") {
			entered <- struct{}{}
			<-release
			return http.StatusNotFound, "sandbox deleted"
		}
		return http.StatusOK, ndjsonLines(stdoutMsg("done\n"))
	})

	var readers sync.WaitGroup
	stop := make(chan struct{})
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = executor.SandboxID()
				_ = executor.Sandbox()
			}
		}
	}()

	errCh := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() {
			_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{{Code: "WAVE"}},
			})
			errCh <- err
		}()
	}
	for i := 0; i < callers; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for concurrent executions")
		}
	}
	close(release)
	for i := 0; i < callers; i++ {
		err := <-errCh
		require.Error(t, err)
		assert.False(t, codeexecutor.IsWorkspaceRetrySafe(err))
	}
	close(stop)
	readers.Wait()
	assert.Len(t, srv.sandboxIDs(), 2)
	assert.Equal(t, callers, stringsCount(srv.counts().executeCodes, "WAVE"))

	result, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{Code: "AFTER"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "done\n", result.Output)
	assert.Equal(t, 1, stringsCount(srv.counts().executeCodes, "AFTER"))
}

func TestAutoRecreateCancellationAndFailedRecovery(t *testing.T) {
	t.Run("cancel waits without publishing", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t, WithRequestTimeout(-1))
		first := srv.sandboxIDs()[0]
		srv.setInfo(first, http.StatusNotFound, "", "")
		entered := make(chan struct{})
		release := make(chan struct{})
		srv.setHold(entered, release)
		srv.setExecute(func(_ string, code string) (int, string) {
			if strings.Contains(code, "later") {
				return http.StatusOK, ndjsonLines(stdoutMsg("ok\n"))
			}
			return http.StatusNotFound, "sandbox deleted"
		})
		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			_, err := executor.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{{Code: "cancel-me"}},
			})
			errCh <- err
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("recovery create did not start")
		}
		cancel()
		err := <-errCh
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, []string{first}, srv.sandboxIDs())
		assert.Equal(t, first, executor.SandboxID())
		srv.clearHold()

		result, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "later"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "ok\n", result.Output)
		assert.Len(t, srv.sandboxIDs(), 2)
	})

	t.Run("failed create is retried by a later call", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t)
		first := srv.sandboxIDs()[0]
		srv.setFailCreates(1)
		srv.setInfo(first, http.StatusNotFound, "", "")
		srv.setExecute(func(host, _ string) (int, string) {
			if hostHasSandbox(host, first) {
				return http.StatusNotFound, "sandbox deleted"
			}
			return http.StatusOK, ndjsonLines(stdoutMsg("after-retry\n"))
		})
		_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "fails"}},
		})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "sandbox was recreated")
		assert.Equal(t, []string{first}, srv.sandboxIDs())
		assert.Equal(t, 2, srv.counts().attempts)

		result, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "retries"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "after-retry\n", result.Output)
		assert.Equal(t, 3, srv.counts().attempts)
		assert.Len(t, srv.sandboxIDs(), 2)
	})

	t.Run("canceled waiter does not start", func(t *testing.T) {
		srv := newLifeServer(t)
		executor := srv.executor(t, WithRequestTimeout(-1))
		first := srv.sandboxIDs()[0]
		srv.setInfo(first, http.StatusNotFound, "", "")
		entered := make(chan struct{})
		release := make(chan struct{})
		srv.setHold(entered, release)
		srv.setExecute(func(_ string, code string) (int, string) {
			if strings.Contains(code, "later") {
				return http.StatusOK, ndjsonLines(stdoutMsg("ok\n"))
			}
			return http.StatusNotFound, "sandbox deleted"
		})
		leaderErr := make(chan error, 1)
		go func() {
			_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{{Code: "leader"}},
			})
			leaderErr <- err
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("recovery create did not start")
		}

		waiterCtx, cancel := context.WithCancel(context.Background())
		waiterErr := make(chan error, 1)
		go func() {
			_, err := executor.ExecuteCode(waiterCtx, codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{{Code: "waiter"}},
			})
			waiterErr <- err
		}()
		deadline := time.Now().Add(5 * time.Second)
		for recreateWaiters.Load() == 0 {
			if time.Now().After(deadline) {
				t.Fatal("waiter did not block on recreation")
			}
			time.Sleep(time.Millisecond)
		}
		cancel()
		require.ErrorIs(t, <-waiterErr, context.Canceled)
		assert.NotContains(t, srv.counts().executeCodes, "waiter")

		close(release)
		require.Error(t, <-leaderErr)
		assert.Len(t, srv.sandboxIDs(), 2)
		result, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "later"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "ok\n", result.Output)
	})
}

func TestAutoRecreatePauseDiscardsUnpublishedSandbox(t *testing.T) {
	srv := newLifeServer(t)
	executor := srv.executor(t, WithRequestTimeout(-1))
	first := srv.sandboxIDs()[0]
	srv.setInfo(first, http.StatusNotFound, "", "")
	entered := make(chan struct{})
	release := make(chan struct{})
	srv.setHold(entered, release)
	srv.setExecute(func(host, code string) (int, string) {
		if hostHasSandbox(host, first) || strings.Contains(code, "later") {
			return http.StatusNotFound, "sandbox deleted"
		}
		return http.StatusOK, ndjsonLines(stdoutMsg("should-not-run\n"))
	})
	errCh := make(chan error, 1)
	go func() {
		_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "pause-race"}},
		})
		errCh <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery create did not start")
	}
	require.NoError(t, executor.Pause(context.Background()))
	close(release)
	err := <-errCh
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	require.False(t, codeexecutor.IsWorkspaceRetrySafe(err))
	assert.NotContains(t, err.Error(), "sandbox was recreated")
	assert.Equal(t, first, executor.SandboxID())
	ids := srv.sandboxIDs()
	require.Len(t, ids, 2)
	assert.Contains(t, srv.counts().kills, ids[1])
	assert.NotContains(t, srv.counts().kills, first)

	_, err = executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{Code: "later"}},
	})
	require.Error(t, err)
	assert.False(t, errors.Is(err, codeexecutor.ErrWorkspaceStale))
	assert.Len(t, srv.sandboxIDs(), 2)
}

func TestAutoRecreateInstanceIDUniqueAcrossExecutors(t *testing.T) {
	srv := newLifeServer(t)
	firstExec := srv.executor(t)
	secondExec := srv.executor(t)
	ctx := context.Background()
	firstProvider := firstExec.Engine().Manager().(codeexecutor.WorkspaceInstanceProvider)
	secondProvider := secondExec.Engine().Manager().(codeexecutor.WorkspaceInstanceProvider)
	firstID, err := firstProvider.InstanceID(ctx)
	require.NoError(t, err)
	firstAgain, err := firstProvider.InstanceID(ctx)
	require.NoError(t, err)
	secondID, err := secondProvider.InstanceID(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, firstID)
	require.Equal(t, firstID, firstAgain)
	require.NotEqual(t, firstID, secondID)
	require.NotEqual(t, firstExec.SandboxID(), secondExec.SandboxID())

	registry := codeexecutor.NewWorkspaceRegistry()
	firstHandle, err := registry.AcquireHandle(ctx, firstExec.Engine().Manager(), "shared-session")
	require.NoError(t, err)
	before := len(srv.counts().executeCodes)
	secondHandle, err := registry.AcquireHandle(ctx, secondExec.Engine().Manager(), "shared-session")
	require.NoError(t, err)
	require.Greater(t, len(srv.counts().executeCodes), before)
	require.NotEqual(t, firstHandle.Workspace.Path, secondHandle.Workspace.Path)
	require.Equal(t, firstID, firstHandle.InstanceID)
	require.Equal(t, secondID, secondHandle.InstanceID)
}

func TestAutoRecreateConcurrentCloseSharesKillResult(t *testing.T) {
	srv := newLifeServer(t)
	base := srv.client().Transport
	entered := make(chan struct{})
	release := make(chan struct{})
	var deletes atomic.Int32
	client := &http.Client{Transport: hostnameTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete && deletes.Add(1) == 1 {
			close(entered)
			<-release
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("temporary kill failure")),
				Request:    req,
			}, nil
		}
		return base.RoundTrip(req)
	})}
	executor := srv.executor(t, WithHTTPClient(client))
	sandboxID := executor.SandboxID()
	require.NotEmpty(t, sandboxID)

	firstErr := make(chan error, 1)
	secondErr := make(chan error, 1)
	go func() { firstErr <- executor.Close() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not reach kill")
	}
	go func() { secondErr <- executor.Close() }()
	select {
	case err := <-secondErr:
		t.Fatalf("second Close finished before the shared kill returned: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	require.Error(t, <-firstErr)
	require.Error(t, <-secondErr)
	assert.Equal(t, sandboxID, executor.SandboxID())
	assert.Equal(t, int32(1), deletes.Load())

	require.NoError(t, executor.Close())
	assert.Empty(t, executor.SandboxID())
	assert.Equal(t, int32(2), deletes.Load())
	require.NoError(t, executor.Close())
	assert.Equal(t, int32(2), deletes.Load())
}

func TestAutoRecreateCloseDropsUnpublishedSandbox(t *testing.T) {
	srv := newLifeServer(t)
	executor := srv.executor(t, WithRequestTimeout(-1))
	first := srv.sandboxIDs()[0]
	srv.setInfo(first, http.StatusNotFound, "", "")
	entered := make(chan struct{})
	release := make(chan struct{})
	srv.setHold(entered, release)
	srv.setExecute(func(string, string) (int, string) {
		return http.StatusNotFound, "sandbox deleted"
	})
	errCh := make(chan error, 1)
	go func() {
		_, err := executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{Code: "close-race"}},
		})
		errCh <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery create did not start")
	}
	require.NoError(t, executor.Close())
	close(release)
	err := <-errCh
	require.Error(t, err)
	require.ErrorIs(t, err, errExecutorClosed)
	assert.NotContains(t, err.Error(), "sandbox was recreated")
	kills := srv.counts().kills
	require.Contains(t, kills, first)
	require.Len(t, srv.sandboxIDs(), 2)
	assert.Contains(t, kills, srv.sandboxIDs()[1])
	assert.Empty(t, executor.SandboxID())
	assert.Nil(t, executor.Sandbox())

	_, err = executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{Code: "after-close"}},
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "sandbox not initialized")
	assert.Len(t, srv.sandboxIDs(), 2)
}

type createPayload struct {
	TemplateID string            `json:"templateID"`
	Timeout    int               `json:"timeout"`
	Metadata   map[string]string `json:"metadata"`
	EnvVars    map[string]string `json:"envVars"`
}

type lifeCounts struct {
	ids          []string
	attempts     int
	infoCalls    []string
	executeCodes []string
	processLines []string
	kills        []string
	bodies       []createPayload
	headers      []http.Header
}

type sandboxScript struct {
	status int
	state  string
	endAt  string
}

type lifeServer struct {
	t               *testing.T
	server          *httptest.Server
	process         *workspaceProcessHandler
	rpc             http.Handler
	timeoutExecute  atomicBool
	mu              sync.Mutex
	nextID          int
	createAttempts  int
	failCreates     int
	ids             []string
	bodies          []createPayload
	headers         []http.Header
	infoCalls       []string
	executeCodes    []string
	processLines    []string
	kills           []string
	scripts         map[string]*sandboxScript
	execute         func(host, code string) (int, string)
	onProcess       func(host, line string) error
	processStatus   int
	holdRecovery    bool
	recoveryEntered chan struct{}
	releaseRecovery chan struct{}
	recoveryOnce    sync.Once
}

func newLifeServer(t *testing.T) *lifeServer {
	t.Helper()
	srv := &lifeServer{
		t:       t,
		scripts: map[string]*sandboxScript{},
		process: &workspaceProcessHandler{inputs: map[uint32]*workspaceProcessInput{}},
	}
	_, srv.rpc = processconnect.NewProcessHandler(srv.process)
	srv.process.start = srv.startProcess
	srv.server = httptest.NewServer(http.HandlerFunc(srv.handle))
	t.Cleanup(srv.close)
	return srv
}

func (s *lifeServer) close() {
	if s != nil && s.server != nil {
		s.server.Close()
	}
}

func (s *lifeServer) client() *http.Client {
	parsed, err := url.Parse(s.server.URL)
	require.NoError(s.t, err)
	return &http.Client{Transport: &lifeTransport{
		host: parsed.Host, scheme: parsed.Scheme, timeout: &s.timeoutExecute,
	}}
}

func (s *lifeServer) executor(t *testing.T, opts ...Option) *CodeExecutor {
	t.Helper()
	t.Setenv("E2B_API_KEY", "test-key")
	all := append([]Option{
		WithAPIKey("test-key"),
		WithDomain("e2b.test"),
		WithHTTPClient(s.client()),
		WithRequestTimeout(-1),
		WithAutoRecreate(true),
	}, opts...)
	executor, err := NewWithContext(context.Background(), all...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = executor.Close() })
	return executor
}

func (s *lifeServer) mustWorkspace(t *testing.T, executor *CodeExecutor) codeexecutor.Workspace {
	t.Helper()
	ws, err := executor.CreateWorkspace(context.Background(), "ws", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	return ws
}

func (s *lifeServer) setInfo(id string, status int, state, endAt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripts[id] = &sandboxScript{status: status, state: state, endAt: endAt}
}

func (s *lifeServer) setExecute(fn func(host, code string) (int, string)) {
	s.mu.Lock()
	s.execute = fn
	s.mu.Unlock()
}

func (s *lifeServer) setProcess(fn func(host, line string) error) {
	s.mu.Lock()
	s.onProcess = fn
	s.mu.Unlock()
}

func (s *lifeServer) setProcessStatus(status int) {
	s.mu.Lock()
	s.processStatus = status
	s.mu.Unlock()
}

func (s *lifeServer) setFailCreates(n int) {
	s.mu.Lock()
	s.failCreates = n
	s.mu.Unlock()
}

func (s *lifeServer) setHold(entered, release chan struct{}) {
	s.mu.Lock()
	s.holdRecovery = true
	s.recoveryEntered = entered
	s.releaseRecovery = release
	s.mu.Unlock()
}

func (s *lifeServer) clearHold() {
	s.mu.Lock()
	s.holdRecovery = false
	s.mu.Unlock()
}

func (s *lifeServer) sandboxIDs() []string {
	return s.counts().ids
}

func (s *lifeServer) counts() lifeCounts {
	s.mu.Lock()
	defer s.mu.Unlock()
	return lifeCounts{
		ids:          append([]string(nil), s.ids...),
		attempts:     s.createAttempts,
		infoCalls:    append([]string(nil), s.infoCalls...),
		executeCodes: append([]string(nil), s.executeCodes...),
		processLines: append([]string(nil), s.processLines...),
		kills:        append([]string(nil), s.kills...),
		bodies:       append([]createPayload(nil), s.bodies...),
		headers:      append([]http.Header(nil), s.headers...),
	}
}

func (s *lifeServer) handle(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/process.Process/") {
		s.mu.Lock()
		status := s.processStatus
		s.mu.Unlock()
		if status != 0 {
			s.mu.Lock()
			s.processLines = append(s.processLines, r.Header.Get("X-Original-Host")+" raw-http")
			s.mu.Unlock()
			w.WriteHeader(status)
			return
		}
		s.rpc.ServeHTTP(w, r)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
		s.handleCreate(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pause"):
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
		s.mu.Lock()
		s.kills = append(s.kills, sandboxIDFromPath(r.URL.Path))
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
		s.handleInfo(w, r)
	case r.URL.Path == "/execute":
		s.handleExecute(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *lifeServer) handleCreate(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body createPayload
	_ = json.Unmarshal(raw, &body)
	s.mu.Lock()
	s.createAttempts++
	attempt := s.createAttempts
	if s.failCreates > 0 {
		s.failCreates--
		s.mu.Unlock()
		http.Error(w, "create failed", http.StatusInternalServerError)
		return
	}
	s.nextID++
	id := fmt.Sprintf("sbx-%d", s.nextID)
	hold := s.holdRecovery && attempt > 1
	entered := s.recoveryEntered
	release := s.releaseRecovery
	s.mu.Unlock()

	if hold {
		if entered != nil {
			s.recoveryOnce.Do(func() { close(entered) })
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		if r.Context().Err() != nil {
			return
		}
	}

	s.mu.Lock()
	s.ids = append(s.ids, id)
	s.bodies = append(s.bodies, body)
	s.headers = append(s.headers, r.Header.Clone())
	s.scripts[id] = &sandboxScript{status: http.StatusOK, state: "running"}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"sandboxID":%q,"clientID":"client","templateID":"code-interpreter-v1","envdVersion":"0.5.2","state":"running"}`, id)
}

func (s *lifeServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	id := sandboxIDFromPath(r.URL.Path)
	s.mu.Lock()
	s.infoCalls = append(s.infoCalls, id)
	script := s.scripts[id]
	s.mu.Unlock()
	if script == nil || script.status == http.StatusNotFound {
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	}
	if script.status != 0 && script.status != http.StatusOK {
		w.WriteHeader(script.status)
		return
	}
	state := script.state
	if state == "" {
		state = "running"
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"sandboxID":%q,"state":%q,"endAt":%q,"envdVersion":"0.5.2"}`, id, state, script.endAt)
}

func (s *lifeServer) handleExecute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)
	host := r.Header.Get("X-Original-Host")
	s.mu.Lock()
	s.executeCodes = append(s.executeCodes, body.Code)
	fn := s.execute
	s.mu.Unlock()
	status, payload := http.StatusOK, ""
	if fn != nil {
		status, payload = fn(host, body.Code)
	}
	if status >= 400 {
		http.Error(w, payload, status)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	_, _ = w.Write([]byte(payload))
}

func (s *lifeServer) startProcess(
	_ context.Context,
	req *connect.Request[process.StartRequest],
	stream *connect.ServerStream[process.StartResponse],
) error {
	host := req.Header().Get("X-Original-Host")
	line := ""
	if req.Msg != nil && req.Msg.Process != nil {
		line = req.Msg.Process.Cmd + " " + strings.Join(req.Msg.Process.Args, " ")
	}
	s.mu.Lock()
	s.processLines = append(s.processLines, host+" "+line)
	fn := s.onProcess
	s.mu.Unlock()
	if fn != nil {
		if err := fn(host, line); err != nil {
			return err
		}
	}
	if err := sendWorkspaceStart(stream, 1); err != nil {
		return err
	}
	return sendWorkspaceResult(stream, "ok\n", "", 0)
}

func sandboxIDFromPath(path string) string {
	rest := strings.TrimPrefix(path, "/sandboxes/")
	if index := strings.IndexByte(rest, '/'); index >= 0 {
		rest = rest[:index]
	}
	return rest
}

func hostHasSandbox(host, id string) bool {
	return strings.Contains(host, id+"-") || strings.Contains(host, id+".")
}

func stringsCount(values []string, needle string) int {
	count := 0
	for _, value := range values {
		if strings.Contains(value, needle) {
			count++
		}
	}
	return count
}

type lifeTransport struct {
	host    string
	scheme  string
	timeout *atomicBool
}

func (t *lifeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == "/execute" && t.timeout != nil && t.timeout.Load() {
		return nil, &url.Error{Op: req.Method, URL: req.URL.String(), Err: timeoutNetError{}}
	}
	cloned := req.Clone(req.Context())
	cloned.Header.Set("X-Original-Host", req.URL.Host)
	cloned.Header.Set("X-Original-Scheme", req.URL.Scheme)
	cloned.URL.Scheme = t.scheme
	cloned.URL.Host = t.host
	cloned.Host = t.host
	return http.DefaultTransport.RoundTrip(cloned)
}

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "i/o timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

// atomicBool avoids importing sync/atomic for one flag while staying race-safe.
type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (b *atomicBool) Store(v bool) {
	b.mu.Lock()
	b.v = v
	b.mu.Unlock()
}

func (b *atomicBool) Load() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.v
}

// Keep net imported for the timeout error's documented interface neighbors.
var _ net.Error = timeoutNetError{}
