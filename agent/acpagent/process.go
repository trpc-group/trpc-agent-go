//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

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

	acpsdk "github.com/coder/acp-go-sdk"
	"golang.org/x/sync/singleflight"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

const defaultCloseTimeout = 5 * time.Second

var (
	errAgentClosed = errors.New("acpagent: agent is closed")
	errProcessDone = errors.New("acpagent: process is no longer running")
)

type lifecycleState uint8

const (
	lifecycleStopped lifecycleState = iota
	lifecycleStarting
	lifecycleRunning
	lifecycleClosing
)

type processConnection struct {
	generation      uint64
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	connection      *acpsdk.ClientSideConnection
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	waitErr         error
	stderr          *processStderr
	startedAt       time.Time
	agentDescriptor acpAgentDescriptor
	authGroup       singleflight.Group
}

type connectionManager struct {
	config       processConfig
	stderr       io.Writer
	closeTimeout time.Duration
	activeTurns  *activeTurnRegistry
	sessions     *acpSessionStore
	authSelector AuthMethodSelector
	terminalAuth bool
	idleTimeout  time.Duration

	mu         sync.Mutex
	startMu    sync.Mutex
	state      lifecycleState
	current    *processConnection
	generation uint64
	closed     bool
	closeOnce  sync.Once
	closeErr   error

	activityMu         sync.Mutex
	activeOperations   int
	activityGeneration uint64
	idleTimer          *time.Timer
	activityClosed     bool
}

func newConnectionManager(
	config processConfig,
	stderr io.Writer,
	activeTurns *activeTurnRegistry,
	sessions *acpSessionStore,
	authSelector AuthMethodSelector,
	terminalAuth bool,
) *connectionManager {
	return &connectionManager{
		config:       config,
		stderr:       stderr,
		closeTimeout: defaultCloseTimeout,
		activeTurns:  activeTurns,
		sessions:     sessions,
		authSelector: authSelector,
		terminalAuth: terminalAuth,
		idleTimeout:  config.IdleTimeout,
	}
}

func (m *connectionManager) connect(ctx context.Context) (*processConnection, error) {
	m.startMu.Lock()
	defer m.startMu.Unlock()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errAgentClosed
	}
	if m.current != nil {
		current := m.current
		m.mu.Unlock()
		debugContext(
			ctx,
			"acpagent: reusing process",
			append(processLogFields(current), "agent", m.config.Name)...,
		)
		return current, nil
	}
	m.state = lifecycleStarting
	m.mu.Unlock()
	debugContext(
		ctx,
		"acpagent: starting connection",
		"agent", m.config.Name,
	)

	process, err := m.startProcess()
	if err != nil {
		m.mu.Lock()
		m.state = lifecycleStopped
		m.mu.Unlock()
		return nil, err
	}
	if err := m.initialize(ctx, process); err != nil {
		m.stopStartedProcess(process)
		m.mu.Lock()
		m.state = lifecycleStopped
		m.mu.Unlock()
		return nil, process.withStderr(err)
	}

	m.mu.Lock()
	if m.closed {
		m.state = lifecycleClosing
		m.mu.Unlock()
		m.stopStartedProcess(process)
		return nil, errAgentClosed
	}
	m.generation++
	process.generation = m.generation
	m.current = process
	m.state = lifecycleRunning
	m.mu.Unlock()
	infoContext(
		ctx,
		"acpagent: process ready",
		append(processLogFields(process), "agent", m.config.Name)...,
	)

	go m.wait(process)
	return process, nil
}

func (m *connectionManager) startProcess() (*processConnection, error) {
	log.Debug(
		"acpagent: starting process",
		"agent", m.config.Name,
		"command", m.config.Command,
		"work_dir", m.config.WorkDir,
		"arg_count", len(m.config.Args),
		"env_count", len(m.config.Env),
	)
	processCtx, cancel := context.WithCancel(context.Background())
	//nolint:gosec // The executable is explicit ACP agent configuration.
	cmd := exec.CommandContext(processCtx, m.config.Command, m.config.Args...)
	cmd.Dir = m.config.WorkDir
	cmd.Env = mergeEnvironment(os.Environ(), m.config.Env)
	var stderr *processStderr
	if m.stderr != nil {
		stderr = newProcessStderr(m.config.Name, m.stderr)
		cmd.Stderr = stderr
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		err = fmt.Errorf("acpagent: create process stdin: %w", err)
		log.Error("acpagent: failed to create process stdin", "agent", m.config.Name, "error", err)
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		cancel()
		err = fmt.Errorf("acpagent: create process stdout: %w", err)
		log.Error("acpagent: failed to create process stdout", "agent", m.config.Name, "error", err)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		cancel()
		err = fmt.Errorf("acpagent: start agent process: %w", err)
		log.Error("acpagent: failed to start process", "agent", m.config.Name, "error", err)
		return nil, err
	}
	log.Info(
		"acpagent: process started",
		"agent", m.config.Name,
		"pid", cmd.Process.Pid,
	)
	return &processConnection{
		cmd:   cmd,
		stdin: stdin,
		connection: acpsdk.NewClientSideConnection(
			&protocolClient{
				activeTurns: m.activeTurns,
				sessions:    m.sessions,
			},
			stdin,
			stdout,
		),
		ctx:       processCtx,
		cancel:    cancel,
		done:      make(chan struct{}),
		stderr:    stderr,
		startedAt: time.Now(),
	}, nil
}

func (m *connectionManager) wait(process *processConnection) {
	err := process.cmd.Wait()
	process.flushStderr()
	process.cancel()

	m.mu.Lock()
	process.waitErr = err
	var (
		failureErr error
		duration   time.Duration
		unexpected bool
	)
	if m.current == process {
		unexpected = m.state == lifecycleRunning
		failureErr = process.failureError()
		duration = time.Since(process.startedAt)
		m.activeTurns.failAll(failureErr)
		m.current = nil
		m.state = lifecycleStopped
	}
	m.mu.Unlock()
	close(process.done)
	if failureErr == nil {
		return
	}
	fields := append(
		processLogFields(process),
		"agent", m.config.Name,
		"duration", duration,
		"error", failureErr,
	)
	if unexpected {
		errorContext(context.Background(), "acpagent: process exited unexpectedly", fields...)
		return
	}
	debugContext(context.Background(), "acpagent: process exited after shutdown", fields...)
}

func (m *connectionManager) stopStartedProcess(process *processConnection) {
	_ = process.stdin.Close()
	process.cancel()
	process.waitErr = process.cmd.Wait()
	process.flushStderr()
	close(process.done)
}

func (m *connectionManager) close() error {
	m.closeOnce.Do(func() {
		debugContext(context.Background(), "acpagent: closing connection", "agent", m.config.Name)
		m.stopIdleTimer()
		m.startMu.Lock()
		m.mu.Lock()
		m.closed = true
		process := m.current
		if process != nil {
			m.state = lifecycleClosing
		}
		m.mu.Unlock()
		m.startMu.Unlock()

		if process == nil {
			m.mu.Lock()
			m.state = lifecycleStopped
			m.mu.Unlock()
			debugContext(context.Background(), "acpagent: closed without a running process", "agent", m.config.Name)
			return
		}
		m.closeErr = m.closeProcess(process)
		m.mu.Lock()
		if m.current == process {
			m.current = nil
		}
		m.state = lifecycleStopped
		m.mu.Unlock()
		if m.closeErr != nil {
			warnContext(
				context.Background(),
				"acpagent: close process failed",
				append(processLogFields(process), "agent", m.config.Name, "error", m.closeErr)...,
			)
		} else {
			debugContext(
				context.Background(),
				"acpagent: closed process",
				append(processLogFields(process), "agent", m.config.Name)...,
			)
		}
	})
	return m.closeErr
}

func (m *connectionManager) beginActivity() func() {
	m.activityMu.Lock()
	m.activityGeneration++
	m.activeOperations++
	m.stopIdleTimerLocked()
	m.activityMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			m.activityMu.Lock()
			if m.activeOperations > 0 {
				m.activeOperations--
			}
			if m.activeOperations == 0 {
				m.scheduleIdleLocked()
			}
			m.activityMu.Unlock()
		})
	}
}

func (m *connectionManager) scheduleIdleLocked() {
	if m.activityClosed || m.idleTimeout <= 0 || m.idleTimer != nil {
		return
	}
	m.activityGeneration++
	generation := m.activityGeneration
	m.idleTimer = time.AfterFunc(m.idleTimeout, func() {
		m.reclaimIdleProcess(generation)
	})
}

func (m *connectionManager) stopIdleTimer() {
	m.activityMu.Lock()
	m.activityGeneration++
	m.activityClosed = true
	m.stopIdleTimerLocked()
	m.activityMu.Unlock()
}

func (m *connectionManager) stopIdleTimerLocked() {
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
}

func (m *connectionManager) reclaimIdleProcess(generation uint64) {
	m.activityMu.Lock()
	if m.activityClosed ||
		m.activityGeneration != generation ||
		m.activeOperations > 0 {
		m.activityMu.Unlock()
		return
	}
	m.idleTimer = nil
	defer m.activityMu.Unlock()

	m.startMu.Lock()
	defer m.startMu.Unlock()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	process := m.current
	if process == nil {
		m.mu.Unlock()
		return
	}
	m.state = lifecycleClosing
	m.mu.Unlock()
	infoContext(
		context.Background(),
		"acpagent: reclaiming idle process",
		append(
			processLogFields(process),
			"agent", m.config.Name,
			"idle_timeout", m.idleTimeout,
		)...,
	)

	closeErr := m.closeProcess(process)
	m.mu.Lock()
	if m.current == process {
		m.current = nil
	}
	m.state = lifecycleStopped
	m.mu.Unlock()
	if closeErr != nil {
		warnContext(
			context.Background(),
			"acpagent: idle process close failed",
			append(processLogFields(process), "agent", m.config.Name, "error", closeErr)...,
		)
		return
	}
	infoContext(
		context.Background(),
		"acpagent: reclaimed idle process",
		append(processLogFields(process), "agent", m.config.Name)...,
	)
}

func (m *connectionManager) closeProcess(process *processConnection) error {
	stdinErr := process.stdin.Close()
	if errors.Is(stdinErr, os.ErrClosed) || errors.Is(stdinErr, io.ErrClosedPipe) {
		stdinErr = nil
	}
	timer := time.NewTimer(m.closeTimeout)
	defer timer.Stop()

	select {
	case <-process.done:
		return errors.Join(stdinErr, process.waitErr)
	case <-timer.C:
		process.cancel()
		<-process.done
		return errors.Join(
			stdinErr,
			fmt.Errorf("acpagent: close process: %w", context.DeadlineExceeded),
			process.waitErr,
		)
	}
}

func (m *connectionManager) processError(process *processConnection) error {
	if process == nil {
		return errProcessDone
	}
	select {
	case <-process.done:
		return process.failureError()
	default:
		return nil
	}
}

func (p *processConnection) failureWithin(timeout time.Duration) error {
	if p == nil {
		return errProcessDone
	}
	select {
	case <-p.done:
		return p.failureError()
	case <-time.After(timeout):
		return nil
	}
}

func mergeEnvironment(base, overrides []string) []string {
	merged := append([]string(nil), base...)
	indexes := make(map[string]int, len(merged))
	for i, entry := range merged {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			indexes[key] = i
		}
	}
	for _, entry := range overrides {
		key, _, _ := strings.Cut(entry, "=")
		if index, ok := indexes[key]; ok {
			merged[index] = entry
			continue
		}
		indexes[key] = len(merged)
		merged = append(merged, entry)
	}
	return merged
}

func (p *processConnection) flushStderr() {
	if p == nil || p.stderr == nil {
		return
	}
	p.stderr.Flush()
}

func (p *processConnection) stderrTail() string {
	if p == nil || p.stderr == nil {
		return ""
	}
	return p.stderr.Tail()
}

func (p *processConnection) failureError() error {
	if p == nil {
		return errProcessDone
	}
	return processFailureError(p.waitErr, p.stderrTail())
}

func (p *processConnection) withStderr(err error) error {
	if err == nil {
		return nil
	}
	stderr := p.stderrTail()
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w\nacpagent stderr:\n%s", err, stderr)
}

func processFailureError(err error, stderr string) error {
	var failure error
	if err == nil {
		failure = errors.New("acpagent: process exited")
	} else {
		failure = fmt.Errorf("acpagent: process exited: %w", err)
	}
	if stderr == "" {
		return failure
	}
	return fmt.Errorf("%w\nacpagent stderr:\n%s", failure, stderr)
}
