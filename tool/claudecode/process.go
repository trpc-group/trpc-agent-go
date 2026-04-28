//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package claudecode

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
)

type capturedProcessResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type waitedProcessState struct {
	State *os.ProcessState
	Err   error
}

type processCapture struct {
	stdout bytes.Buffer
	stderr bytes.Buffer
	wg     sync.WaitGroup
}

func runCapturedProcess(
	ctx context.Context,
	dir string,
	env []string,
	bin string,
	args ...string,
) (capturedProcessResult, error) {
	processPath, err := exec.LookPath(bin)
	if err != nil {
		return capturedProcessResult{}, err
	}
	stdin, stdoutReader, stdoutWriter, stderrReader, stderrWriter, closeErr, err := processPipes()
	if err != nil {
		return capturedProcessResult{}, err
	}
	defer closeErr()
	proc, err := os.StartProcess(
		processPath,
		append([]string{processPath}, args...),
		&os.ProcAttr{
			Dir:   dir,
			Env:   processEnv(env),
			Files: []*os.File{stdin, stdoutWriter, stderrWriter},
		},
	)
	if err != nil {
		return capturedProcessResult{}, err
	}
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	capture := startProcessCapture(stdoutReader, stderrReader)
	state := waitForProcess(ctx, proc)
	stdout, stderr := capture.wait()
	result := capturedProcessResult{
		Stdout: stdout,
		Stderr: stderr,
	}
	if state.State != nil {
		result.ExitCode = state.State.ExitCode()
	}
	return result, state.Err
}

func startProcess(
	dir string,
	env []string,
	stdoutFile *os.File,
	stderrFile *os.File,
	bin string,
	args ...string,
) (*os.Process, error) {
	processPath, err := exec.LookPath(bin)
	if err != nil {
		return nil, err
	}
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer stdin.Close()
	return os.StartProcess(
		processPath,
		append([]string{processPath}, args...),
		&os.ProcAttr{
			Dir:   dir,
			Env:   processEnv(env),
			Files: []*os.File{stdin, stdoutFile, stderrFile},
		},
	)
}

func processPipes() (
	*os.File,
	*os.File,
	*os.File,
	*os.File,
	*os.File,
	func() error,
	error,
) {
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, nil, nil, nil, err
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		return nil, nil, nil, nil, nil, nil, err
	}
	closeAll := func() error {
		var firstErr error
		for _, closer := range []io.Closer{stdin, stdoutReader, stdoutWriter, stderrReader, stderrWriter} {
			if closer == nil {
				continue
			}
			if err := closer.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
	return stdin, stdoutReader, stdoutWriter, stderrReader, stderrWriter, closeAll, nil
}

func processEnv(extra []string) []string {
	env := os.Environ()
	if len(extra) == 0 {
		return env
	}
	return append(env, extra...)
}

func startProcessCapture(stdoutReader *os.File, stderrReader *os.File) *processCapture {
	capture := &processCapture{}
	capture.wg.Add(2)
	go func() {
		defer capture.wg.Done()
		_, _ = io.Copy(&capture.stdout, stdoutReader)
	}()
	go func() {
		defer capture.wg.Done()
		_, _ = io.Copy(&capture.stderr, stderrReader)
	}()
	return capture
}

func (c *processCapture) wait() ([]byte, []byte) {
	c.wg.Wait()
	return c.stdout.Bytes(), c.stderr.Bytes()
}

func waitForProcess(ctx context.Context, proc *os.Process) waitedProcessState {
	waitCh := make(chan waitedProcessState, 1)
	go func() {
		state, err := proc.Wait()
		waitCh <- waitedProcessState{State: state, Err: err}
	}()
	select {
	case state := <-waitCh:
		return state
	case <-ctx.Done():
		_ = proc.Kill()
		state := <-waitCh
		if state.Err == nil {
			state.Err = ctx.Err()
		}
		return state
	}
}
