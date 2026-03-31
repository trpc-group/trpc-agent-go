//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hostexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	defaultYieldMS    = 10_000
	defaultTimeoutS   = 1_800
	defaultLogTail    = 40
	defaultMaxLines   = 20_000
	defaultJobTTL     = 30 * time.Minute
	defaultKillGrace  = 2 * time.Second
	timeoutKillGrace  = time.Duration(0)
	defaultIODrain    = 1 * time.Second
	maxTimeoutSeconds = int64((1<<63)-1) /
		int64(time.Second)
)

var errUnknownSession = errors.New("unknown session id")

type manager struct {
	mu       sync.Mutex
	sessions map[string]*session

	maxLines int
	jobTTL   time.Duration
	baseEnv  map[string]string

	clock func() time.Time
}

type execParams struct {
	Command    string
	Workdir    string
	Env        map[string]string
	Pty        bool
	Background bool

	YieldMs  *int
	TimeoutS *int
}

type execResult struct {
	Status    string
	Output    string
	ExitCode  *int
	SessionID string
}

func newManager() *manager {
	return &manager{
		sessions: map[string]*session{},
		maxLines: defaultMaxLines,
		jobTTL:   defaultJobTTL,
		clock:    time.Now,
	}
}

func (m *manager) exec(
	ctx context.Context,
	params execParams,
) (execResult, error) {
	if ctx == nil {
		return execResult{}, errors.New("nil context")
	}
	if strings.TrimSpace(params.Command) == "" {
		return execResult{}, errors.New(errCommandRequired)
	}

	m.cleanupExpired()

	yieldMs := defaultYieldMS
	if params.YieldMs != nil && *params.YieldMs >= 0 {
		yieldMs = *params.YieldMs
	}

	timeoutS := defaultTimeoutS
	if params.TimeoutS != nil && *params.TimeoutS > 0 {
		timeoutS = *params.TimeoutS
	}
	timeout := timeoutDuration(timeoutS)

	if !params.Background && yieldMs == 0 && !params.Pty {
		out, code, err := runForeground(
			ctx,
			params,
			timeout,
			m.baseEnv,
		)
		if err != nil {
			return execResult{}, err
		}
		return execResult{
			Status:   programStatusExited,
			Output:   out,
			ExitCode: intPtr(code),
		}, nil
	}

	sess, err := m.startBackground(params, timeout)
	if err != nil {
		return execResult{}, err
	}

	if params.Background {
		return execResult{
			Status:    programStatusRunning,
			SessionID: sess.id,
			Output:    sess.pollTail(defaultLogTail),
		}, nil
	}

	if yieldMs == 0 {
		select {
		case <-ctx.Done():
			_ = m.kill(sess.id)
			return execResult{}, ctx.Err()
		case <-sess.doneCh:
		}
		out, code := sess.allOutput()
		_ = m.clearFinished(sess.id)
		return execResult{
			Status:   programStatusExited,
			Output:   out,
			ExitCode: intPtr(code),
		}, nil
	}

	timer := time.NewTimer(time.Duration(yieldMs) * time.Millisecond)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		_ = m.kill(sess.id)
		return execResult{}, ctx.Err()
	case <-sess.doneCh:
		out, code := sess.allOutput()
		_ = m.clearFinished(sess.id)
		return execResult{
			Status:   programStatusExited,
			Output:   out,
			ExitCode: intPtr(code),
		}, nil
	case <-timer.C:
		return execResult{
			Status:    programStatusRunning,
			SessionID: sess.id,
			Output:    sess.pollTail(defaultLogTail),
		}, nil
	}
}

func runForeground(
	ctx context.Context,
	params execParams,
	timeout time.Duration,
	baseEnv map[string]string,
) (string, int, error) {
	sess, err := startSession(
		"",
		params,
		timeout,
		baseEnv,
		0,
	)
	if err != nil {
		return "", 0, err
	}

	select {
	case <-ctx.Done():
		_ = sess.kill(context.Background(), defaultKillGrace)
		return "", 0, ctx.Err()
	case <-sess.doneCh:
	}

	out, code := sess.allOutput()
	return out, code, nil
}

func timeoutDuration(timeoutS int) time.Duration {
	if timeoutS <= 0 {
		timeoutS = defaultTimeoutS
	}
	if int64(timeoutS) > maxTimeoutSeconds {
		timeoutS = int(maxTimeoutSeconds)
	}
	return time.Duration(timeoutS) * time.Second
}

func shellCmd(
	_ context.Context,
	command string,
) (*exec.Cmd, error) {
	shell, args, err := shellSpec()
	if err != nil {
		return nil, err
	}
	// nosemgrep: go.lang.security.audit.dangerous-exec-command
	// hostexec intentionally executes trusted host commands.
	return exec.Command(
		shell,
		append(args, command)...,
	), nil //nolint:gosec
}

func shellSpec() (string, []string, error) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/d", "/s", "/c"}, nil
	}
	if path, err := exec.LookPath("bash"); err == nil {
		return path, []string{"-lc"}, nil
	}
	if path, err := exec.LookPath("sh"); err == nil {
		return path, []string{"-lc"}, nil
	}
	return "", nil, errors.New("bash or sh is required")
}

func mergedEnv(
	baseEnv map[string]string,
	extra map[string]string,
) []string {
	if len(baseEnv) == 0 && len(extra) == 0 {
		return nil
	}
	env := os.Environ()
	out := make([]string, 0, len(env)+len(baseEnv)+len(extra))
	out = append(out, env...)

	for key, value := range baseEnv {
		out = setEnv(out, key, value)
	}
	for key, value := range extra {
		out = setEnv(out, key, value)
	}
	return out
}

func setEnv(env []string, key string, value string) []string {
	prefix := fmt.Sprintf("%s=", key)
	for i := range env {
		if strings.HasPrefix(env[i], prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
		return exitErr.ProcessState.ExitCode()
	}
	return -1
}

func (m *manager) startBackground(
	params execParams,
	timeout time.Duration,
) (*session, error) {
	sess, err := startSession(
		newSessionID(),
		params,
		timeout,
		m.baseEnv,
		m.maxLines,
	)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.sessions[sess.id] = sess
	m.mu.Unlock()
	return sess, nil
}

func startSession(
	id string,
	params execParams,
	timeout time.Duration,
	baseEnv map[string]string,
	maxLines int,
) (*session, error) {
	runCtx, cancel := context.WithTimeout(
		context.Background(),
		timeout,
	)
	cmd, err := shellCmd(runCtx, params.Command)
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Dir = params.Workdir
	cmd.Env = mergedEnv(baseEnv, params.Env)

	sess := newSession(id, params.Command, maxLines)
	sess.cancel = cancel
	sess.cmd = cmd

	if params.Pty {
		master, closeIO, err := startPTY(cmd)
		if err != nil {
			cancel()
			return nil, err
		}
		sess.processGroupID = commandProcessGroupID(cmd)
		sess.stdin = master
		sess.closeIO = closeIO
		sess.ioWG.Add(1)
		go func() {
			defer sess.ioWG.Done()
			sess.readFrom(master)
		}()
	} else {
		stdin, stdout, stderr, err := startPipes(cmd)
		if err != nil {
			cancel()
			return nil, err
		}
		preparePipeCommand(cmd)
		sess.stdin = stdin
		sess.closeIO = func() error {
			_ = stdin.Close()
			_ = stdout.Close()
			_ = stderr.Close()
			return nil
		}
		if err := cmd.Start(); err != nil {
			cancel()
			_ = sess.closeIO()
			return nil, err
		}
		sess.processGroupID = commandProcessGroupID(cmd)
		sess.ioWG.Add(2)
		go func() {
			defer sess.ioWG.Done()
			sess.readFrom(stdout)
		}()
		go func() {
			defer sess.ioWG.Done()
			sess.readFrom(stderr)
		}()
	}

	go func() {
		sess.ioWG.Wait()
		close(sess.ioDone)
	}()

	go func() {
		// Use cmd.Process.Wait() instead of cmd.Wait() because
		// cmd.Wait() closes StdoutPipe/StderrPipe readers before
		// the readFrom goroutines are done consuming those pipes.
		processState, _ := cmd.Process.Wait()
		waitDone(sess.ioDone, defaultIODrain)
		_ = terminateProcessTree(
			context.Background(),
			cmd.Process,
			sess.processGroupID,
			defaultKillGrace,
		)
		code := -1
		if processState != nil {
			code = processState.ExitCode()
		}
		sess.markDone(code)
		cancel()
		_ = sess.closeIO()
	}()

	go func() {
		select {
		case <-sess.doneCh:
		case <-runCtx.Done():
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				_ = sess.kill(
					context.Background(),
					timeoutKillGrace,
				)
			}
		}
	}()

	return sess, nil
}

func waitDone(
	done <-chan struct{},
	timeout time.Duration,
) {
	if done == nil {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func startPipes(
	cmd *exec.Cmd,
) (io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, err
	}
	return stdin, stdout, stderr, nil
}

func (m *manager) poll(
	id string,
	limit *int,
) (processPoll, error) {
	sess, err := m.get(id)
	if err != nil {
		return processPoll{}, err
	}
	return sess.poll(limit), nil
}

func (m *manager) write(
	id string,
	data string,
	newline bool,
) error {
	sess, err := m.get(id)
	if err != nil {
		return err
	}
	return sess.write(data, newline)
}

func (m *manager) kill(id string) error {
	return m.killContext(context.Background(), id)
}

func (m *manager) killContext(
	ctx context.Context,
	id string,
) error {
	sess, err := m.get(id)
	if err != nil {
		return err
	}
	return sess.kill(ctx, defaultKillGrace)
}

func (m *manager) clearFinished(id string) error {
	sess, err := m.get(id)
	if err != nil {
		return err
	}
	if sess.running() {
		return errors.New("session is still running")
	}
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return nil
}

func (m *manager) get(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", errUnknownSession, id)
	}
	return sess, nil
}

func (m *manager) cleanupExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock()
	for id, sess := range m.sessions {
		if sess.running() {
			continue
		}
		if now.Sub(sess.doneAt()) < m.jobTTL {
			continue
		}
		delete(m.sessions, id)
	}
}

func (m *manager) close() error {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	var firstErr error
	for _, id := range ids {
		if err := m.kill(id); err != nil {
			if errors.Is(err, errUnknownSession) {
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
		}
		m.mu.Lock()
		delete(m.sessions, id)
		m.mu.Unlock()
	}
	return firstErr
}

func intPtr(value int) *int {
	return &value
}
