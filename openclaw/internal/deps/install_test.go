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

	"github.com/stretchr/testify/require"
)

func TestBuildPlanForSources_PythonPackages(t *testing.T) {
	t.Parallel()

	plan, err := BuildPlanForSources(
		t.TempDir(),
		[]string{"test"},
		[]Source{{
			Name: "test",
			Requires: Requirement{
				Python: []PythonPackage{{
					Module:  "definitely_missing_python_module",
					Package: "definitely-missing-python-package",
				}},
			},
		}},
	)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Steps)
	require.Equal(t, stepKindVenv, plan.Steps[0].Kind)
	require.Equal(t, stepKindPython, plan.Steps[1].Kind)
	require.Contains(
		t,
		plan.Steps[0].CommandLine,
		venvFlagSystemSitePackages,
	)
	require.Contains(
		t,
		plan.Steps[1].CommandLine,
		"definitely-missing-python-package",
	)
}

func TestBuildPlanForSources_SystemPackages(t *testing.T) {
	t.Parallel()

	manager := DetectPackageManager()
	if manager == "" {
		t.Skip("no supported package manager")
	}

	action := InstallAction{
		Kind:    manager,
		Formula: "tool-pkg",
		Bins:    []string{"missing-test-bin"},
	}
	plan, err := BuildPlanForSources(
		t.TempDir(),
		[]string{"test"},
		[]Source{{
			Name: "test",
			Requires: Requirement{
				Bins: []string{"missing-test-bin"},
			},
			Install: []InstallAction{action},
		}},
	)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Steps)
	require.Equal(
		t,
		stepKindSystem,
		plan.Steps[len(plan.Steps)-1].Kind,
	)
	require.Contains(
		t,
		plan.Steps[len(plan.Steps)-1].CommandLine,
		"tool-pkg",
	)
}

func TestBuildPlan_ResolvesBuiltinProfiles(t *testing.T) {
	t.Parallel()

	plan, err := BuildPlan(
		t.TempDir(),
		[]string{ProfileCommonFileTools},
	)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Profiles)
	require.Contains(t, plan.Profiles, ProfilePDF)
}

func TestApplyPlan_RunsStepsAndCapturesFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertions run on unix-like systems")
	}

	stateDir := t.TempDir()
	require.NoError(t, os.MkdirAll(ManagedBinDir(stateDir), 0o755))
	envCmd := writeTestCommand(
		t,
		t.TempDir(),
		"python3",
		`printf '%s|%s|%s' `+
			`"$OPENCLAW_TOOLCHAIN_ROOT" `+
			`"$OPENCLAW_TOOLCHAIN_PYTHON" `+
			`"$PIP_DISABLE_PIP_VERSION_CHECK"`,
	)
	failCmd := writeTestCommand(
		t,
		t.TempDir(),
		"python3",
		"printf fail\nexit 3",
	)

	plan := Plan{
		Toolchain: Toolchain{StateDir: stateDir},
		Steps: []Step{
			{},
			{
				Label:   "print env",
				Kind:    stepKindPython,
				Command: []string{envCmd},
			},
		},
	}
	result, err := ApplyPlan(context.Background(), plan)
	require.NoError(t, err)
	require.Len(t, result.Steps, 1)
	require.Equal(t, 0, result.Steps[0].ExitCode)
	require.Contains(
		t,
		result.Steps[0].Output,
		ManagedToolchainRoot(stateDir),
	)
	require.Contains(
		t,
		result.Steps[0].Output,
		filepath.Join(ManagedBinDir(stateDir), "python3"),
	)
	require.Contains(
		t,
		result.Steps[0].Output,
		pipDisableVersionValue,
	)

	failPlan := Plan{
		Toolchain: Toolchain{StateDir: stateDir},
		Steps: []Step{{
			Label:   "fail",
			Kind:    stepKindPython,
			Command: []string{failCmd},
		}},
	}
	result, err = ApplyPlan(context.Background(), failPlan)
	require.Error(t, err)
	require.Len(t, result.Steps, 1)
	require.Equal(t, 3, result.Steps[0].ExitCode)
	require.Equal(t, "fail", result.Steps[0].Output)
}

func TestApplyPlan_RejectsUnsupportedExecutable(t *testing.T) {
	t.Parallel()

	result, err := ApplyPlan(context.Background(), Plan{
		Steps: []Step{{
			Label:   "invalid",
			Kind:    stepKindPython,
			Command: []string{filepath.Join(t.TempDir(), "custom-python")},
		}},
	})
	require.Error(t, err)
	require.Empty(t, result.Steps)
	require.Contains(t, err.Error(), "invalid")
}

func TestApplyPlan_RejectsRootOnlyStep(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("root-only branch requires non-root unix")
	}

	result, err := ApplyPlan(context.Background(), Plan{
		Steps: []Step{{
			Label:        "root step",
			Kind:         stepKindSystem,
			Command:      []string{"sh", "-c", "true"},
			RequiresRoot: true,
		}},
	})
	require.Error(t, err)
	require.Empty(t, result.Steps)
	require.Contains(t, err.Error(), "requires root privileges")
}

func TestInstallHelpers(t *testing.T) {
	t.Parallel()

	step := newSystemStep(InstallKindAPT, []string{"pkg"})
	require.True(t, step.RequiresRoot)
	require.Equal(
		t,
		[]string{InstallKindAPT, "install", "-y", "pkg"},
		step.Command,
	)
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		require.True(
			t,
			strings.HasPrefix(step.CommandLine, "sudo "),
		)
	}

	require.Equal(
		t,
		[]string{"brew-pkg"},
		packagesForAction(InstallAction{
			Kind:    InstallKindBrew,
			Formula: "brew-pkg",
		}),
	)
	require.Equal(
		t,
		[]string{"a", "b"},
		packagesForAction(InstallAction{
			Kind:     InstallKindAPT,
			Packages: []string{"a", "b"},
		}),
	)
	require.Equal(
		t,
		[]string{"fallback"},
		packagesForAction(InstallAction{
			Package: "fallback",
		}),
	)

	covered := map[string]struct{}{
		"one": {},
	}
	require.True(t, anyCovered([]string{"one", "two"}, covered))
	require.False(t, anyCovered([]string{"two"}, covered))

	unresolved := unresolvedMissing(
		InstallKindAPT,
		[]Source{{
			Name: "demo",
			Install: []InstallAction{{
				Kind:     InstallKindAPT,
				Packages: []string{"pkg"},
				Bins:     []string{"one"},
			}},
		}},
		Missing{
			Bins:    []string{"one", "two"},
			AnyBins: [][]string{{"one", "other"}, {"x", "y"}},
		},
	)
	require.Equal(t, []string{"two"}, unresolved.Bins)
	require.Equal(t, [][]string{{"x", "y"}}, unresolved.AnyBins)

	stateDir := t.TempDir()
	require.NoError(t, os.MkdirAll(ManagedBinDir(stateDir), 0o755))
	env := mergedPlanEnv(Toolchain{StateDir: stateDir})
	require.Contains(
		t,
		strings.Join(env, "\n"),
		envOpenClawToolchain+"="+ManagedToolchainRoot(stateDir),
	)
	env = setPlanEnv([]string{"A=1"}, "A", "2")
	require.Equal(t, []string{"A=2"}, env)
	env = setPlanEnv(env, "B", "3")
	require.Equal(t, []string{"A=2", "B=3"}, env)

	require.Equal(t, "''", shellQuote(""))
	require.Equal(t, "plain", shellQuote("plain"))
	require.Equal(
		t,
		"'needs space'",
		shellQuote("needs space"),
	)
	require.Equal(
		t,
		[]string{"a", "b"},
		profileNames([]Profile{
			{Name: "a"},
			{Name: "b"},
		}),
	)
}

func writeTestCommand(
	t *testing.T,
	dir string,
	name string,
	body string,
) string {
	t.Helper()

	path := filepath.Join(dir, name)
	script := "#!/bin/sh\nset -eu\n" + body + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}
