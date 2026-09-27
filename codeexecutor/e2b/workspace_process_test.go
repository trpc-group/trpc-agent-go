//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package e2b

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec/processconnect"
)

type workspaceProcessInput struct {
	data []byte
	done chan struct{}
}

type workspaceProcessHandler struct {
	processconnect.UnimplementedProcessHandler
	mu       sync.Mutex
	requests []*process.StartRequest
	headers  []http.Header
	inputs   map[uint32]*workspaceProcessInput
	stdout   string
	stderr   string
	exitCode int32
	start    func(context.Context, *connect.Request[process.StartRequest], *connect.ServerStream[process.StartResponse]) error
	signal   func(*process.SendSignalRequest)
}

func (h *workspaceProcessHandler) Start(ctx context.Context, req *connect.Request[process.StartRequest], stream *connect.ServerStream[process.StartResponse]) error {
	h.mu.Lock()
	h.requests = append(h.requests, req.Msg)
	h.headers = append(h.headers, req.Header().Clone())
	pid := uint32(len(h.requests))
	input := &workspaceProcessInput{done: make(chan struct{})}
	h.inputs[pid] = input
	h.mu.Unlock()
	if h.start != nil {
		return h.start(ctx, req, stream)
	}
	if err := sendWorkspaceStart(stream, pid); err != nil {
		return err
	}
	if req.Msg.GetStdin() {
		select {
		case <-input.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return sendWorkspaceResult(stream, h.stdout, h.stderr, h.exitCode)
}

func (h *workspaceProcessHandler) SendInput(_ context.Context, req *connect.Request[process.SendInputRequest]) (*connect.Response[process.SendInputResponse], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	input := h.inputs[req.Msg.Process.GetPid()]
	if input == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown process"))
	}
	input.data = append(input.data, req.Msg.Input.GetStdin()...)
	return connect.NewResponse(&process.SendInputResponse{}), nil
}

func (h *workspaceProcessHandler) CloseStdin(_ context.Context, req *connect.Request[process.CloseStdinRequest]) (*connect.Response[process.CloseStdinResponse], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	input := h.inputs[req.Msg.Process.GetPid()]
	if input == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown process"))
	}
	select {
	case <-input.done:
	default:
		close(input.done)
	}
	return connect.NewResponse(&process.CloseStdinResponse{}), nil
}

func (h *workspaceProcessHandler) SendSignal(_ context.Context, req *connect.Request[process.SendSignalRequest]) (*connect.Response[process.SendSignalResponse], error) {
	if h.signal != nil {
		h.signal(req.Msg)
	}
	return connect.NewResponse(&process.SendSignalResponse{}), nil
}

func sendWorkspaceStart(stream *connect.ServerStream[process.StartResponse], pid uint32) error {
	return stream.Send(&process.StartResponse{Event: &process.ProcessEvent{
		Event: &process.ProcessEvent_Start{Start: &process.ProcessEvent_StartEvent{Pid: pid}},
	}})
}

func sendWorkspaceResult(stream *connect.ServerStream[process.StartResponse], stdout, stderr string, exit int32) error {
	for _, data := range []*process.ProcessEvent_DataEvent{
		{Output: &process.ProcessEvent_DataEvent_Stdout{Stdout: []byte(stdout)}},
		{Output: &process.ProcessEvent_DataEvent_Stderr{Stderr: []byte(stderr)}},
	} {
		if err := stream.Send(&process.StartResponse{Event: &process.ProcessEvent{
			Event: &process.ProcessEvent_Data{Data: data},
		}}); err != nil {
			return err
		}
	}
	return stream.Send(&process.StartResponse{Event: &process.ProcessEvent{
		Event: &process.ProcessEvent_End{End: &process.ProcessEvent_EndEvent{Exited: true, ExitCode: exit}},
	}})
}

func TestRunProgramNativeOutputAndStdin(t *testing.T) {
	srv := newMockE2BServer(t, func(string) string {
		t.Error("RunProgram must not use /execute")
		return ""
	})
	defer srv.close()
	srv.process.stdout = "before\n__E2B_STDOUT_END__\nafter\x00中文\n\n" + strings.Repeat("x", 128*1024)
	srv.process.stderr = "__E2B_STDERR_END__\n__E2B_EXITCODE__=99\n\n"
	srv.process.exitCode = 7
	c := newMockedExecutor(t, srv)
	stdin := "input\x00\n" + strings.Repeat("s", 128*1024)
	result, err := c.Engine().Runner().RunProgram(context.Background(),
		codeexecutor.Workspace{Path: "/tmp/workspace"},
		codeexecutor.RunProgramSpec{Cmd: "cat", Stdin: stdin})
	require.NoError(t, err)
	assert.Equal(t, srv.process.stdout, result.Stdout)
	assert.Equal(t, srv.process.stderr, result.Stderr)
	assert.Equal(t, 7, result.ExitCode)
	assert.False(t, result.TimedOut)
	assert.Positive(t, result.Duration)
	assert.Zero(t, srv.execCalls)
	srv.process.mu.Lock()
	defer srv.process.mu.Unlock()
	assert.Equal(t, stdin, string(srv.process.inputs[1].data))
	select {
	case <-srv.process.inputs[1].done:
	default:
		t.Error("stdin EOF was not sent")
	}
}

func TestRunProgramDefaultTimeout(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second, 2 * time.Minute} {
		t.Run(timeout.String(), func(t *testing.T) {
			srv := newMockE2BServer(t, nil)
			defer srv.close()
			c := newMockedExecutor(t, srv)
			_, err := c.RunProgram(context.Background(), codeexecutor.Workspace{Path: "/tmp/ws"},
				codeexecutor.RunProgramSpec{Cmd: "true", Timeout: timeout})
			require.NoError(t, err)
			srv.process.mu.Lock()
			defer srv.process.mu.Unlock()
			ms, err := strconv.Atoi(srv.process.headers[0].Get("Connect-Timeout-Ms"))
			require.NoError(t, err)
			want := timeout
			if want <= 0 {
				want = 30 * time.Second
			}
			assert.InDelta(t, want.Milliseconds(), ms, 1000)
			assert.False(t, srv.process.requests[0].GetStdin())
		})
	}
}

// Execute the request's bootstrap locally to test actual argv, environment,
// directory preparation and exit behavior, rather than matching script text.
func TestRunProgramBootstrap(t *testing.T) {
	if _, err := exec.LookPath("/bin/bash"); err != nil {
		t.Skip("bootstrap execution test requires /bin/bash")
	}
	for _, clean := range []bool{false, true} {
		t.Run(fmt.Sprintf("clean=%t", clean), func(t *testing.T) {
			srv := newMockE2BServer(t, nil)
			defer srv.close()
			srv.process.start = func(ctx context.Context, req *connect.Request[process.StartRequest], stream *connect.ServerStream[process.StartResponse]) error {
				if err := sendWorkspaceStart(stream, 1); err != nil {
					return err
				}
				command := exec.CommandContext(ctx, req.Msg.Process.Cmd, req.Msg.Process.Args...)
				command.Env = []string{"PATH=/usr/bin:/bin", "INHERITED=present"}
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				err := command.Run()
				var exitError *exec.ExitError
				var exit int32
				if errors.As(err, &exitError) {
					exit = int32(exitError.ExitCode())
				} else if err != nil {
					return err
				}
				return sendWorkspaceResult(stream, stdout.String(), stderr.String(), exit)
			}
			c := newMockedExecutor(t, srv)
			ws := codeexecutor.Workspace{Path: t.TempDir()}
			var err error
			ws.Path, err = filepath.EvalSymlinks(ws.Path)
			require.NoError(t, err)
			literal := "' \" $(echo injected);\n*"
			env := map[string]string{"FOO": literal, "WORKSPACE_DIR": "override"}
			result, err := c.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
				Cmd: "/bin/sh", Args: []string{"-c", `printf '%s\n' "$PWD" "$INHERITED" "$FOO" "$WORKSPACE_DIR" "$1" "$2"; test -d "$RUN_DIR" && test -d "$OUTPUT_DIR" || exit 9; exit 7`, "test", literal, ""},
				Cwd: "out", Env: env, CleanEnv: clean,
			})
			require.NoError(t, err)
			inherited := "present"
			if clean {
				inherited = ""
			}
			realPath, err := filepath.EvalSymlinks(filepath.Join(ws.Path, "out"))
			require.NoError(t, err)
			assert.Equal(t, strings.Join([]string{realPath, inherited, literal, "override", literal, "", ""}, "\n"), result.Stdout)
			assert.Empty(t, result.Stderr)
			assert.Equal(t, 7, result.ExitCode)
			assert.Equal(t, map[string]string{"FOO": literal, "WORKSPACE_DIR": "override"}, env)
		})
	}
}

func TestRunProgramInvalidArguments(t *testing.T) {
	for _, spec := range []codeexecutor.RunProgramSpec{
		{}, {Cmd: "echo\x00"}, {Cmd: "echo", Args: []string{"\x00"}},
		{Cmd: "echo", Cwd: "\x00"}, {Cmd: "echo", Env: map[string]string{"A=B": "x"}},
	} {
		srv := newMockE2BServer(t, nil)
		c := newMockedExecutor(t, srv)
		_, err := c.RunProgram(context.Background(), codeexecutor.Workspace{Path: "/tmp/ws"}, spec)
		require.Error(t, err)
		assert.Empty(t, srv.process.requests)
		srv.close()
	}
}

func TestRunProgramCancellationAndTimeout(t *testing.T) {
	for _, caller := range []bool{false, true} {
		t.Run(fmt.Sprintf("caller=%t", caller), func(t *testing.T) {
			srv := newMockE2BServer(t, nil)
			defer srv.close()
			rpc := srv.rpc
			srv.rpc = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/Start") {
					// Exercise the client's owned timer, not a second timer in
					// Connect's mock server rounded to whole milliseconds. RPC
					// deadline errors are covered separately as stream failures.
					assert.NotEmpty(t, r.Header.Get("Connect-Timeout-Ms"))
					r.Header.Del("Connect-Timeout-Ms")
				}
				rpc.ServeHTTP(w, r)
			})
			srv.process.start = func(ctx context.Context, _ *connect.Request[process.StartRequest], stream *connect.ServerStream[process.StartResponse]) error {
				if err := sendWorkspaceStart(stream, 1); err != nil {
					return err
				}
				<-ctx.Done()
				return ctx.Err()
			}
			killed := make(chan struct{}, 1)
			srv.process.signal = func(*process.SendSignalRequest) { killed <- struct{}{} }
			c := newMockedExecutor(t, srv)
			ctx := context.Background()
			timeout := 100 * time.Millisecond
			if caller {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
				timeout = time.Minute
			}
			result, err := c.RunProgram(ctx, codeexecutor.Workspace{Path: "/tmp/ws"},
				codeexecutor.RunProgramSpec{Cmd: "sleep", Args: []string{"60"}, Timeout: timeout})
			if caller {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				assert.False(t, result.TimedOut)
				select {
				case <-killed:
				default:
					t.Error("caller deadline did not clean up the process")
				}
			} else {
				require.NoError(t, err)
				assert.True(t, result.TimedOut)
			}
		})
	}
}

func TestRunProgramPreservesPartialOutputOnStreamFailure(t *testing.T) {
	srv := newMockE2BServer(t, nil)
	defer srv.close()
	srv.process.start = func(_ context.Context, _ *connect.Request[process.StartRequest], stream *connect.ServerStream[process.StartResponse]) error {
		if err := sendWorkspaceStart(stream, 1); err != nil {
			return err
		}
		if err := stream.Send(&process.StartResponse{Event: &process.ProcessEvent{
			Event: &process.ProcessEvent_Data{Data: &process.ProcessEvent_DataEvent{
				Output: &process.ProcessEvent_DataEvent_Stdout{Stdout: []byte("partial\n")},
			}},
		}}); err != nil {
			return err
		}
		return connect.NewError(connect.CodeDeadlineExceeded, errors.New("gateway timeout"))
	}
	c := newMockedExecutor(t, srv)
	result, err := c.RunProgram(context.Background(), codeexecutor.Workspace{Path: "/tmp/ws"},
		codeexecutor.RunProgramSpec{Cmd: "echo"})
	require.ErrorContains(t, err, "gateway timeout")
	assert.False(t, result.TimedOut)
	assert.Equal(t, "partial\n", result.Stdout)
}

func TestRunProgramConcurrentRequests(t *testing.T) {
	srv := newMockE2BServer(t, nil)
	defer srv.close()
	c := newMockedExecutor(t, srv)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := c.RunProgram(context.Background(), codeexecutor.Workspace{Path: fmt.Sprintf("/tmp/ws-%d", i)},
				codeexecutor.RunProgramSpec{Cmd: "cat", Stdin: fmt.Sprintf("input-%d", i), Env: map[string]string{"INDEX": strconv.Itoa(i)}})
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()
	srv.process.mu.Lock()
	defer srv.process.mu.Unlock()
	require.Len(t, srv.process.requests, 8)
	for index, req := range srv.process.requests {
		input := string(srv.process.inputs[uint32(index+1)].data)
		value := strings.TrimPrefix(input, "input-")
		assert.Contains(t, req.Process.Args, "INDEX="+value)
		assert.Contains(t, req.Process.Args, "/tmp/ws-"+value)
	}
}

func TestRunProgramUninitializedSandbox(t *testing.T) {
	for _, tc := range []struct {
		name string
		ce   *CodeExecutor
	}{
		{name: "missing executor"},
		{name: "missing sandbox", ce: &CodeExecutor{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := &workspaceRuntime{ce: tc.ce}
			result, err := runtime.RunProgram(context.Background(),
				codeexecutor.Workspace{Path: "/tmp/ws"}, codeexecutor.RunProgramSpec{Cmd: "true"})
			require.ErrorContains(t, err, "sandbox not initialized")
			assert.Empty(t, result.Stdout)
			assert.Empty(t, result.Stderr)
			assert.Zero(t, result.ExitCode)
			assert.False(t, result.TimedOut)
		})
	}
}

func TestRunProgramBootstrapFailuresAndEOF(t *testing.T) {
	if _, err := exec.LookPath("/bin/bash"); err != nil {
		t.Skip("requires /bin/bash")
	}
	for _, tc := range []struct {
		name, cmd, cwd string
		exit           int
	}{
		{"missing command", "trpc-command-does-not-exist", "", 127},
		{"missing directory", "/bin/echo", "missing", 1},
		{"empty stdin", "/bin/cat", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newMockE2BServer(t, nil)
			defer srv.close()
			srv.process.start = func(ctx context.Context, req *connect.Request[process.StartRequest], stream *connect.ServerStream[process.StartResponse]) error {
				if err := sendWorkspaceStart(stream, 1); err != nil {
					return err
				}
				cmd := exec.CommandContext(ctx, req.Msg.Process.Cmd, req.Msg.Process.Args...)
				// Model an old server that ignores Start.Stdin=false and leaves input open.
				reader, writer, err := os.Pipe()
				if err != nil {
					return err
				}
				defer reader.Close()
				defer writer.Close()
				cmd.Stdin = reader
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err = cmd.Run()
				var exitError *exec.ExitError
				var exit int32
				if errors.As(err, &exitError) {
					exit = int32(exitError.ExitCode())
				} else if err != nil {
					return err
				}
				return sendWorkspaceResult(stream, stdout.String(), stderr.String(), exit)
			}
			c := newMockedExecutor(t, srv)
			result, err := c.RunProgram(context.Background(), codeexecutor.Workspace{Path: t.TempDir()},
				codeexecutor.RunProgramSpec{Cmd: tc.cmd, Cwd: tc.cwd, Timeout: 2 * time.Second})
			require.NoError(t, err)
			assert.False(t, result.TimedOut)
			assert.Equal(t, tc.exit, result.ExitCode)
			if tc.exit != 0 {
				assert.NotEmpty(t, result.Stderr)
			}
		})
	}
}
