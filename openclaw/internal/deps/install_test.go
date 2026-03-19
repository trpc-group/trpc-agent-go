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
	"archive/tar"
	"compress/gzip"
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

func TestBuildPlanForSources_UserInstallSteps(t *testing.T) {
	t.Parallel()

	const (
		goBinName   = "definitely-missing-go-bin"
		nodeBinName = "definitely-missing-node-bin"
		uvBinName   = "definitely-missing-uv-bin"
	)

	plan, err := BuildPlanForSources(
		t.TempDir(),
		[]string{"test"},
		[]Source{{
			Name: "test",
			Requires: Requirement{
				Bins: []string{goBinName, nodeBinName, uvBinName},
			},
			Install: []InstallAction{
				{
					Kind:   InstallKindGo,
					Module: "github.com/example/eightctl@latest",
					Bins:   []string{goBinName},
				},
				{
					Kind:    InstallKindNode,
					Package: "mcporter",
					Bins:    []string{nodeBinName},
				},
				{
					Kind:    InstallKindUV,
					Package: "nano-pdf",
					Bins:    []string{uvBinName},
				},
			},
		}},
	)
	require.NoError(t, err)
	require.Len(t, plan.Steps, 4)
	require.Equal(t, stepKindVenv, plan.Steps[0].Kind)
	require.Equal(t, stepKindPython, plan.Steps[1].Kind)
	require.Contains(t, plan.Steps[1].CommandLine, "nano-pdf")
	require.Equal(t, stepKindCommand, plan.Steps[2].Kind)
	require.Contains(
		t,
		plan.Steps[2].CommandLine,
		"github.com/example/eightctl@latest",
	)
	require.Equal(t, stepKindCommand, plan.Steps[3].Kind)
	require.Contains(t, plan.Steps[3].CommandLine, "mcporter")
	require.Empty(t, plan.Unresolved.Bins)
}

func TestInstallStepHelpers(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	goStep, err := goInstallStep(Toolchain{StateDir: stateDir}, InstallAction{
		Kind:   InstallKindGo,
		Module: "example.com/tool@latest",
	})
	require.NoError(t, err)
	require.Equal(
		t,
		[]string{ManagedBinDir(stateDir)},
		goStep.EnsureDirs,
	)
	require.Equal(t, ManagedBinDir(stateDir), goStep.Env[envGoBin])

	npmStep, err := npmInstallStep(
		Toolchain{StateDir: stateDir},
		InstallAction{
			Kind:    InstallKindNPM,
			Package: "pkg",
		},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		[]string{ManagedToolPrefix(stateDir)},
		npmStep.EnsureDirs,
	)
	require.Contains(t, npmStep.Command, ManagedToolPrefix(stateDir))
}

func TestEnsureStepWorkingDirs_UsesEnsureDirs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ensureDir := filepath.Join(root, "npm-prefix")
	err := ensureStepWorkingDirs(Toolchain{}, Step{
		Kind:       stepKindCommand,
		EnsureDirs: []string{"", ensureDir},
		Env: map[string]string{
			envGoBin: filepath.Join(root, "ignored"),
		},
	})
	require.NoError(t, err)
	info, statErr := os.Stat(ensureDir)
	require.NoError(t, statErr)
	require.True(t, info.IsDir())
	_, statErr = os.Stat(filepath.Join(root, "ignored"))
	require.Error(t, statErr)
}

func TestActionMatchesPlatform(t *testing.T) {
	t.Parallel()

	require.True(t, actionMatchesPlatform(InstallAction{}, runtime.GOOS))
	require.True(t, actionMatchesPlatform(
		InstallAction{OS: []string{runtime.GOOS}},
		runtime.GOOS,
	))
	require.True(t, actionMatchesPlatform(
		InstallAction{OS: []string{"win32"}},
		"windows",
	))
	require.False(t, actionMatchesPlatform(
		InstallAction{OS: []string{"darwin"}},
		"linux",
	))
}

func TestSelectInstallActionsForMissing(t *testing.T) {
	t.Parallel()

	actions := selectInstallActionsForMissing(
		Platform{GOOS: "linux", PackageManager: InstallKindAPT},
		[]Source{{
			Name: "demo",
			Install: []InstallAction{
				{
					Kind: InstallKindGo,
					Bins: []string{"go-bin"},
				},
				{
					Kind: InstallKindGo,
					Bins: []string{"go-bin"},
					OS:   []string{"darwin"},
				},
				{
					Kind: InstallKindNPM,
					Bins: []string{"alt-bin"},
				},
			},
		}},
		Missing{
			AnyBins: [][]string{{"go-bin", "alt-bin"}},
		},
		isCommandInstallKind,
	)
	require.Len(t, actions, 1)
	require.Equal(t, InstallKindGo, actions[0].Action.Kind)
}

func TestBuildPlanForSources_AnyBinChoosesFirstInstallAction(
	t *testing.T,
) {
	t.Parallel()

	manager := DetectPackageManager()
	if manager == "" {
		t.Skip("no supported package manager")
	}

	primary := InstallAction{
		Kind: manager,
		Bins: []string{"preferred"},
	}
	fallback := InstallAction{
		Kind: manager,
		Bins: []string{"fallback"},
	}
	switch manager {
	case InstallKindBrew:
		primary.Formula = "preferred-pkg"
		fallback.Formula = "fallback-pkg"
	default:
		primary.Package = "preferred-pkg"
		fallback.Package = "fallback-pkg"
	}

	plan, err := BuildPlanForSources(
		t.TempDir(),
		[]string{"spotify"},
		[]Source{{
			Name: "spotify",
			Requires: Requirement{
				AnyBins: []string{"preferred", "fallback"},
			},
			Install: []InstallAction{primary, fallback},
		}},
	)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Steps)
	last := plan.Steps[len(plan.Steps)-1]
	require.Equal(t, stepKindSystem, last.Kind)
	require.Contains(t, last.CommandLine, "preferred-pkg")
	require.NotContains(t, last.CommandLine, "fallback-pkg")
}

func TestBuildPlanForSources_DownloadActionWithoutBins(t *testing.T) {
	t.Parallel()

	plan, err := BuildPlanForSources(
		t.TempDir(),
		[]string{"tts"},
		[]Source{{
			Name: "tts",
			Requires: Requirement{
				Env: []string{"SHERPA_ONNX_RUNTIME_DIR"},
			},
			Install: []InstallAction{{
				Kind:      InstallKindDownload,
				URL:       "https://example.com/runtime.tar.gz",
				Archive:   "tar.gz",
				Extract:   true,
				TargetDir: "runtime",
			}},
		}},
	)
	require.NoError(t, err)
	require.Len(t, plan.Steps, 1)
	require.Equal(t, stepKindDownload, plan.Steps[0].Kind)
	require.Equal(
		t,
		filepath.Join(
			plan.Toolchain.StateDir,
			defaultToolsDir,
			"tts",
			"runtime",
		),
		plan.Steps[0].TargetPath,
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
	require.Equal(t, stepStatusApplied, result.Steps[0].Status)
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
	require.Equal(t, stepStatusFailed, result.Steps[0].Status)
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
	require.Len(t, result.Steps, 1)
	require.Equal(t, stepStatusFailed, result.Steps[0].Status)
	require.Contains(t, result.Steps[0].Error, "invalid")
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
	require.NoError(t, err)
	require.Len(t, result.Steps, 1)
	require.Equal(t, stepStatusDeferred, result.Steps[0].Status)
	require.Contains(t, result.Steps[0].Error, "requires root privileges")
}

func TestApplyPlan_DownloadStepExtractsArchive(t *testing.T) {
	t.Parallel()

	archivePath := filepath.Join(t.TempDir(), "runtime.tar.gz")
	writeTarGzArchive(
		t,
		archivePath,
		map[string]string{
			"pkg/bin/tool": "hello",
		},
	)

	target := filepath.Join(t.TempDir(), "out")
	result, err := ApplyPlan(context.Background(), Plan{
		Steps: []Step{{
			Label:           "download runtime",
			Kind:            stepKindDownload,
			URL:             "file://" + archivePath,
			TargetPath:      target,
			Archive:         "tar.gz",
			Extract:         true,
			StripComponents: 1,
		}},
	})
	require.NoError(t, err)
	require.Len(t, result.Steps, 1)
	require.Equal(t, stepStatusApplied, result.Steps[0].Status)

	data, err := os.ReadFile(filepath.Join(target, "bin", "tool"))
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))
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
		[]string{"tap/tool"},
		packagesForAction(InstallAction{
			Kind:    InstallKindBrew,
			Formula: "tool",
			Tap:     "tap",
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
	require.Equal(
		t,
		[]string{"github.com/example/tool@latest"},
		packagesForAction(InstallAction{
			Kind:   InstallKindGo,
			Module: "github.com/example/tool@latest",
		}),
	)

	covered := map[string]struct{}{
		"one": {},
	}
	require.True(t, anyCovered([]string{"one", "two"}, covered))
	require.False(t, anyCovered([]string{"two"}, covered))

	unresolved := unresolvedMissing(
		Platform{
			GOOS:           runtime.GOOS,
			PackageManager: InstallKindAPT,
		},
		[]Source{{
			Name: "demo",
			Install: []InstallAction{{
				Kind:     InstallKindAPT,
				Packages: []string{"pkg"},
				Bins:     []string{"one"},
			}, {
				Kind:   InstallKindGo,
				Module: "example.com/two@latest",
				Bins:   []string{"two"},
			}},
		}},
		Missing{
			Bins:    []string{"one", "two"},
			AnyBins: [][]string{{"one", "other"}, {"x", "y"}},
		},
	)
	require.Empty(t, unresolved.Bins)
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

func writeTarGzArchive(
	t *testing.T,
	path string,
	files map[string]string,
) {
	t.Helper()

	file, err := os.Create(path)
	require.NoError(t, err)
	defer file.Close()

	gz := gzip.NewWriter(file)
	defer gz.Close()

	tw := tar.NewWriter(gz)
	defer tw.Close()

	for name, body := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(body)),
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
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
