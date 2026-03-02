//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package octool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	defaultYieldMs   = 10_000
	defaultTimeoutS  = 1_800
	defaultLogTail   = 40
	defaultLogLimit  = 200
	defaultMaxLines  = 20_000
	defaultJobTTL    = 30 * time.Minute
	defaultKillGrace = 2 * time.Second
	defaultIODrain   = 1 * time.Second
)

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session

	maxLines int
	jobTTL   time.Duration

	clock func() time.Time
}

type Option func(*Manager)

func WithMaxLines(n int) Option {
	return func(m *Manager) {
		if n > 0 {
			m.maxLines = n
		}
	}
}

func WithJobTTL(d time.Duration) Option {
	return func(m *Manager) {
		if d > 0 {
			m.jobTTL = d
		}
	}
}

func NewManager(opts ...Option) *Manager {
	m := &Manager{
		sessions: map[string]*session{},
		maxLines: defaultMaxLines,
		jobTTL:   defaultJobTTL,
		clock:    time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
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
	Status    string `json:"status"`
	Output    string `json:"output,omitempty"`
	ExitCode  int    `json:"exitCode,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

func (m *Manager) Exec(
	ctx context.Context,
	params execParams,
) (execResult, error) {
	if ctx == nil {
		return execResult{}, errors.New("nil context")
	}
	if params.Command == "" {
		return execResult{}, errors.New("command is required")
	}

	m.cleanupExpired()

	yieldMs := defaultYieldMs
	if params.YieldMs != nil && *params.YieldMs >= 0 {
		yieldMs = *params.YieldMs
	}

	timeoutS := defaultTimeoutS
	if params.TimeoutS != nil && *params.TimeoutS > 0 {
		timeoutS = *params.TimeoutS
	}

	timeout := time.Duration(timeoutS) * time.Second

	if !params.Background && yieldMs == 0 && !params.Pty {
		out, code, err := runForeground(ctx, params, timeout)
		if err != nil {
			return execResult{}, err
		}
		return execResult{
			Status:   "exited",
			Output:   out,
			ExitCode: code,
		}, nil
	}

	sess, err := m.startBackground(params, timeout)
	if err != nil {
		return execResult{}, err
	}

	if params.Background {
		return execResult{
			Status:    "running",
			SessionID: sess.id,
			Output:    sess.tail(defaultLogTail),
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
			Status:   "exited",
			Output:   out,
			ExitCode: code,
		}, nil
	}

	yield := time.Duration(yieldMs) * time.Millisecond
	timer := time.NewTimer(yield)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		_ = m.kill(sess.id)
		return execResult{}, ctx.Err()
	case <-sess.doneCh:
		out, code := sess.allOutput()
		_ = m.clearFinished(sess.id)
		return execResult{
			Status:   "exited",
			Output:   out,
			ExitCode: code,
		}, nil
	case <-timer.C:
		return execResult{
			Status:    "running",
			SessionID: sess.id,
			Output:    sess.tail(defaultLogTail),
		}, nil
	}
}

func runForeground(
	ctx context.Context,
	params execParams,
	timeout time.Duration,
) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := shellCmd(ctx, params.Command)
	cmd.Dir = params.Workdir
	cmd.Env = mergedEnv(params.Env)

	out, err := cmd.CombinedOutput()
	code := exitCode(err)
	return string(out), code, nil
}

func shellCmd(ctx context.Context, command string) *exec.Cmd {
	const shell = "bash"
	const flag = "-lc"
	return exec.CommandContext(ctx, shell, flag, command)
}

func mergedEnv(extra map[string]string) []string {
	if len(extra) == 0 {
		return nil
	}
	base := os.Environ()
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)

	for k, v := range extra {
		out = setEnv(out, k, v)
	}
	return out
}

func setEnv(env []string, k, v string) []string {
	prefix := fmt.Sprintf("%s=", k)
	for i := range env {
		if strings.HasPrefix(env[i], prefix) {
			env[i] = prefix + v
			return env
		}
	}
	return append(env, prefix+v)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ProcessState != nil {
		return ee.ProcessState.ExitCode()
	}
	return -1
}

func (m *Manager) startBackground(
	params execParams,
	timeout time.Duration,
) (*session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := shellCmd(ctx, params.Command)
	cmd.Dir = params.Workdir
	cmd.Env = mergedEnv(params.Env)

	sess := newSession(newSessionID(), params.Command, m.maxLines)
	sess.cancel = cancel

	if params.Pty {
		master, closeIO, err := startPTY(cmd)
		if err != nil {
			cancel()
			return nil, err
		}
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
		sess.stdin = stdin
		sess.closeIO = func() error {
			_ = stdin.Close()
			_ = stdout.Close()
			_ = stderr.Close()
			return nil
		}
		sess.ioWG.Add(2)
		go func() {
			defer sess.ioWG.Done()
			sess.readFrom(stdout)
		}()
		go func() {
			defer sess.ioWG.Done()
			sess.readFrom(stderr)
		}()
		if err := cmd.Start(); err != nil {
			cancel()
			_ = sess.closeIO()
			return nil, err
		}
	}

	go func() {
		sess.ioWG.Wait()
		close(sess.ioDone)
	}()

	sess.cmd = cmd
	m.mu.Lock()
	m.sessions[sess.id] = sess
	m.mu.Unlock()

	go func() {
		err := cmd.Wait()
		waitDone(sess.ioDone, defaultIODrain)
		sess.markDone(exitCode(err))
		cancel()
		_ = sess.closeIO()
	}()

	return sess, nil
}

func waitDone(done <-chan struct{}, timeout time.Duration) {
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

func (m *Manager) list() []processSession {
	m.cleanupExpired()

	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]processSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.snapshot())
	}
	sortSessions(out)
	return out
}

func (m *Manager) poll(id string, limit *int) (processPoll, error) {
	s, err := m.get(id)
	if err != nil {
		return processPoll{}, err
	}
	return s.poll(limit), nil
}

func (m *Manager) log(
	id string,
	offset *int,
	limit *int,
) (processLog, error) {
	s, err := m.get(id)
	if err != nil {
		return processLog{}, err
	}
	return s.log(offset, limit), nil
}

func (m *Manager) write(
	id string,
	data string,
	newline bool,
) (processWrite, error) {
	s, err := m.get(id)
	if err != nil {
		return processWrite{}, err
	}
	return s.write(data, newline)
}

func (m *Manager) kill(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	return s.kill(defaultKillGrace)
}

func (m *Manager) clearFinished(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if s.running() {
		return errors.New("session is still running")
	}
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return nil
}

func (m *Manager) remove(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if s.running() {
		if err := s.kill(defaultKillGrace); err != nil {
			return err
		}
	}
	return m.clearFinished(id)
}

func (m *Manager) get(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("unknown sessionId: %s", id)
	}
	return s, nil
}

func (m *Manager) cleanupExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock()
	for id, s := range m.sessions {
		if s.running() {
			continue
		}
		if now.Sub(s.doneAt()) < m.jobTTL {
			continue
		}
		delete(m.sessions, id)
	}
}
