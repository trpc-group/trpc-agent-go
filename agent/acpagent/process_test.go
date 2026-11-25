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
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestAgentProcessLifecycle(t *testing.T) {
	var stderr bytes.Buffer
	acpAgent := newHelperAgent(t, "wait", &stderr)

	process, err := acpAgent.connections.connect(context.Background())
	require.NoError(t, err)
	require.NotNil(t, process)
	require.NotNil(t, process.connection)
	require.True(t, process.agentDescriptor.agentCapabilities.LoadSession)
	require.Len(t, process.agentDescriptor.authMethods, 2)
	require.Equal(t, acpsdk.AuthMethodId("agent-login"), process.agentDescriptor.authMethods[0].Agent.Id)
	require.Equal(t, acpsdk.AuthMethodId("terminal-login"), process.agentDescriptor.authMethods[1].Terminal.Id)
	require.Equal(t, "helper-agent", process.agentDescriptor.agentInfo.Name)
	require.Equal(t, "1.2.3", process.agentDescriptor.agentInfo.Version)
	require.Equal(t, testConnectionSnapshot{
		state:      lifecycleRunning,
		generation: 1,
		pid:        process.cmd.Process.Pid,
	}, snapshotConnectionManager(acpAgent.connections))

	again, err := acpAgent.connections.connect(context.Background())
	require.NoError(t, err)
	require.Same(t, process, again)

	require.NoError(t, acpAgent.Close())
	require.Eventually(t, func() bool {
		select {
		case <-process.done:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, testConnectionSnapshot{
		state:      lifecycleStopped,
		generation: 1,
	}, snapshotConnectionManager(acpAgent.connections))
	require.Contains(t, stderr.String(), "acp helper stderr")
}

func TestAgentCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	name, options := validACPOptions(t)
	options = append(options, WithTerminalAuthHandler(noopTerminalAuth))
	acpAgent, err := New(name, options...)
	require.NoError(t, err)
	require.NoError(t, acpAgent.Close())
	require.NoError(t, acpAgent.Close())

	_, err = acpAgent.connections.connect(context.Background())
	require.ErrorIs(t, err, errAgentClosed)
}

func TestAgentCloseReturnsStableError(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgent(t, "hang", io.Discard)
	acpAgent.connections.closeTimeout = 100 * time.Millisecond
	_, err := acpAgent.connections.connect(context.Background())
	require.NoError(t, err)

	first := acpAgent.Close()
	require.ErrorIs(t, first, context.DeadlineExceeded)
	second := acpAgent.Close()
	require.EqualError(t, second, first.Error())
}

func TestAgentProcessAbnormalExit(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgent(t, "exit_after_init", io.Discard)
	process, err := acpAgent.connections.connect(context.Background())
	require.NoError(t, err)
	<-process.done

	require.Equal(t, testConnectionSnapshot{
		state:      lifecycleStopped,
		generation: 1,
	}, snapshotConnectionManager(acpAgent.connections))
	runErr := acpAgent.connections.processError(process)
	require.Error(t, runErr)
	var exitErr *exec.ExitError
	require.ErrorAs(t, runErr, &exitErr)
	require.Equal(t, 7, exitErr.ExitCode())
	require.NoError(t, acpAgent.Close())
}

func TestAgentProcessStartFailureKeepsManagerStopped(t *testing.T) {
	t.Parallel()

	name, options := validACPOptions(t)
	options = append(
		options,
		WithCommand(t.TempDir()+"/missing-agent"),
		WithTerminalAuthHandler(noopTerminalAuth),
	)
	acpAgent, err := New(name, options...)
	require.NoError(t, err)

	process, err := acpAgent.connections.connect(context.Background())
	require.Nil(t, process)
	require.ErrorContains(t, err, "start agent process")
	require.Equal(t, testConnectionSnapshot{
		state: lifecycleStopped,
	}, snapshotConnectionManager(acpAgent.connections))
	require.NoError(t, acpAgent.Close())
}

func TestAgentProcessRestartIncrementsGeneration(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgent(t, "exit_after_init", io.Discard)
	first, err := acpAgent.connections.connect(context.Background())
	require.NoError(t, err)
	<-first.done

	second, err := acpAgent.connections.connect(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(2), second.generation)
	require.Equal(t, uint64(2), snapshotConnectionManager(acpAgent.connections).generation)
	<-second.done
	require.NoError(t, acpAgent.Close())
}

func TestAgentConcurrentConnectSharesProcess(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgent(t, "wait", io.Discard)
	const callers = 8
	processes := make([]*processConnection, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			processes[index], errs[index] = acpAgent.connections.connect(context.Background())
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	for i := 1; i < callers; i++ {
		require.Same(t, processes[0], processes[i])
	}
	require.NoError(t, acpAgent.Close())
}

func TestAgentConcurrentConnectAndClose(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgent(t, "wait", io.Discard)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, _ = acpAgent.connections.connect(context.Background())
	}()
	go func() {
		defer wg.Done()
		<-start
		_ = acpAgent.Close()
	}()
	close(start)

	waited := make(chan struct{})
	go func() {
		wg.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent connect and close did not finish")
	}
	require.Equal(t, lifecycleStopped, snapshotConnectionManager(acpAgent.connections).state)
}

type testConnectionSnapshot struct {
	state      lifecycleState
	generation uint64
	pid        int
}

func snapshotConnectionManager(manager *connectionManager) testConnectionSnapshot {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	snapshot := testConnectionSnapshot{
		state:      manager.state,
		generation: manager.generation,
	}
	if manager.current != nil && manager.current.cmd.Process != nil {
		snapshot.pid = manager.current.cmd.Process.Pid
	}
	return snapshot
}

func newHelperAgent(t *testing.T, mode string, stderr io.Writer) *ACPAgent {
	t.Helper()
	return newHelperAgentForTerminalAuth(t, mode, stderr, true)
}

func newHelperAgentForTerminalAuth(
	t *testing.T,
	mode string,
	stderr io.Writer,
	terminalAuth bool,
) *ACPAgent {
	t.Helper()
	return newHelperAgentWithOptions(t, mode, stderr, terminalAuth)
}

func newHelperAgentWithOptions(
	t *testing.T,
	mode string,
	stderr io.Writer,
	terminalAuth bool,
	extraOptions ...Option,
) *ACPAgent {
	t.Helper()
	return newHelperAgentWithConfig(
		t,
		mode,
		stderr,
		terminalAuth,
		nil,
		extraOptions...,
	)
}

func newHelperAgentWithConfig(
	t *testing.T,
	mode string,
	stderr io.Writer,
	terminalAuth bool,
	configure func(*processConfig),
	extraOptions ...Option,
) *ACPAgent {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	terminalAuthValue := "0"
	if terminalAuth {
		terminalAuthValue = "1"
	}
	config := processConfig{
		Name:    "acp-helper",
		Command: executable,
		Args:    []string{"-test.run=^TestACPHelperProcess$"},
		Env: []string{
			"GO_WANT_ACP_HELPER=1",
			"ACP_HELPER_MODE=" + mode,
			"ACP_HELPER_EXPECT_TERMINAL_AUTH=" + terminalAuthValue,
		},
		WorkDir: t.TempDir(),
	}
	if configure != nil {
		configure(&config)
	}
	options := []Option{
		WithDescription(config.Description),
		WithCommand(config.Command),
		WithArgs(config.Args...),
		WithEnv(config.Env...),
		WithWorkDir(config.WorkDir),
		WithIdleTimeout(config.IdleTimeout),
		WithStderrWriter(stderr),
	}
	if terminalAuth {
		options = append(options, WithTerminalAuthHandler(noopTerminalAuth))
	}
	options = append(options, extraOptions...)
	acpAgent, err := New(config.Name, options...)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = acpAgent.Close()
	})
	return acpAgent
}
