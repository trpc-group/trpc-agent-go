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
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolEnv_UsesManagedBinDir(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	binDir := ManagedBinDir(stateDir)
	require.NoError(t, os.MkdirAll(binDir, 0o755))

	env := ToolEnv(stateDir)
	require.Contains(t, env[envPath], binDir)
	require.Equal(t, ManagedPythonRoot(stateDir), env[envVirtualEnv])
}

func TestInspectStartup_SkipsPythonChecks(t *testing.T) {
	t.Parallel()

	report, err := InspectStartup(t.TempDir(), []Source{{
		Name: "x",
		Requires: Requirement{
			Python: []PythonPackage{{
				Module: "definitely_missing_python_module",
			}},
		},
	}})
	require.NoError(t, err)
	require.Empty(t, report.Missing.Python)
}

func TestManagedPythonCandidates(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	candidates := ManagedPythonCandidates(stateDir)
	require.NotEmpty(t, candidates)
	if runtime.GOOS == "windows" {
		require.Equal(
			t,
			filepath.Join(ManagedBinDir(stateDir), "python.exe"),
			candidates[0],
		)
		return
	}
	require.Equal(
		t,
		filepath.Join(ManagedBinDir(stateDir), "python3"),
		candidates[0],
	)
}
