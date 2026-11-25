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

func TestNewValidatesOptions(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	tests := []struct {
		name      string
		agentName string
		options   []Option
		want      string
	}{
		{
			name:      "empty name",
			agentName: " ",
			options: []Option{
				WithCommand("agent"),
				WithWorkDir(workDir),
			},
			want: "name is required",
		},
		{
			name: "empty command",
			options: []Option{
				WithWorkDir(workDir),
			},
			want: "command is required",
		},
		{
			name: "empty work directory",
			options: []Option{
				WithCommand("agent"),
			},
			want: "work directory is required",
		},
		{
			name: "command with NUL",
			options: []Option{
				WithCommand("agent\x00"),
				WithWorkDir(workDir),
			},
			want: "command contains a NUL byte",
		},
		{
			name: "relative work directory",
			options: []Option{
				WithCommand("agent"),
				WithWorkDir(filepath.Join(".", "relative")),
			},
			want: "work directory must be absolute",
		},
		{
			name: "work directory with NUL",
			options: []Option{
				WithCommand("agent"),
				WithWorkDir(workDir + "\x00"),
			},
			want: "work directory contains a NUL byte",
		},
		{
			name: "negative idle timeout",
			options: []Option{
				WithCommand("agent"),
				WithWorkDir(workDir),
				WithIdleTimeout(-time.Second),
			},
			want: "idle timeout must not be negative",
		},
		{
			name: "env without equals",
			options: []Option{
				WithCommand("agent"),
				WithWorkDir(workDir),
				WithEnv("INVALID"),
			},
			want: "must use KEY=VALUE",
		},
		{
			name: "env with empty key",
			options: []Option{
				WithCommand("agent"),
				WithWorkDir(workDir),
				WithEnv("=value"),
			},
			want: "empty key",
		},
		{
			name: "env with NUL",
			options: []Option{
				WithCommand("agent"),
				WithWorkDir(workDir),
				WithEnv("KEY=value\x00"),
			},
			want: "NUL byte",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			agentName := test.agentName
			if agentName == "" {
				agentName = "acp-test"
			}
			options := append(
				[]Option{WithTerminalAuthHandler(noopTerminalAuth)},
				test.options...,
			)
			_, err := New(agentName, options...)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestNewAllowsMissingTerminalAuthHandler(t *testing.T) {
	t.Parallel()

	name, options := validACPOptions(t)
	acpAgent, err := New(name, options...)
	require.NoError(t, err)
	require.Nil(t, acpAgent.terminalAuthHandler)
	require.NoError(t, acpAgent.Close())

	options = append(options, WithTerminalAuthHandler(nil))
	acpAgent, err = New(name, options...)
	require.NoError(t, err)
	require.Nil(t, acpAgent.terminalAuthHandler)
	require.NoError(t, acpAgent.Close())
}

func TestNewDoesNotStartProcessAndCopiesSlices(t *testing.T) {
	t.Parallel()

	name, options := validACPOptions(t)
	args := []string{"--mode", "test"}
	env := []string{"ACP_TEST=1"}
	options = append(
		options,
		WithDescription("test agent"),
		WithArgs(args...),
		WithEnv(env...),
		WithIdleTimeout(time.Second),
		WithTerminalAuthHandler(noopTerminalAuth),
		WithStderrWriter(nil),
	)
	acpAgent, err := New(
		name,
		options...,
	)
	require.NoError(t, err)
	defer require.NoError(t, acpAgent.Close())

	args[0] = "mutated"
	env[0] = "ACP_TEST=mutated"
	require.Equal(t, []string{"--mode", "test"}, acpAgent.config.Args)
	require.Equal(t, []string{"ACP_TEST=1"}, acpAgent.config.Env)
	require.Equal(t, time.Second, acpAgent.config.IdleTimeout)
	require.Equal(t, testConnectionSnapshot{
		state: lifecycleStopped,
	}, snapshotConnectionManager(acpAgent.connections))
	require.Equal(t, agent.Info{
		Name:        name,
		Description: "test agent",
	}, acpAgent.Info())
	require.Nil(t, acpAgent.Tools())
	require.Nil(t, acpAgent.SubAgents())
	require.Nil(t, acpAgent.FindSubAgent("missing"))
}

func TestRunValidatesInvocation(t *testing.T) {
	t.Parallel()

	name, options := validACPOptions(t)
	options = append(options, WithTerminalAuthHandler(noopTerminalAuth))
	acpAgent, err := New(name, options...)
	require.NoError(t, err)
	defer require.NoError(t, acpAgent.Close())

	events, err := acpAgent.Run(context.Background(), nil)
	require.Nil(t, events)
	require.EqualError(t, err, "acpagent: invocation is nil")

	events, err = acpAgent.Run(context.Background(), &agent.Invocation{})
	require.Nil(t, events)
	require.ErrorContains(t, err, "prompt is empty")
}

func TestMergeEnvironment(t *testing.T) {
	t.Parallel()

	got := mergeEnvironment(
		[]string{"A=1", "B=2"},
		[]string{"A=3", "C=4", "B=5"},
	)
	require.Equal(t, []string{"A=3", "B=5", "C=4"}, got)
}

func validACPOptions(t *testing.T) (string, []Option) {
	t.Helper()
	return "acp-test", []Option{
		WithCommand("agent"),
		WithWorkDir(t.TempDir()),
	}
}

var noopTerminalAuth TerminalAuthHandler = terminalAuthHandlerFunc(
	func(context.Context, TerminalAuthRequest) error {
		return nil
	},
)
