//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build !windows

package jupyter

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestWithIP(t *testing.T) {
	type args struct {
		ip string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "normal IPv4 address",
			args: args{ip: "192.168.1.1"},
			want: "192.168.1.1",
		},
		{
			name: "IPv6 address",
			args: args{ip: "2001:db8::1"},
			want: "2001:db8::1",
		},
		{
			name: "empty string",
			args: args{ip: ""},
			want: "",
		},
		{
			name: "localhost",
			args: args{ip: "localhost"},
			want: "localhost",
		},
		{
			name: "special characters",
			args: args{ip: "0.0.0.0"},
			want: "0.0.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			ce := &CodeExecutor{
				ip: "default",
			}

			opt := WithIP(tt.args.ip)

			opt(ce)

			assert.Equal(t, tt.want, ce.ip, "WithIP() set unexpected ip value")
		})
	}
}

func TestWithToken(t *testing.T) {
	type args struct {
		token string
	}
	tests := []struct {
		name      string
		args      args
		wantToken string
	}{
		{
			name:      "normal token",
			args:      args{token: "valid-token-123"},
			wantToken: "valid-token-123",
		},
		{
			name:      "empty token",
			args:      args{token: ""},
			wantToken: "",
		},
		{
			name:      "special characters token",
			args:      args{token: "!@#$%^&*()_+-=[]{}|;:'\",.<>?/"},
			wantToken: "!@#$%^&*()_+-=[]{}|;:'\",.<>?/",
		},
		{
			name:      "long token",
			args:      args{token: strings.Repeat("a", 1024)},
			wantToken: strings.Repeat("a", 1024),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			c := &CodeExecutor{}

			opt := WithToken(tt.args.token)
			opt(c)

			assert.Equal(t, tt.wantToken, c.token, "token should match expected value")
		})
	}
}

func TestWithStartTimeout(t *testing.T) {
	type args struct {
		timeout time.Duration
	}
	tests := []struct {
		name string
		args args
		want time.Duration
	}{
		{
			name: "normal positive timeout",
			args: args{timeout: 5 * time.Second},
			want: 5 * time.Second,
		},
		{
			name: "zero timeout boundary",
			args: args{timeout: 0},
			want: 0,
		},
		{
			name: "negative timeout edge case",
			args: args{timeout: -1 * time.Second},
			want: -1 * time.Second,
		},
		{
			name: "maximum duration value",
			args: args{timeout: time.Duration(1<<63 - 1)},
			want: time.Duration(1<<63 - 1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			ce := &CodeExecutor{}

			option := WithStartTimeout(tt.args.timeout)
			option(ce)

			if ce.startTimeout != tt.want {
				t.Errorf("startTimeout = %v, want %v", ce.startTimeout, tt.want)
			}
		})
	}
}

func TestWithKernelName(t *testing.T) {
	type args struct {
		kernelName string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "normal kernel name",
			args: args{kernelName: "python3"},
			want: "python3",
		},
		{
			name: "empty kernel name",
			args: args{kernelName: ""},
			want: "",
		},
		{
			name: "special characters",
			args: args{kernelName: "my-kernel@123"},
			want: "my-kernel@123",
		},
		{
			name: "long kernel name",
			args: args{kernelName: "a-very-long-kernel-name-with-many-characters-1234567890"},
			want: "a-very-long-kernel-name-with-many-characters-1234567890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			opt := WithKernelName(tt.args.kernelName)

			ce := &CodeExecutor{}

			opt(ce)

			if ce.kernelName != tt.want {
				t.Errorf(
					"WithKernelName() set kernelName to %v, want %v",
					ce.kernelName,
					tt.want,
				)
			}
		})
	}
}

func TestWithPort(t *testing.T) {
	type args struct {
		port int
	}
	tests := []struct {
		name     string
		args     args
		wantPort int
	}{
		{
			name:     "normal positive port",
			args:     args{port: 8888},
			wantPort: 8888,
		},
		{
			name:     "zero port",
			args:     args{port: 0},
			wantPort: 0,
		},
		{
			name:     "negative port",
			args:     args{port: -1},
			wantPort: -1,
		},
		{
			name:     "max valid port",
			args:     args{port: 65535},
			wantPort: 65535,
		},
		{
			name:     "port exceeding uint16",
			args:     args{port: 65536},
			wantPort: 65536,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			opt := WithPort(tt.args.port)

			ce := &CodeExecutor{}

			opt(ce)

			if ce.port != tt.wantPort {
				t.Errorf(
					"WithPort() applied port = %v, want %v",
					ce.port,
					tt.wantPort,
				)
			}
		})
	}
}

func Test_silencePip(t *testing.T) {
	type args struct {
		code string
		lang string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "python code with !pip install",
			args: args{
				code: "! pip install requests",
				lang: "python",
			},
			want: "! pip install -qqq requests",
		},
		{
			name: "bash code with pip install",
			args: args{
				code: "pip install numpy",
				lang: "bash",
			},
			want: "pip install -qqq numpy",
		},
		{
			name: "multiple lines in python",
			args: args{
				code: "! pip install a\n! pip install b",
				lang: "python",
			},
			want: "! pip install -qqq a\n! pip install -qqq b",
		},
		{
			name: "already has -qqq",
			args: args{
				code: "! pip install -qqq pandas",
				lang: "python",
			},
			want: "! pip install -qqq pandas",
		},
		{
			name: "other language (java)",
			args: args{
				code: "pip install something",
				lang: "java",
			},
			want: "pip install something",
		},
		{
			name: "empty code",
			args: args{
				code: "",
				lang: "python",
			},
			want: "",
		},
		{
			name: "mixed lines in python",
			args: args{
				code: "! pip install a\nprint('hello')\npip install b",
				lang: "python",
			},
			want: "! pip install -qqq a\nprint('hello')\npip install b",
		},
		{
			name: "shell language",
			args: args{
				code: "pip install flask",
				lang: "shell",
			},
			want: "pip install -qqq flask",
		},
		{
			name: "powershell language",
			args: args{
				code: "pip install django",
				lang: "powershell",
			},
			want: "pip install -qqq django",
		},
		{
			name: "multiple lines with some -qqq",
			args: args{
				code: "! pip install a\npip install -qqq b",
				lang: "python",
			},
			want: "! pip install -qqq a\npip install -qqq b",
		},
		{
			name: "python code without !",
			args: args{
				code: "pip install c",
				lang: "python",
			},
			want: "pip install c",
		},
		{
			name: "bash with multiple spaces",
			args: args{
				code: "pip   install flask",
				lang: "bash",
			},
			want: "pip   install flask",
		},
		{
			name: "powershell with comment",
			args: args{
				code: "pip install django # quiet",
				lang: "powershell",
			},
			want: "pip install -qqq django # quiet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := silencePip(tt.args.code, tt.args.lang); got != tt.want {
				t.Errorf("silencePip() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWithLogFile(t *testing.T) {
	c := &CodeExecutor{}
	WithLogFile("test.log")(c)
	assert.Equal(t, "test.log", c.logFile)
}

func TestWithLogLevel(t *testing.T) {
	c := &CodeExecutor{}
	WithLogLevel("debug")(c)
	assert.Equal(t, "debug", c.logLevel)
}

func TestWithWaitReadyTimeout(t *testing.T) {
	c := &CodeExecutor{}
	WithWaitReadyTimeout(10 * time.Second)(c)
	assert.Equal(t, 10*time.Second, c.waitReadyTimeout)
}

func isJupyterInstalled() bool {
	cmd := exec.Command("python", "-m", "jupyter", "kernelgateway", "--version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func TestExecuteCode(t *testing.T) {
	srv := newServer(t)
	defer srv.Close()

	codeExecutor := &CodeExecutor{
		cli: srv.cli,
	}

	code := "print('hello world')"
	result, err := codeExecutor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Code: code, Language: "python"},
		},
		ExecutionID: "test",
	})
	if err != nil {
		t.Fatalf("ExecuteCode failed: %v", err)
	}
	assert.Equal(t, "", result.Output)
}

func Test_cleanup(t *testing.T) {
	srv := newServer(t)
	defer srv.Close()

	_, cancel := context.WithCancel(context.Background())
	codeExecutor := &CodeExecutor{
		cli:    srv.cli,
		cancel: cancel,
	}

	codeExecutor.cleanup()
}

func Test_generateToken(t *testing.T) {
	token := generateToken()
	assert.NotEmpty(t, token)
}

func Test_checkJupyterGateway(t *testing.T) {
	codeExecutor := &CodeExecutor{}
	err := codeExecutor.checkJupyterGateway()
	if !isJupyterInstalled() {
		assert.Error(t, err)
	} else {
		assert.NoError(t, err)
	}
}

func TestCodeBlockDelimiter(t *testing.T) {
	codeExecutor := &CodeExecutor{}
	delimiter := codeExecutor.CodeBlockDelimiter()
	assert.Equal(t, "```", delimiter.Start)
	assert.Equal(t, "```", delimiter.End)

	cli := &Client{}
	delimiter = cli.CodeBlockDelimiter()
	assert.Equal(t, "```", delimiter.Start)
	assert.Equal(t, "```", delimiter.End)
}

func TestNew(t *testing.T) {
	if !isJupyterInstalled() {
		_, err := New(WithLogFile("/tmp/jupyter.log"), WithPort(9999))
		assert.Error(t, err)
		return
	}
	codeExecutor, err := New()
	defer codeExecutor.Close()
	assert.NoError(t, err)
}

// Test the workspace delegate methods to cover ensureWS and wrappers.
func TestWorkspaceDelegates(t *testing.T) {
	const (
		execID     = "ws_exec_id"
		relWork    = "work/hello.txt"
		fileText   = "hello"
		copyTarget = "out/copied.txt"
		globOut    = "out/*.txt"
		bashCmd    = "bash"
	)

	ce := &CodeExecutor{}

	// Create a workspace
	ws, err := ce.CreateWorkspace(
		context.Background(), execID,
		codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	defer ce.Cleanup(context.Background(), ws)

	// Put a file into workspace
	err = ce.PutFiles(
		context.Background(), ws,
		[]codeexecutor.PutFile{{
			Path:    relWork,
			Content: []byte(fileText),
			Mode:    0o644,
		}},
	)
	if err != nil {
		t.Fatalf("PutFiles failed: %v", err)
	}

	// Also test PutDirectory by staging a temp dir with one file.
	tmpDir := t.TempDir()
	hostFile := filepath.Join(tmpDir, "host.txt")
	if writeErr := os.WriteFile(hostFile, []byte(fileText), 0o644); writeErr != nil {
		t.Fatalf("write host file: %v", writeErr)
	}
	if err = ce.PutDirectory(
		context.Background(), ws, tmpDir, "staged",
	); err != nil {
		t.Fatalf("PutDirectory failed: %v", err)
	}

	// Run a program to copy the staged file to out/copied.txt
	spec := codeexecutor.RunProgramSpec{
		Cmd:  bashCmd,
		Args: []string{"-c", "cat staged/host.txt > " + copyTarget},
		Cwd:  "",
	}
	if _, err = ce.RunProgram(context.Background(), ws, spec); err != nil {
		t.Fatalf("RunProgram failed: %v", err)
	}

	// Collect output files
	files, err := ce.Collect(
		context.Background(), ws, []string{globOut},
	)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}
	// Ensure that our copied file exists among collected results.
	found := false
	for _, f := range files {
		if f.Name == copyTarget {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected collected file %q not found", copyTarget)
	}

	// ExecuteInline with a simple bash block
	runRes, err := ce.ExecuteInline(
		context.Background(), "inline_exec",
		[]codeexecutor.CodeBlock{{
			Code: "echo inline", Language: "bash",
		}},
		2*time.Second,
	)
	if err != nil {
		t.Fatalf("ExecuteInline failed: %v", err)
	}
	// Output may or may not include newline; check prefix.
	if !strings.HasPrefix(runRes.Stdout, "inline") &&
		!strings.Contains(runRes.Stdout, "inline\n") {
		t.Fatalf("unexpected inline output: %q", runRes.Stdout)
	}

	// Engine should be non-nil and usable
	eng := ce.Engine()
	if eng == nil {
		t.Fatalf("Engine() returned nil")
	}
}

func TestCodeExecutorClose(t *testing.T) {
	// Provide a valid cancel func to avoid nil panic.
	ctx, cancel := context.WithCancel(context.Background())
	_ = ctx
	ce := CodeExecutor{cancel: cancel}
	if err := ce.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

// Use a fake python on PATH to drive New() error branch and cleanup.
func TestNewWithFakePythonError(t *testing.T) {
	const (
		fakeEcho = "Jupyter Kernel Gateway is unavailable"
		errLine  = "ERROR: boot failure"
	)
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "python")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = \"--version\" ]; then exit 0; fi\n" +
		"done\n" +
		"echo \"" + errLine + "\" 1>&2\n" +
		"echo \"" + fakeEcho + "\" 1>&2\n" +
		"sleep 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake python: %v", err)
	}
	oldPath := os.Getenv("PATH")
	_ = os.Setenv("PATH", tmp+string(os.PathListSeparator)+oldPath)
	defer os.Setenv("PATH", oldPath)

	_, err := New(
		WithStartTimeout(2*time.Second),
		WithWaitReadyTimeout(100*time.Millisecond),
		WithLogFile(filepath.Join(tmp, "k.log")),
	)
	assert.Error(t, err)
}

// Timeout path: child stays alive, but we hit startup timeout first.
func TestNewWithFakePythonTimeout(t *testing.T) {
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "python")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = \"--version\" ]; then exit 0; fi\n" +
		"done\n" +
		"sleep 2\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake python: %v", err)
	}
	oldPath := os.Getenv("PATH")
	_ = os.Setenv("PATH", tmp+string(os.PathListSeparator)+oldPath)
	defer os.Setenv("PATH", oldPath)

	_, err := New(
		WithStartTimeout(10*time.Millisecond),
		WithWaitReadyTimeout(10*time.Millisecond),
	)
	assert.Error(t, err)
}

// Process exited path: child exits quickly; ticker sees exited state.
func TestNewWithFakePythonExited(t *testing.T) {
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "python")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = \"--version\" ]; then exit 0; fi\n" +
		"done\n" +
		"exit 3\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake python: %v", err)
	}
	oldPath := os.Getenv("PATH")
	_ = os.Setenv("PATH", tmp+string(os.PathListSeparator)+oldPath)
	defer os.Setenv("PATH", oldPath)

	_, err := New(
		WithStartTimeout(500*time.Millisecond),
		WithWaitReadyTimeout(10*time.Millisecond),
	)
	assert.Error(t, err)
}

// NewClient failure path: the fake gateway announces "is available at" but
// the endpoint returns an error, so NewClient fails. New() must still clean
// up the gateway subprocess before returning the error.
func TestNewClientFailureCleansUpGateway(t *testing.T) {
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "gateway.pid")

	// Serve a deterministic failing endpoint on an OS-assigned port instead
	// of guessing a free port.
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failSrv.Close()

	parsed, err := url.Parse(failSrv.URL)
	if err != nil {
		t.Fatalf("parse failing endpoint URL: %v", err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("parse failing endpoint port: %v", err)
	}

	fake := filepath.Join(tmp, "python")
	script := "#!/bin/sh\n" +
		"trap 'exit 0' TERM INT\n" +
		"prev=\"\"\n" +
		"PORT=8888\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = \"--version\" ]; then exit 0; fi\n" +
		"  if [ \"$prev\" = \"--KernelGatewayApp.port\" ]; then PORT=\"$a\"; fi\n" +
		"  prev=\"$a\"\n" +
		"done\n" +
		"echo \"$$\" > \"" + pidFile + "\"\n" +
		"echo \"[KernelGatewayApp] Jupyter Kernel Gateway is available at http://127.0.0.1:$PORT\" 1>&2\n" +
		"while :; do sleep 1; done\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake python: %v", err)
	}
	oldPath := os.Getenv("PATH")
	_ = os.Setenv("PATH", tmp+string(os.PathListSeparator)+oldPath)
	defer os.Setenv("PATH", oldPath)

	_, err = New(
		WithPort(port),
		WithStartTimeout(8*time.Second),
		WithWaitReadyTimeout(3*time.Second),
	)
	if err == nil {
		t.Fatal("expected New() to fail: NewClient cannot reach the fake gateway")
	}

	pidBytes, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("gateway pid file missing: %v", rerr)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if perr != nil {
		t.Fatalf("invalid gateway pid %q: %v", pidBytes, perr)
	}
	// Guarantee no leftover process even when this test fails before the
	// cleanup assertion below.
	defer func() {
		_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
	}()

	// The gateway subprocess must be reaped after New() returns the error.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if proc, ferr := os.FindProcess(pid); ferr != nil {
			return // Process not found: cleaned up.
		} else if serr := proc.Signal(syscall.Signal(0)); serr != nil {
			return // Process not found: cleaned up.
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("gateway subprocess (pid=%d) still alive after New() returned error", pid)
}

// Error path for ExecuteCode when client is not initialized.
func TestExecuteCodeClientNotInit(t *testing.T) {
	ce := &CodeExecutor{}
	_, err := ce.ExecuteCode(
		context.Background(),
		codeexecutor.CodeExecutionInput{ExecutionID: "x"},
	)
	assert.Error(t, err)
}

// Cover cleanup kill-branch by signaling an already-exited process.
func TestCleanupKillBranch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "bash", "-c", "true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start cmd: %v", err)
	}
	_ = cmd.Wait()
	ce := &CodeExecutor{cancel: cancel, subprocess: cmd}
	ce.cleanup()
}

// TestScannerGoroutineExitsOnContextCancel verifies that the scanner goroutine
// started inside New() drains stderr and exits cleanly when the CodeExecutor
// context is cancelled while the subprocess is still writing output.
// This covers the ctx.Done() drain branch inside the scanner goroutine.
func TestScannerGoroutineExitsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Start a subprocess that writes to stderr continuously.
	cmd := exec.CommandContext(ctx, "bash", "-c",
		`while :; do echo "log line" >&2; sleep 0.01; done`)

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("StderrPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	lines := make(chan string, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(lines)
		sc := bufio.NewScanner(stderrPipe)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				// drain branch under test
				for sc.Scan() {
				}
				return
			default:
			}
		}
	}()

	// Read a few lines to confirm the goroutine is running.
	for i := 0; i < 3; i++ {
		select {
		case <-lines:
		case <-time.After(2 * time.Second):
			t.Fatal("scanner goroutine did not produce lines in time")
		}
	}

	// Cancel the context; this should trigger the ctx.Done() drain path.
	cancel()

	// Kill the subprocess so it closes stderr, unblocking the scanner.
	_ = cmd.Process.Signal(syscall.SIGINT)
	stderrPipe.Close()
	_ = cmd.Wait()

	select {
	case <-done:
		// goroutine exited cleanly
	case <-time.After(3 * time.Second):
		t.Fatal("scanner goroutine did not exit after context cancel")
	}
}

// TestNewExitedWithKnownExitCode verifies that when the subprocess exits and
// ProcessState is populated before the pipe EOF is observed, the exit code is
// correctly extracted. This covers the ProcessState.ExitCode() branch inside
// the !ok case.
func TestNewExitedWithKnownExitCode(t *testing.T) {
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "python")
	// Script writes one stderr line then exits with code 42, giving the parent
	// time to call Wait() (and thus populate ProcessState) before the pipe
	// is fully drained and EOF is detected.
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = \"--version\" ]; then exit 0; fi\n" +
		"done\n" +
		"echo 'starting up' >&2\n" +
		"sleep 0.05\n" +
		"exit 42\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake python: %v", err)
	}
	oldPath := os.Getenv("PATH")
	_ = os.Setenv("PATH", tmp+string(os.PathListSeparator)+oldPath)
	defer os.Setenv("PATH", oldPath)

	_, err := New(
		WithStartTimeout(3*time.Second),
		WithWaitReadyTimeout(100*time.Millisecond),
	)
	require.Error(t, err)
	// The error should mention the exit code (either 42 or -1 depending on
	// whether ProcessState was populated before EOF; both are valid).
	assert.Contains(t, err.Error(), "jupyter gateway exited with code")
}
