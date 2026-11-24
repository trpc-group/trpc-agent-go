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
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

type terminalAuthHandlerFunc func(context.Context, TerminalAuthRequest) error

func (f terminalAuthHandlerFunc) RunTerminalAuth(
	ctx context.Context,
	request TerminalAuthRequest,
) error {
	return f(ctx, request)
}

func TestNewValidatesACPConfig(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	valid := ACPConfig{
		Name:    "acp-test",
		Command: "agent",
		WorkDir: workDir,
	}
	tests := []struct {
		name   string
		mutate func(*ACPConfig)
		want   string
	}{
		{
			name: "empty name",
			mutate: func(config *ACPConfig) {
				config.Name = " "
			},
			want: "name is required",
		},
		{
			name: "empty command",
			mutate: func(config *ACPConfig) {
				config.Command = ""
			},
			want: "command is required",
		},
		{
			name: "empty work directory",
			mutate: func(config *ACPConfig) {
				config.WorkDir = ""
			},
			want: "work directory is required",
		},
		{
			name: "command with NUL",
			mutate: func(config *ACPConfig) {
				config.Command = "agent\x00"
			},
			want: "command contains a NUL byte",
		},
		{
			name: "relative work directory",
			mutate: func(config *ACPConfig) {
				config.WorkDir = filepath.Join(".", "relative")
			},
			want: "work directory must be absolute",
		},
		{
			name: "work directory with NUL",
			mutate: func(config *ACPConfig) {
				config.WorkDir = workDir + "\x00"
			},
			want: "work directory contains a NUL byte",
		},
		{
			name: "negative idle timeout",
			mutate: func(config *ACPConfig) {
				config.IdleTimeout = -time.Second
			},
			want: "idle timeout must not be negative",
		},
		{
			name: "env without equals",
			mutate: func(config *ACPConfig) {
				config.Env = []string{"INVALID"}
			},
			want: "must use KEY=VALUE",
		},
		{
			name: "env with empty key",
			mutate: func(config *ACPConfig) {
				config.Env = []string{"=value"}
			},
			want: "empty key",
		},
		{
			name: "env with NUL",
			mutate: func(config *ACPConfig) {
				config.Env = []string{"KEY=value\x00"}
			},
			want: "NUL byte",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := valid
			test.mutate(&config)
			_, err := New(config, WithTerminalAuthHandler(noopTerminalAuth))
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestNewAllowsMissingTerminalAuthHandler(t *testing.T) {
	t.Parallel()

	acpAgent, err := New(validACPConfig(t))
	require.NoError(t, err)
	require.Nil(t, acpAgent.terminalAuthHandler)
	require.NoError(t, acpAgent.Close())

	acpAgent, err = New(validACPConfig(t), WithTerminalAuthHandler(nil))
	require.NoError(t, err)
	require.Nil(t, acpAgent.terminalAuthHandler)
	require.NoError(t, acpAgent.Close())
}

func TestNewDoesNotStartProcessAndCopiesSlices(t *testing.T) {
	t.Parallel()

	config := validACPConfig(t)
	config.Args = []string{"--mode", "test"}
	config.Env = []string{"ACP_TEST=1"}
	config.Description = "test agent"
	config.IdleTimeout = time.Second
	acpAgent, err := New(
		config,
		WithTerminalAuthHandler(noopTerminalAuth),
		WithStderrWriter(nil),
	)
	require.NoError(t, err)
	defer require.NoError(t, acpAgent.Close())

	config.Args[0] = "mutated"
	config.Env[0] = "ACP_TEST=mutated"
	require.Equal(t, []string{"--mode", "test"}, acpAgent.config.Args)
	require.Equal(t, []string{"ACP_TEST=1"}, acpAgent.config.Env)
	require.Equal(t, time.Second, acpAgent.config.IdleTimeout)
	require.Equal(t, testConnectionSnapshot{
		state: lifecycleStopped,
	}, snapshotConnectionManager(acpAgent.connections))
	require.Equal(t, agent.Info{
		Name:        config.Name,
		Description: config.Description,
	}, acpAgent.Info())
	require.Nil(t, acpAgent.Tools())
	require.Nil(t, acpAgent.SubAgents())
	require.Nil(t, acpAgent.FindSubAgent("missing"))
}

func TestRunValidatesInvocation(t *testing.T) {
	t.Parallel()

	acpAgent, err := New(validACPConfig(t), WithTerminalAuthHandler(noopTerminalAuth))
	require.NoError(t, err)
	defer require.NoError(t, acpAgent.Close())

	events, err := acpAgent.Run(context.Background(), nil)
	require.Nil(t, events)
	require.EqualError(t, err, "acpagent: invocation is nil")

	events, err = acpAgent.Run(context.Background(), &agent.Invocation{})
	require.Nil(t, events)
	require.ErrorIs(t, err, errRunNotImplemented)
}

func TestMergeEnvironment(t *testing.T) {
	t.Parallel()

	got := mergeEnvironment(
		[]string{"A=1", "B=2"},
		[]string{"A=3", "C=4", "B=5"},
	)
	require.Equal(t, []string{"A=3", "B=5", "C=4"}, got)
}

func validACPConfig(t *testing.T) ACPConfig {
	t.Helper()
	return ACPConfig{
		Name:    "acp-test",
		Command: "agent",
		WorkDir: t.TempDir(),
	}
}

var noopTerminalAuth TerminalAuthHandler = terminalAuthHandlerFunc(
	func(context.Context, TerminalAuthRequest) error {
		return nil
	},
)
