//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const (
	outputLimitHelperEnv    = "TRPC_AGENT_GO_OUTPUT_LIMIT_HELPER"
	outputLimitHelperFinite = "finite"
	outputLimitHelperBlock  = "block"
	outputLimitHelperTree   = "tree"
	outputLimitHelperChild  = "child"
	outputLimitMarkerEnv    = "TRPC_AGENT_GO_OUTPUT_LIMIT_MARKER"
	outputLimitTestBytes    = int64(9)
)

func TestRuntime_RunProgramMaxOutputBytes(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	ws := codeexecutor.Workspace{ID: "output-limit", Path: t.TempDir()}

	res, err := rt.RunProgram(
		context.Background(),
		ws,
		codeexecutor.RunProgramSpec{
			Cmd: os.Args[0],
			Args: []string{
				"-test.run=^TestOutputLimitHelperProcess$",
			},
			Env: map[string]string{
				outputLimitHelperEnv: outputLimitHelperBlock,
			},
			Timeout:        5 * time.Second,
			MaxOutputBytes: outputLimitTestBytes,
		},
	)

	require.NoError(t, err)
	require.True(t, res.OutputLimitReached)
	require.False(t, res.TimedOut)
	require.Equal(t, int(outputLimitTestBytes),
		len(res.Stdout)+len(res.Stderr))
	require.Equal(t, "abcde", res.Stdout)
	require.Equal(t, "xxxx", res.Stderr)
}

func TestRuntime_RunProgramMaxOutputBytesDisabled(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	ws := codeexecutor.Workspace{
		ID:   "output-limit-disabled",
		Path: t.TempDir(),
	}

	res, err := rt.RunProgram(
		context.Background(),
		ws,
		codeexecutor.RunProgramSpec{
			Cmd: os.Args[0],
			Args: []string{
				"-test.run=^TestOutputLimitHelperProcess$",
			},
			Env: map[string]string{
				outputLimitHelperEnv: outputLimitHelperFinite,
			},
			Timeout:        5 * time.Second,
			MaxOutputBytes: 0,
		},
	)

	require.NoError(t, err)
	require.False(t, res.OutputLimitReached)
	require.Equal(t, "abcde", res.Stdout)
	require.Equal(t, "xxxxx", res.Stderr)
}

func TestRuntime_RunProgramOutputLimitKillsProcessTree(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-survived")
	rt := NewRuntime(t.TempDir())
	ws := codeexecutor.Workspace{ID: "output-limit-tree", Path: t.TempDir()}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:  os.Args[0],
		Args: []string{"-test.run=^TestOutputLimitHelperProcess$"},
		Env: map[string]string{
			outputLimitHelperEnv: outputLimitHelperTree,
			outputLimitMarkerEnv: marker,
		},
		Timeout: 5 * time.Second, MaxOutputBytes: outputLimitTestBytes,
	})
	require.NoError(t, err)
	require.True(t, res.OutputLimitReached)
	time.Sleep(1500 * time.Millisecond)
	require.NoFileExists(t, marker, "descendant survived output-limit cancellation")
}

func TestRuntime_StartProgramOutputLimitKillsProcessTree(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "interactive-child-survived")
	rt := NewRuntime(t.TempDir())
	ws := codeexecutor.Workspace{ID: "interactive-output-limit-tree", Path: t.TempDir()}
	sess, err := rt.StartProgram(context.Background(), ws, codeexecutor.InteractiveProgramSpec{
		RunProgramSpec: codeexecutor.RunProgramSpec{
			Cmd:  os.Args[0],
			Args: []string{"-test.run=^TestOutputLimitHelperProcess$"},
			Env: map[string]string{
				outputLimitHelperEnv: outputLimitHelperTree,
				outputLimitMarkerEnv: marker,
			},
			Timeout: 5 * time.Second, MaxOutputBytes: outputLimitTestBytes,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	state := sess.(codeexecutor.ProgramStateProvider)
	require.Eventually(t, func() bool {
		return state.State().Status == codeexecutor.ProgramStatusExited
	}, 5*time.Second, 10*time.Millisecond)
	time.Sleep(1500 * time.Millisecond)
	require.NoFileExists(t, marker, "interactive descendant survived output-limit cancellation")
}

func TestRuntime_StartProgramMaxOutputBytes(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	ws := codeexecutor.Workspace{
		ID:   "interactive-output-limit",
		Path: t.TempDir(),
	}

	sess, err := rt.StartProgram(
		context.Background(),
		ws,
		codeexecutor.InteractiveProgramSpec{
			RunProgramSpec: codeexecutor.RunProgramSpec{
				Cmd: os.Args[0],
				Args: []string{
					"-test.run=^TestOutputLimitHelperProcess$",
				},
				Env: map[string]string{
					outputLimitHelperEnv: outputLimitHelperBlock,
				},
				Timeout:        5 * time.Second,
				MaxOutputBytes: outputLimitTestBytes,
			},
		},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	state, ok := sess.(codeexecutor.ProgramStateProvider)
	require.True(t, ok)
	require.Eventually(t, func() bool {
		return state.State().Status == codeexecutor.ProgramStatusExited
	}, 5*time.Second, 10*time.Millisecond)

	provider, ok := sess.(codeexecutor.ProgramResultProvider)
	require.True(t, ok)
	res := provider.RunResult()
	require.True(t, res.OutputLimitReached)
	require.False(t, res.TimedOut)
	require.Equal(t, int(outputLimitTestBytes),
		len(res.Stdout)+len(res.Stderr))
	require.Equal(t, "abcde", res.Stdout)
	require.Equal(t, "xxxx", res.Stderr)
}

func TestLegacyCodeExecutionCommandUsesContextOutputLimit(t *testing.T) {
	t.Setenv(outputLimitHelperEnv, outputLimitHelperBlock)
	executor := &CodeExecutor{Timeout: 5 * time.Second}
	ctx := codeexecutor.WithExecutionLimits(context.Background(), codeexecutor.ExecutionLimits{
		MaxOutputBytes: outputLimitTestBytes,
	})
	started := time.Now()
	output, err := executor.executeCommand(ctx, t.TempDir(), []string{
		os.Args[0], "-test.run=^TestOutputLimitHelperProcess$",
	})
	require.Error(t, err)
	require.Less(t, time.Since(started), 4*time.Second)
	require.LessOrEqual(t, len(output), int(outputLimitTestBytes))
}

func TestLegacyCodeExecutionGeneratedErrorsUseOutputLimit(t *testing.T) {
	const maxOutput = int64(12)
	executor := &CodeExecutor{Timeout: 5 * time.Second}
	ctx := codeexecutor.WithExecutionLimits(context.Background(), codeexecutor.ExecutionLimits{
		MaxOutputBytes: maxOutput,
	})
	result, err := executor.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "unsupported"},
			{Language: "unsupported"},
		},
	})
	require.NoError(t, err)
	require.LessOrEqual(t, int64(len(result.Output)), maxOutput)
}

func TestLegacyCodeExecutionUsesContextTimeoutLimit(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is unavailable")
	}
	executor := &CodeExecutor{Timeout: 5 * time.Second}
	ctx := codeexecutor.WithExecutionLimits(context.Background(), codeexecutor.ExecutionLimits{
		MaxTimeout: 100 * time.Millisecond,
	})
	started := time.Now()
	result, err := executor.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{Language: "bash", Code: "sleep 5"}},
	})
	require.NoError(t, err)
	require.Contains(t, result.Output, "Error executing code block")
	require.Less(t, time.Since(started), 2*time.Second)
}

func TestLocalEngineSupportsMaxOutputBytes(t *testing.T) {
	capabilities := New().Engine().Describe()
	require.True(t, capabilities.SupportsMaxOutputBytes)
}

func TestOutputLimitHelperProcess(t *testing.T) {
	mode := os.Getenv(outputLimitHelperEnv)
	if mode == "" {
		return
	}
	if mode == outputLimitHelperChild {
		time.Sleep(time.Second)
		_ = os.WriteFile(os.Getenv(outputLimitMarkerEnv), []byte("survived"), 0o600)
		os.Exit(0)
	}
	if mode == outputLimitHelperTree {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOutputLimitHelperProcess$")
		cmd.Env = append(os.Environ(),
			outputLimitHelperEnv+"="+outputLimitHelperChild,
			outputLimitMarkerEnv+"="+os.Getenv(outputLimitMarkerEnv),
		)
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 64))
		time.Sleep(30 * time.Second)
	}

	_, _ = fmt.Fprint(os.Stdout, "abcde")
	time.Sleep(100 * time.Millisecond)
	stderr := "xxxxx"
	if mode == outputLimitHelperBlock {
		stderr = strings.Repeat("x", 64)
	}
	_, _ = fmt.Fprint(os.Stderr, stderr)
	if mode == outputLimitHelperFinite {
		os.Exit(0)
	}
	time.Sleep(30 * time.Second)
}
