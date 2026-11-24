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
	cancel          context.CancelFunc
	done            chan struct{}
	waitErr         error
	agentDescriptor acpAgentDescriptor
}

type connectionManager struct {
	config       ACPConfig
	stderr       io.Writer
	closeTimeout time.Duration
	activeTurns  *activeTurnRegistry
	terminalAuth bool

	mu         sync.Mutex
	startMu    sync.Mutex
	state      lifecycleState
	current    *processConnection
	generation uint64
	closed     bool
	closeOnce  sync.Once
	closeErr   error
}

func newConnectionManager(
	config ACPConfig,
	stderr io.Writer,
	activeTurns *activeTurnRegistry,
	terminalAuth bool,
) *connectionManager {
	return &connectionManager{
		config:       config,
		stderr:       stderr,
		closeTimeout: defaultCloseTimeout,
		activeTurns:  activeTurns,
		terminalAuth: terminalAuth,
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
		return current, nil
	}
	m.state = lifecycleStarting
	m.mu.Unlock()

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
		return nil, err
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

	go m.wait(process)
	return process, nil
}

func (m *connectionManager) startProcess() (*processConnection, error) {
	processCtx, cancel := context.WithCancel(context.Background())
	//nolint:gosec // The executable is explicit ACP agent configuration.
	cmd := exec.CommandContext(processCtx, m.config.Command, m.config.Args...)
	cmd.Dir = m.config.WorkDir
	cmd.Env = mergeEnvironment(os.Environ(), m.config.Env)
	cmd.Stderr = m.stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("acpagent: create process stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		cancel()
		return nil, fmt.Errorf("acpagent: create process stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		cancel()
		return nil, fmt.Errorf("acpagent: start agent process: %w", err)
	}
	return &processConnection{
		cmd:   cmd,
		stdin: stdin,
		connection: acpsdk.NewClientSideConnection(
			&protocolClient{activeTurns: m.activeTurns},
			stdin,
			stdout,
		),
		cancel: cancel,
		done:   make(chan struct{}),
	}, nil
}

func (m *connectionManager) wait(process *processConnection) {
	err := process.cmd.Wait()
	process.cancel()

	m.mu.Lock()
	process.waitErr = err
	if m.current == process {
		m.current = nil
		m.state = lifecycleStopped
	}
	m.mu.Unlock()
	close(process.done)
}

func (m *connectionManager) stopStartedProcess(process *processConnection) {
	_ = process.stdin.Close()
	process.cancel()
	process.waitErr = process.cmd.Wait()
	close(process.done)
}

func (m *connectionManager) close() error {
	m.closeOnce.Do(func() {
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
			return
		}
		m.closeErr = m.closeProcess(process)
		m.mu.Lock()
		if m.current == process {
			m.current = nil
		}
		m.state = lifecycleStopped
		m.mu.Unlock()
	})
	return m.closeErr
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
		m.mu.Lock()
		defer m.mu.Unlock()
		return process.waitErr
	default:
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
