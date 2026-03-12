//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package deps

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPlanStepCommand(t *testing.T) {
	t.Parallel()

	_, err := planStepCommand(Toolchain{}, Step{})
	require.ErrorContains(t, err, "empty step command")

	_, err = planStepCommand(Toolchain{}, Step{
		Kind:    "custom",
		Command: []string{"tool"},
	})
	require.ErrorContains(t, err, "unsupported step kind")

	stateDir := t.TempDir()
	require.NoError(t, os.MkdirAll(ManagedBinDir(stateDir), 0o755))

	pythonPath := writeTestCommand(
		t,
		t.TempDir(),
		"python3.11",
		"printf python",
	)
	cmd, err := planStepCommand(Toolchain{StateDir: stateDir}, Step{
		Kind:    stepKindPython,
		Command: []string{pythonPath, "-c", "print('ignored')"},
	})
	require.NoError(t, err)
	require.Equal(t, pythonPath, cmd.Path)
	require.Equal(
		t,
		[]string{pythonPath, "-c", "print('ignored')"},
		cmd.Args,
	)
	require.Contains(
		t,
		strings.Join(cmd.Env, "\n"),
		envOpenClawToolchain+"="+ManagedToolchainRoot(stateDir),
	)

	managerPath := writeTestCommand(
		t,
		t.TempDir(),
		"apt",
		"printf system",
	)
	cmd, err = planStepCommand(Toolchain{}, Step{
		Kind:    stepKindSystem,
		Command: []string{managerPath, "install", "-y"},
	})
	require.NoError(t, err)
	require.Equal(t, managerPath, cmd.Path)
	require.Equal(t, []string{managerPath, "install", "-y"}, cmd.Args)
}

func TestCombinedOutputContext(t *testing.T) {
	t.Parallel()

	_, err := combinedOutputContext(context.Background(), nil)
	require.ErrorContains(t, err, "nil command")

	if runtime.GOOS == "windows" {
		t.Skip("shell assertions run on unix-like systems")
	}

	okPath := writeTestCommand(
		t,
		t.TempDir(),
		"python3",
		"printf ok",
	)
	out, err := combinedOutputContext(
		nil,
		newExecCommand(executableSpec{path: okPath}),
	)
	require.NoError(t, err)
	require.Equal(t, "ok", string(out))

	out, err = combinedOutputContext(
		context.Background(),
		newExecCommand(executableSpec{path: okPath}),
	)
	require.NoError(t, err)
	require.Equal(t, "ok", string(out))

	out, err = combinedOutputContext(
		context.Background(),
		newExecCommand(executableSpec{
			path: filepath.Join(t.TempDir(), "missing"),
		}),
	)
	require.Error(t, err)
	require.Empty(t, out)

	slowPath := writeTestCommand(
		t,
		t.TempDir(),
		"python3",
		"sleep 1\nprintf late",
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	out, err = combinedOutputContext(
		ctx,
		newExecCommand(executableSpec{path: slowPath}),
	)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Empty(t, out)
}

func TestSystemCommandSpec(t *testing.T) {
	t.Parallel()

	for _, manager := range []string{
		InstallKindAPT,
		InstallKindBrew,
		InstallKindDNF,
		InstallKindYUM,
	} {
		manager := manager
		t.Run(manager, func(t *testing.T) {
			t.Parallel()

			path := writeTestCommand(
				t,
				t.TempDir(),
				manager,
				"printf manager",
			)
			spec, err := systemCommandSpec(path)
			require.NoError(t, err)
			require.Equal(t, path, spec.path)
		})
	}

	_, err := systemCommandSpec("pkgmgr")
	require.ErrorContains(t, err, "unsupported package manager")
}

func TestResolveExecutable(t *testing.T) {
	_, err := resolveExecutable("", "")
	require.ErrorContains(t, err, "empty executable name")

	if runtime.GOOS == "windows" {
		t.Skip("shell assertions run on unix-like systems")
	}

	explicitPath := writeTestCommand(
		t,
		t.TempDir(),
		"python3.12",
		"printf explicit",
	)
	spec, err := resolveExecutable(explicitPath, "")
	require.NoError(t, err)
	require.Equal(t, explicitPath, spec.path)

	dir := t.TempDir()
	_, err = resolveExecutable(dir, "")
	require.ErrorContains(t, err, "is a directory")

	toolDir := t.TempDir()
	toolPath := writeTestCommand(
		t,
		toolDir,
		"python-custom",
		"printf lookup",
	)
	t.Setenv("PATH", toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	spec, err = resolveExecutable("", "python-custom")
	require.NoError(t, err)
	require.Equal(t, toolPath, spec.path)
}

func TestCommandPathHelpers(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", commandDir(""))
	require.Equal(t, "", commandDir(" python3.11 "))
	require.Equal(
		t,
		filepath.Join("tmp", "bin"),
		commandDir(filepath.Join("tmp", "bin", "python3.11")),
	)

	require.Equal(t, "", commandBase(""))
	require.Equal(
		t,
		"python3.11",
		commandBase("  "+filepath.Join("tmp", "bin", "python3.11")+"  "),
	)
}
