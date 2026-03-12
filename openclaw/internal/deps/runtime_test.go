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

func TestInspectProfilesAndHasMissing(t *testing.T) {
	t.Parallel()

	report, err := InspectProfiles(
		t.TempDir(),
		[]string{ProfilePDF},
	)
	require.NoError(t, err)
	require.NotEmpty(t, report.Sources)

	require.True(t, HasMissing(Report{
		Missing: Missing{Bins: []string{"x"}},
	}))
	require.False(t, HasMissing(Report{}))
}

func TestFindPythonRuntime_PrefersManagedCandidate(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	candidate := ManagedPythonCandidates(stateDir)[0]
	require.NoError(
		t,
		os.MkdirAll(filepath.Dir(candidate), 0o755),
	)
	require.NoError(t, os.WriteFile(candidate, []byte("x"), 0o644))

	got := FindPythonRuntime(stateDir)
	require.True(t, got.Found)
	require.True(t, got.Managed)
	require.Equal(t, candidate, got.Path)
	require.Equal(t, ManagedPythonRoot(stateDir), got.EnvRoot)
}

func TestRuntimeHelpers(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	require.Empty(t, ManagedToolchainRoot(""))
	require.Equal(
		t,
		filepath.Join(stateDir, defaultToolchainDir),
		ManagedToolchainRoot(stateDir),
	)
	require.Equal(
		t,
		filepath.Join(
			stateDir,
			defaultToolchainDir,
			defaultPythonEnvDir,
		),
		ManagedPythonRoot(stateDir),
	)
	if runtime.GOOS == "windows" {
		require.Equal(
			t,
			filepath.Join(
				stateDir,
				defaultToolchainDir,
				defaultPythonEnvDir,
				"Scripts",
			),
			ManagedBinDir(stateDir),
		)
	} else {
		require.Equal(
			t,
			filepath.Join(
				stateDir,
				defaultToolchainDir,
				defaultPythonEnvDir,
				"bin",
			),
			ManagedBinDir(stateDir),
		)
	}

	require.Equal(t, "current", prependPath("", "current"))
	require.Equal(t, "prefix", prependPath("prefix", ""))
	require.Contains(
		t,
		prependPath("prefix", "current"),
		"prefix"+string(os.PathListSeparator)+"current",
	)

	dir := filepath.Join(stateDir, "dir")
	file := filepath.Join(stateDir, "file.txt")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	require.True(t, dirExists(dir))
	require.False(t, dirExists(file))
	require.True(t, fileExists(file))
	require.False(t, fileExists(dir))
	require.False(t, dirExists(" "))
	require.False(t, fileExists(" "))
}

func TestInspectSourceAndNormalizeMissing(t *testing.T) {
	t.Parallel()

	source := Source{
		Name: " demo ",
		Requires: Requirement{
			Bins:    []string{"missing-bin"},
			AnyBins: []string{"missing-a", "missing-a", "missing-b"},
			Python: []PythonPackage{{
				Module:  "missing.mod",
				Package: "missing-pkg",
			}},
		},
	}

	report, missing, err := inspectSource(
		Toolchain{},
		source,
		true,
	)
	require.NoError(t, err)
	require.Equal(t, "demo", report.Name)
	require.Len(t, report.Bins, 1)
	require.False(t, report.Bins[0].Found)
	require.Len(t, report.AnyBins, 1)
	require.False(t, report.AnyBins[0].Satisfied)
	require.Len(t, report.Python, 1)
	require.False(t, report.Python[0].Found)

	require.Equal(t, []string{"missing-bin"}, missing.Bins)
	require.Equal(
		t,
		[][]string{{"missing-a", "missing-b"}},
		missing.AnyBins,
	)
	require.Equal(
		t,
		[]PythonPackage{{
			Module:  "missing.mod",
			Package: "missing-pkg",
		}},
		missing.Python,
	)
}

func TestCheckPythonPackages_CommandFailure(t *testing.T) {
	t.Parallel()

	status, err := CheckPythonPackages(
		PythonRuntime{
			Found: true,
			Path:  filepath.Join(t.TempDir(), "missing-python"),
		},
		[]PythonPackage{{Module: "mod", Package: "pkg"}},
	)
	require.Error(t, err)
	require.Len(t, status, 1)
	require.False(t, status[0].Found)
}

func TestPythonExecCommand_AcceptsVersionedInterpreterPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertions run on unix-like systems")
	}

	path := writeTestCommand(
		t,
		t.TempDir(),
		"python3.11",
		"printf ok",
	)
	cmd, err := pythonExecCommand(path)
	require.NoError(t, err)

	out, err := cmd.CombinedOutput()
	require.NoError(t, err)
	require.Equal(t, "ok", string(out))
}

func TestResolveExecutable_RejectsNonExecutablePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable bits are platform-specific")
	}

	path := filepath.Join(t.TempDir(), "python3")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	_, err := resolveExecutable(path, "")
	require.Error(t, err)
}
