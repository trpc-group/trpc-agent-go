//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestExecutePythonGuest(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	add := testTool{declaration: &tool.Declaration{Name: "add", InputSchema: &tool.Schema{Type: "object", Required: []string{"a", "b"}, Properties: map[string]*tool.Schema{"a": {Type: "integer"}, "b": {Type: "integer"}}}}, call: func(raw []byte) (any, error) {
		var in struct{ A, B int }
		require.NoError(t, json.Unmarshal(raw, &in))
		return in.A + in.B, nil
	}}
	g, err := NewGateway(add)
	require.NoError(t, err)
	result, err := Execute(context.Background(), LocalRunner{}, g, "value = await call_tool('add', a=20, b=22)\nprint('computed', value)\nreturn {'answer': value}")
	require.NoError(t, err)
	require.JSONEq(t, `{"answer":42}`, string(result.Value))
	require.Contains(t, result.Stdout, "computed 42")
}

func TestExecuteStdioReapsGuestAfterProtocolError(t *testing.T) {
	process := &fakeStdioProcess{stdout: io.NopCloser(strings.NewReader("not-json\n"))}
	_, err := executeStdio(
		context.Background(),
		fakeStdioRunner{process: process},
		Request{Code: "return 1"},
		fakeToolCallHandler{},
	)
	require.Error(t, err)
	require.Equal(t, 1, process.kills)
	require.Equal(t, 1, process.waits)
	require.Equal(t, 1, process.stdinCloses)
}

func TestExecuteStdioValidatesRequiredInputs(t *testing.T) {
	process := &fakeStdioProcess{stdout: io.NopCloser(strings.NewReader(""))}
	tests := []struct {
		name    string
		runner  stdioRunner
		req     Request
		handler ToolCallHandler
		want    string
	}{
		{name: "runner", req: Request{Code: "return 1"}, handler: fakeToolCallHandler{}, want: "runner is required"},
		{name: "handler", runner: fakeStdioRunner{process: process}, req: Request{Code: "return 1"}, want: "tool call handler is required"},
		{name: "code", runner: fakeStdioRunner{process: process}, handler: fakeToolCallHandler{}, want: "code is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executeStdio(context.Background(), tt.runner, tt.req, tt.handler)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestExecuteStdioBridgesToolCalls(t *testing.T) {
	process := &fakeStdioProcess{stdout: io.NopCloser(strings.NewReader(`{"type":"tool_call","id":"call-1","name":"add","args":{"a":1,"b":2}}
{"type":"complete","args":{"answer":3},"code":"done\n"}
`))}
	var got ToolCall
	result, err := executeStdio(
		context.Background(),
		fakeStdioRunner{process: process},
		Request{Code: "return 3"},
		toolCallHandlerFunc(func(_ context.Context, call ToolCall) (json.RawMessage, error) {
			got = call
			return json.RawMessage(`3`), nil
		}),
	)
	require.NoError(t, err)
	require.Equal(t, "call-1", got.ID)
	require.Equal(t, "add", got.Name)
	require.JSONEq(t, `{"a":1,"b":2}`, string(got.Args))
	require.JSONEq(t, `{"answer":3}`, string(result.Value))
	require.Equal(t, "done\n", result.Stdout)
	require.Equal(t, 0, process.kills)
	require.Equal(t, 1, process.waits)
	require.Equal(t, 1, process.stdinCloses)
	require.Contains(t, process.stdin.String(), `"type":"run"`)
	require.Contains(t, process.stdin.String(), `"type":"tool_result"`)
	require.Contains(t, process.stdin.String(), `"result":3`)
}

func TestExecuteStdioReturnsHandlerErrorToGuest(t *testing.T) {
	process := &fakeStdioProcess{stdout: io.NopCloser(strings.NewReader(`{"type":"tool_call","id":"call-1","name":"add","args":{}}
{"type":"complete","args":null}
`))}
	result, err := executeStdio(
		context.Background(),
		fakeStdioRunner{process: process},
		Request{Code: "return None"},
		toolCallHandlerFunc(func(context.Context, ToolCall) (json.RawMessage, error) {
			return nil, errors.New("denied")
		}),
	)
	require.NoError(t, err)
	require.JSONEq(t, "null", string(result.Value))
	require.Contains(t, process.stdin.String(), `"error":"denied"`)
}

func TestExecuteStdioReportsRunnerGuestAndContextErrors(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		runner stdioRunner
		want   string
	}{
		{name: "start", ctx: context.Background(), runner: fakeStdioRunner{err: errors.New("start failed")}, want: "start guest: start failed"},
		{name: "guest", ctx: context.Background(), runner: fakeStdioRunner{process: &fakeStdioProcess{stdout: io.NopCloser(strings.NewReader(`{"type":"complete","name":"RuntimeError: boom"}
`))}}, want: "RuntimeError: boom"},
		{name: "wait", ctx: context.Background(), runner: fakeStdioRunner{process: &fakeStdioProcess{stdout: io.NopCloser(strings.NewReader(`{"type":"complete","args":1}
`)), waitErr: errors.New("wait failed")}}, want: "wait for guest: wait failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executeStdio(tt.ctx, tt.runner, Request{Code: "return 1"}, fakeToolCallHandler{})
			require.ErrorContains(t, err, tt.want)
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := executeStdio(
		ctx,
		fakeStdioRunner{process: &fakeStdioProcess{stdout: io.NopCloser(strings.NewReader(""))}},
		Request{Code: "return 1"},
		fakeToolCallHandler{},
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestExecuteStdioReportsProtocolEdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		stdout io.ReadCloser
		want   string
	}{
		{name: "unknown message", stdout: io.NopCloser(strings.NewReader(`{"type":"surprise"}
`)), want: `unknown guest message "surprise"`},
		{name: "scanner error", stdout: errReadCloser{err: errors.New("read failed")}, want: "read failed"},
		{name: "no completion", stdout: io.NopCloser(strings.NewReader("")), want: "guest exited without a completion message"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executeStdio(
				context.Background(),
				fakeStdioRunner{process: &fakeStdioProcess{stdout: tt.stdout}},
				Request{Code: "return 1"},
				fakeToolCallHandler{},
			)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestExecuteStdioReportsWriteErrors(t *testing.T) {
	t.Run("run request", func(t *testing.T) {
		_, err := executeStdio(
			context.Background(),
			fakeStdioRunner{process: &fakeStdioProcess{
				stdinWriter: errWriteCloser{err: errors.New("write failed")},
				stdout:      io.NopCloser(strings.NewReader("")),
			}},
			Request{Code: "return 1"},
			fakeToolCallHandler{},
		)
		require.ErrorContains(t, err, "write failed")
	})

	t.Run("tool result", func(t *testing.T) {
		process := &fakeStdioProcess{
			stdout: io.NopCloser(strings.NewReader(`{"type":"tool_call","id":"call-1","name":"add","args":{}}
`)),
		}
		process.stdinWriter = &failAfterWritesWriteCloser{
			Writer: &process.stdin,
			After:  1,
			Err:    errors.New("write failed"),
			OnClose: func() {
				process.stdinCloses++
			},
		}
		_, err := executeStdio(
			context.Background(),
			fakeStdioRunner{process: process},
			Request{Code: "return 1"},
			fakeToolCallHandler{},
		)
		require.ErrorContains(t, err, "write failed")
	})
}

func TestLocalRunnerReturnsStartError(t *testing.T) {
	_, err := LocalRunner{Python: "definitely-not-a-python-executable"}.start(context.Background(), "return 1")
	require.Error(t, err)
}

func TestLocalProcessKill(t *testing.T) {
	notStarted := &localProcess{cmd: exec.Command("sleep", "60")}
	require.NoError(t, notStarted.Kill())

	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep unavailable")
	}
	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	process := &localProcess{cmd: cmd}
	require.NoError(t, process.Kill())
	_ = cmd.Wait()
}

type fakeStdioRunner struct {
	process *fakeStdioProcess
	err     error
}

func (r fakeStdioRunner) start(context.Context, string) (stdioProcess, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.process, nil
}

type fakeStdioProcess struct {
	stdin       bytes.Buffer
	stdinWriter io.WriteCloser
	stdout      io.ReadCloser
	kills       int
	waits       int
	stdinCloses int
	waitErr     error
}

func (p *fakeStdioProcess) Stdin() io.WriteCloser {
	if p.stdinWriter != nil {
		return p.stdinWriter
	}
	return fakeWriteCloser{Writer: &p.stdin, onClose: func() { p.stdinCloses++ }}
}
func (p *fakeStdioProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *fakeStdioProcess) Wait() error {
	p.waits++
	return p.waitErr
}
func (p *fakeStdioProcess) Kill() error {
	p.kills++
	return nil
}

type fakeWriteCloser struct {
	io.Writer
	onClose func()
}

func (w fakeWriteCloser) Close() error {
	w.onClose()
	return nil
}

type errReadCloser struct {
	err error
}

func (r errReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (r errReadCloser) Close() error             { return nil }

type errWriteCloser struct {
	err error
}

func (w errWriteCloser) Write([]byte) (int, error) { return 0, w.err }
func (w errWriteCloser) Close() error              { return nil }

type failAfterWritesWriteCloser struct {
	io.Writer
	After   int
	Writes  int
	Err     error
	OnClose func()
}

func (w *failAfterWritesWriteCloser) Write(p []byte) (int, error) {
	w.Writes++
	if w.Writes > w.After {
		return 0, w.Err
	}
	return w.Writer.Write(p)
}

func (w *failAfterWritesWriteCloser) Close() error {
	if w.OnClose != nil {
		w.OnClose()
	}
	return nil
}

type fakeToolCallHandler struct{}

func (fakeToolCallHandler) HandleToolCall(context.Context, ToolCall) (json.RawMessage, error) {
	return nil, nil
}

type toolCallHandlerFunc func(context.Context, ToolCall) (json.RawMessage, error)

func (f toolCallHandlerFunc) HandleToolCall(ctx context.Context, call ToolCall) (json.RawMessage, error) {
	return f(ctx, call)
}
