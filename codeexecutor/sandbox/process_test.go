//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const processHelperEnv = "TRPC_SANDBOX_PROCESS_HELPER"

func TestProcessHelper(t *testing.T) {
	mode := os.Getenv(processHelperEnv)
	if mode == "" {
		return
	}
	switch mode {
	case "echo":
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := scanner.Text()
			_, _ = fmt.Fprintln(os.Stdout, "out:"+line)
			_, _ = fmt.Fprintln(os.Stderr, "err:"+line)
			if line == "quit" {
				return
			}
		}
	case "env":
		_, _ = fmt.Fprintln(os.Stdout, os.Getenv("TRPC_SANDBOX_ENV_PROBE"))
	case "sleep":
		time.Sleep(150 * time.Millisecond)
		_, _ = fmt.Fprintln(os.Stdout, "done")
	default:
		os.Exit(2)
	}
}

func TestStartProcessSupportsBidirectionalSeparatedStreams(t *testing.T) {
	rt, ws := newProcessTestRuntime(t)
	process := startProcessHelper(t, rt, ws, "echo", 0)
	stdout := bufio.NewReader(process.Stdout())
	stderr := bufio.NewReader(process.Stderr())

	_, err := io.WriteString(process.Stdin(), "first\n")
	require.NoError(t, err)
	require.Equal(t, "out:first\n", readLine(t, stdout))
	require.Equal(t, "err:first\n", readLine(t, stderr))

	_, err = io.WriteString(process.Stdin(), "quit\n")
	require.NoError(t, err)
	require.Equal(t, "out:quit\n", readLine(t, stdout))
	require.Equal(t, "err:quit\n", readLine(t, stderr))
	require.NoError(t, process.Stdin().Close())
	require.NoError(t, process.Wait())
}

func TestProcessSpecConvertsExecutionFields(t *testing.T) {
	spec := ProcessSpec{
		Cmd:      "python3",
		Args:     []string{"guest.py"},
		Env:      map[string]string{"MODE": "test"},
		CleanEnv: true,
		Cwd:      codeexecutor.DirRuns,
		Timeout:  time.Minute,
		Limits:   codeexecutor.ResourceLimits{MemoryMB: 128},
	}
	got := spec.runProgramSpec()
	require.Equal(t, spec.Cmd, got.Cmd)
	require.Equal(t, spec.Args, got.Args)
	require.Equal(t, spec.Env, got.Env)
	require.Equal(t, spec.Cwd, got.Cwd)
	require.Equal(t, spec.Timeout, got.Timeout)
	require.Equal(t, spec.Limits, got.Limits)
	require.Empty(t, got.Stdin)
	require.False(t, got.CleanEnv)
}

func TestNilProcessOperations(t *testing.T) {
	var process *Process
	require.Nil(t, process.Stdin())
	require.Nil(t, process.Stdout())
	require.Nil(t, process.Stderr())
	require.NoError(t, process.Kill())
	require.NoError(t, process.Wait())

	var prepared *preparedProcess
	require.NotPanics(t, prepared.cleanup)
}

func TestProcessWaitIsConcurrentAndIdempotent(t *testing.T) {
	rt, ws := newProcessTestRuntime(t)
	process := startProcessHelper(t, rt, ws, "sleep", 5*time.Second)
	_, err := io.Copy(io.Discard, process.Stdout())
	require.NoError(t, err)

	const waiters = 8
	errs := make(chan error, waiters)
	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- process.Wait()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, process.Wait())
	require.NoError(t, process.Kill())
}

func TestStartProcessZeroTimeoutDoesNotUseRunDefault(t *testing.T) {
	rt, ws := newProcessTestRuntime(t, WithDefaultTimeout(time.Millisecond))
	process := startProcessHelper(t, rt, ws, "sleep", 0)
	out, err := io.ReadAll(process.Stdout())
	require.NoError(t, err)
	require.NoError(t, process.Wait())
	require.Contains(t, string(out), "done\n")
}

func TestStartProcessExplicitTimeoutStopsProgram(t *testing.T) {
	rt, ws := newProcessTestRuntime(t)
	started := time.Now()
	process := startProcessHelper(t, rt, ws, "sleep", 20*time.Millisecond)
	_, _ = io.Copy(io.Discard, process.Stdout())
	require.ErrorIs(t, process.Wait(), context.DeadlineExceeded)
	require.Less(t, time.Since(started), 2*time.Second)
}

func TestStartProcessContextCancellationStopsProgram(t *testing.T) {
	rt, ws := newProcessTestRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	process, err := rt.StartProcess(ctx, ws, processHelperSpec("sleep", 0))
	require.NoError(t, err)
	cancel()
	_, _ = io.Copy(io.Discard, process.Stdout())
	waitErr := process.Wait()
	require.ErrorIs(t, waitErr, context.Canceled)
	var exitErr *exec.ExitError
	require.ErrorAs(t, waitErr, &exitErr)
}

func TestStartProcessFailureReleasesSerialRunLock(t *testing.T) {
	rt, ws := newProcessTestRuntime(t)
	_, err := rt.StartProcess(context.Background(), ws, ProcessSpec{
		Cmd: "program-that-does-not-exist",
	})
	require.Error(t, err)

	process := startProcessHelper(t, rt, ws, "sleep", 5*time.Second)
	_, err = io.Copy(io.Discard, process.Stdout())
	require.NoError(t, err)
	require.NoError(t, process.Wait())
}

func TestStartProcessCleanEnvDoesNotInheritHost(t *testing.T) {
	t.Setenv("TRPC_SANDBOX_ENV_PROBE", "must-not-leak")
	rt, ws := newProcessTestRuntime(t)
	process := startProcessHelper(t, rt, ws, "env", 5*time.Second)
	out, err := io.ReadAll(process.Stdout())
	require.NoError(t, err)
	require.NoError(t, process.Wait())
	require.NotContains(t, string(out), "must-not-leak")
	require.True(t, strings.HasPrefix(string(out), "\n"))
	capabilities := rt.Describe()
	require.False(t, capabilities.Streaming)
	require.False(t, capabilities.SupportsCleanEnv)
}

func TestRunProgramKeepsLegacyCleanEnvBehavior(t *testing.T) {
	t.Setenv("TRPC_SANDBOX_ENV_PROBE", "still-inherited")
	rt, ws := newProcessTestRuntime(t)
	result, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:      os.Args[0],
		Args:     []string{"-test.run=TestProcessHelper"},
		Env:      map[string]string{processHelperEnv: "env"},
		CleanEnv: true,
	})
	require.NoError(t, err)
	require.Contains(t, result.Stdout, "still-inherited\n")
	require.False(t, rt.Describe().SupportsCleanEnv)
	require.Empty(t, rt.runLocks)
}

func TestRunProgramContextCancellationUnblocksSerialLockWait(t *testing.T) {
	rt, ws := newProcessTestRuntime(t)
	first := startProcessHelper(t, rt, ws, "echo", 0)
	t.Cleanup(func() {
		_ = first.Kill()
		_ = first.Wait()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := rt.RunProgram(
			ctx,
			ws,
			codeexecutor.RunProgramSpec{
				Cmd:  os.Args[0],
				Args: []string{"-test.run=^TestProcessHelper$"},
				Env:  map[string]string{processHelperEnv: "sleep"},
			},
		)
		result <- err
	}()

	require.Eventually(t, func() bool {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		lock := rt.runLocks[workspaceRunLockKey(ws)]
		return lock != nil && lock.refs == 2
	}, time.Second, 5*time.Millisecond)
	cancel()

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceled RunProgram remained blocked on the serial run lock")
	}
	require.Len(t, rt.runLocks, 1)
	require.NoError(t, first.Kill())
	require.Error(t, first.Wait())
	require.Empty(t, rt.runLocks)
}

func TestStartProcessHoldsSerialRunLockUntilWait(t *testing.T) {
	rt, ws := newProcessTestRuntime(t)
	first := startProcessHelper(t, rt, ws, "echo", 0)

	started := make(chan *Process, 1)
	errs := make(chan error, 1)
	go func() {
		process, err := rt.StartProcess(
			context.Background(),
			ws,
			processHelperSpec("sleep", 5*time.Second),
		)
		if err != nil {
			errs <- err
			return
		}
		started <- process
	}()

	select {
	case process := <-started:
		_ = process.Kill()
		_ = process.Wait()
		t.Fatal("second process started while the first held the serial lock")
	case err := <-errs:
		t.Fatalf("second process failed before lock release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	_, err := io.WriteString(first.Stdin(), "quit\n")
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, first.Stdout())
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, first.Stderr())
	require.NoError(t, err)
	require.NoError(t, first.Wait())

	select {
	case second := <-started:
		require.Len(t, rt.runLocks, 1)
		_ = second.Kill()
		_ = second.Wait()
	case err := <-errs:
		t.Fatalf("second process failed after lock release: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("second process did not start after lock release")
	}
	require.Empty(t, rt.runLocks)
}

func TestStartProcessContextCancellationUnblocksSerialLockWait(t *testing.T) {
	rt, ws := newProcessTestRuntime(t)
	first := startProcessHelper(t, rt, ws, "echo", 0)
	t.Cleanup(func() {
		_ = first.Kill()
		_ = first.Wait()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type startResult struct {
		process *Process
		err     error
	}
	started := make(chan struct{})
	result := make(chan startResult, 1)
	go func() {
		close(started)
		process, err := rt.StartProcess(
			ctx,
			ws,
			processHelperSpec("sleep", 5*time.Second),
		)
		result <- startResult{process: process, err: err}
	}()
	<-started

	select {
	case got := <-result:
		if got.process != nil {
			_ = got.process.Kill()
			_ = got.process.Wait()
		}
		t.Fatalf("second process returned before cancellation: %v", got.err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()

	select {
	case got := <-result:
		if got.process != nil {
			_ = got.process.Kill()
			_ = got.process.Wait()
		}
		require.ErrorIs(t, got.err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceled StartProcess remained blocked on the serial run lock")
	}
	require.Len(t, rt.runLocks, 1)
	require.NoError(t, first.Kill())
	require.Error(t, first.Wait())
	require.Empty(t, rt.runLocks)
}

func newProcessTestRuntime(t *testing.T, extra ...Option) (*Runtime, codeexecutor.Workspace) {
	t.Helper()
	options := []Option{
		WithPermissionProfile(DangerFullAccessProfile()),
		WithSessionPolicy(SessionPolicy{
			Persistence:    SessionPersistencePerTurn,
			RunConcurrency: SessionRunConcurrencySerial,
		}),
		WithShellEnvironmentPolicy(ShellEnvironmentPolicy{
			Inherit: ShellEnvironmentPolicyInheritAll,
		}),
	}
	options = append(options, extra...)
	rt := NewRuntime(options...)
	ws, err := rt.CreateWorkspace(context.Background(), t.Name(), codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, rt.Cleanup(context.Background(), ws))
	})
	return rt, ws
}

func startProcessHelper(
	t *testing.T,
	rt *Runtime,
	ws codeexecutor.Workspace,
	mode string,
	timeout time.Duration,
) *Process {
	t.Helper()
	process, err := rt.StartProcess(
		context.Background(),
		ws,
		processHelperSpec(mode, timeout),
	)
	require.NoError(t, err)
	return process
}

func processHelperSpec(mode string, timeout time.Duration) ProcessSpec {
	return ProcessSpec{
		Cmd:      os.Args[0],
		Args:     []string{"-test.run=^TestProcessHelper$"},
		Env:      map[string]string{processHelperEnv: mode},
		CleanEnv: true,
		Timeout:  timeout,
	}
}

func readLine(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	return line
}
