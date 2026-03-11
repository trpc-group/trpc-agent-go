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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const (
	stepKindSystem = "system"
	stepKindPython = "python"
	stepKindVenv   = "venv"

	venvFlagSystemSitePackages = "--system-site-packages"
)

type Step struct {
	Label        string   `json:"label"`
	Kind         string   `json:"kind"`
	Command      []string `json:"command"`
	CommandLine  string   `json:"command_line"`
	RequiresRoot bool     `json:"requires_root,omitempty"`
}

type Plan struct {
	Platform   Platform  `json:"platform"`
	Toolchain  Toolchain `json:"toolchain"`
	Profiles   []string  `json:"profiles,omitempty"`
	Steps      []Step    `json:"steps,omitempty"`
	Unresolved Missing   `json:"unresolved,omitempty"`
}

type ApplyResult struct {
	Steps []StepResult `json:"steps,omitempty"`
}

type StepResult struct {
	Step     Step   `json:"step"`
	Output   string `json:"output,omitempty"`
	ExitCode int    `json:"exit_code"`
}

func BuildPlan(stateDir string, profiles []string) (Plan, error) {
	resolved, err := ResolveProfiles(profiles)
	if err != nil {
		return Plan{}, err
	}

	sources, err := SourcesForProfiles(profiles)
	if err != nil {
		return Plan{}, err
	}
	return BuildPlanForSources(stateDir, profileNames(resolved), sources)
}

func BuildPlanForSources(
	stateDir string,
	names []string,
	sources []Source,
) (Plan, error) {
	report, err := Inspect(stateDir, sources)
	if err != nil {
		return Plan{}, err
	}

	plan := Plan{
		Platform:  report.Platform,
		Toolchain: report.Toolchain,
		Profiles:  append([]string(nil), names...),
	}

	manager := report.Platform.PackageManager
	pythonPkgs := collectPythonPackages(report.Missing.Python)
	if len(pythonPkgs) > 0 {
		steps, err := pythonSteps(report.Toolchain, pythonPkgs)
		if err != nil {
			return Plan{}, err
		}
		plan.Steps = append(plan.Steps, steps...)
	}

	systemPkgs := collectSystemPackages(manager, sources, report.Missing)
	if len(systemPkgs) > 0 {
		plan.Steps = append(plan.Steps, newSystemStep(manager, systemPkgs))
	}

	plan.Unresolved = unresolvedMissing(
		manager,
		sources,
		report.Missing,
	)
	return plan, nil
}

func ApplyPlan(
	ctx context.Context,
	plan Plan,
) (ApplyResult, error) {
	result := ApplyResult{
		Steps: make([]StepResult, 0, len(plan.Steps)),
	}
	for _, step := range plan.Steps {
		if len(step.Command) == 0 {
			continue
		}
		if step.RequiresRoot && runtime.GOOS != "windows" &&
			os.Geteuid() != 0 {
			return result, fmt.Errorf(
				"step %q requires root privileges; rerun as root "+
					"or execute the printed command with sudo",
				step.Label,
			)
		}

		cmd := exec.CommandContext(ctx, step.Command[0], step.Command[1:]...)
		cmd.Env = os.Environ()
		if step.Kind == stepKindPython ||
			step.Kind == stepKindVenv {
			cmd.Env = mergedPlanEnv(plan.Toolchain)
		}
		out, err := cmd.CombinedOutput()
		exitCode := 0
		if err != nil {
			exitCode = -1
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
				exitCode = exitErr.ProcessState.ExitCode()
			}
			result.Steps = append(result.Steps, StepResult{
				Step:     step,
				Output:   string(out),
				ExitCode: exitCode,
			})
			return result, fmt.Errorf(
				"step %q failed: %w",
				step.Label,
				err,
			)
		}
		result.Steps = append(result.Steps, StepResult{
			Step:     step,
			Output:   string(out),
			ExitCode: exitCode,
		})
	}
	return result, nil
}

func collectSystemPackages(
	manager string,
	sources []Source,
	missing Missing,
) []string {
	manager = strings.ToLower(strings.TrimSpace(manager))
	if manager == "" {
		return nil
	}

	needBins := map[string]struct{}{}
	for _, name := range missing.Bins {
		needBins[name] = struct{}{}
	}
	for _, group := range missing.AnyBins {
		for _, name := range group {
			needBins[name] = struct{}{}
		}
	}

	pkgs := map[string]struct{}{}
	for _, source := range sources {
		for _, action := range source.Install {
			action = normalizeInstallActions(
				[]InstallAction{action},
			)[0]
			if action.Kind != manager {
				continue
			}
			if !coversAnyBin(action.Bins, needBins) {
				continue
			}
			for _, pkg := range packagesForAction(action) {
				pkgs[pkg] = struct{}{}
			}
		}
	}

	out := make([]string, 0, len(pkgs))
	for pkg := range pkgs {
		out = append(out, pkg)
	}
	slices.Sort(out)
	return out
}

func collectPythonPackages(pkgs []PythonPackage) []string {
	normalized := normalizePythonPackages(pkgs)
	out := make([]string, 0, len(normalized))
	for _, pkg := range normalized {
		out = append(out, pkg.Package)
	}
	return out
}

func pythonSteps(
	toolchain Toolchain,
	pkgs []string,
) ([]Step, error) {
	pkgs = normalizeStrings(pkgs)
	if len(pkgs) == 0 {
		return nil, nil
	}

	bootstrap := toolchain.Python.Bootstrap
	if bootstrap == "" {
		return nil, errPythonNotFound
	}

	steps := []Step{}
	managedPython := toolchain.Python.Path
	if !toolchain.Python.Managed {
		venvRoot := ManagedPythonRoot(toolchain.StateDir)
		steps = append(steps, Step{
			Label: "Create managed Python environment",
			Kind:  stepKindVenv,
			Command: []string{
				bootstrap,
				"-m",
				"venv",
				venvFlagSystemSitePackages,
				venvRoot,
			},
			CommandLine: shellQuote(
				bootstrap,
				"-m",
				"venv",
				venvFlagSystemSitePackages,
				venvRoot,
			),
		})
		managedPython = managedPythonPath(toolchain.StateDir)
	}

	steps = append(steps, Step{
		Label: "Install Python packages",
		Kind:  stepKindPython,
		Command: append(
			[]string{
				managedPython,
				"-m",
				"pip",
				"install",
			},
			pkgs...,
		),
		CommandLine: shellQuote(
			append(
				[]string{
					managedPython,
					"-m",
					"pip",
					"install",
				},
				pkgs...,
			)...,
		),
	})
	return steps, nil
}

func newSystemStep(manager string, pkgs []string) Step {
	cmd := []string{manager}
	requiresRoot := false

	switch manager {
	case InstallKindBrew:
		cmd = append(cmd, "install")
	case InstallKindAPT:
		cmd = append(cmd, "install", "-y")
		requiresRoot = true
	case InstallKindDNF:
		cmd = append(cmd, "install", "-y")
		requiresRoot = true
	case InstallKindYUM:
		cmd = append(cmd, "install", "-y")
		requiresRoot = true
	}
	cmd = append(cmd, pkgs...)
	commandLine := shellQuote(cmd...)
	if requiresRoot && runtime.GOOS != "windows" &&
		os.Geteuid() != 0 {
		commandLine = "sudo " + commandLine
	}

	return Step{
		Label:        "Install system packages",
		Kind:         stepKindSystem,
		Command:      cmd,
		CommandLine:  commandLine,
		RequiresRoot: requiresRoot,
	}
}

func unresolvedMissing(
	manager string,
	sources []Source,
	missing Missing,
) Missing {
	manager = strings.ToLower(strings.TrimSpace(manager))
	if manager == "" {
		return normalizeMissing(missing)
	}

	coveredBins := map[string]struct{}{}
	for _, source := range sources {
		for _, action := range source.Install {
			if strings.ToLower(strings.TrimSpace(action.Kind)) != manager {
				continue
			}
			for _, bin := range normalizeStrings(action.Bins) {
				coveredBins[bin] = struct{}{}
			}
		}
	}

	var unresolved Missing
	for _, name := range missing.Bins {
		if _, ok := coveredBins[name]; ok {
			continue
		}
		unresolved.Bins = append(unresolved.Bins, name)
	}
	for _, group := range missing.AnyBins {
		if anyCovered(group, coveredBins) {
			continue
		}
		unresolved.AnyBins = append(unresolved.AnyBins, group)
	}
	return normalizeMissing(unresolved)
}

func packagesForAction(action InstallAction) []string {
	switch action.Kind {
	case InstallKindBrew:
		if action.Formula != "" {
			return []string{action.Formula}
		}
	case InstallKindAPT, InstallKindDNF, InstallKindYUM:
		if len(action.Packages) > 0 {
			return append([]string(nil), action.Packages...)
		}
		if action.Formula != "" {
			return []string{action.Formula}
		}
		if action.Package != "" {
			return []string{action.Package}
		}
	}
	if action.Package != "" {
		return []string{action.Package}
	}
	return append([]string(nil), action.Packages...)
}

func coversAnyBin(
	bins []string,
	need map[string]struct{},
) bool {
	for _, bin := range bins {
		if _, ok := need[bin]; ok {
			return true
		}
	}
	return false
}

func anyCovered(
	group []string,
	covered map[string]struct{},
) bool {
	for _, name := range group {
		if _, ok := covered[name]; ok {
			return true
		}
	}
	return false
}

func mergedPlanEnv(toolchain Toolchain) []string {
	base := os.Environ()
	for key, value := range ToolEnv(toolchain.StateDir) {
		base = setPlanEnv(base, key, value)
	}
	return base
}

func setPlanEnv(env []string, key string, value string) []string {
	prefix := key + "="
	for i := range env {
		if strings.HasPrefix(env[i], prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func managedPythonPath(stateDir string) string {
	binDir := ManagedBinDir(stateDir)
	if runtime.GOOS == "windows" {
		return filepath.Join(binDir, "python.exe")
	}
	return filepath.Join(binDir, "python3")
}

func shellQuote(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			out = append(out, "''")
			continue
		}
		if strings.IndexFunc(part, isShellUnsafe) < 0 {
			out = append(out, part)
			continue
		}
		out = append(out, "'"+
			strings.ReplaceAll(part, "'", `'\''`)+"'")
	}
	return strings.Join(out, " ")
}

func isShellUnsafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return false
	case r >= 'A' && r <= 'Z':
		return false
	case r >= '0' && r <= '9':
		return false
	}
	switch r {
	case '-', '_', '.', '/', ':':
		return false
	default:
		return true
	}
}

func profileNames(profiles []Profile) []string {
	out := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, profile.Name)
	}
	return out
}
