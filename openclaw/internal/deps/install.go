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
	stepKindSystem   = "system"
	stepKindPython   = "python"
	stepKindVenv     = "venv"
	stepKindCommand  = "command"
	stepKindDownload = "download"

	venvFlagSystemSitePackages = "--system-site-packages"
)

const (
	stepStatusApplied  = "applied"
	stepStatusDeferred = "deferred"
	stepStatusFailed   = "failed"

	envGoBin          = "GOBIN"
	defaultToolsDir   = "tools"
	pythonInstallName = "Install Python packages"
)

type Step struct {
	Label           string            `json:"label"`
	Kind            string            `json:"kind"`
	Command         []string          `json:"command"`
	CommandLine     string            `json:"command_line"`
	RequiresRoot    bool              `json:"requires_root,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	EnsureDirs      []string          `json:"ensure_dirs,omitempty"`
	URL             string            `json:"url,omitempty"`
	TargetPath      string            `json:"target_path,omitempty"`
	Archive         string            `json:"archive,omitempty"`
	Extract         bool              `json:"extract,omitempty"`
	StripComponents int               `json:"strip_components,omitempty"`
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
	Status   string `json:"status,omitempty"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
	ExitCode int    `json:"exit_code"`
}

type sourceInstallAction struct {
	SourceName string
	Action     InstallAction
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
	pythonPkgs = append(
		pythonPkgs,
		collectInstallPythonPackages(
			report.Platform,
			sources,
			report.Missing,
		)...,
	)
	if len(pythonPkgs) > 0 {
		steps, err := pythonSteps(report.Toolchain, pythonPkgs)
		if err != nil {
			return Plan{}, err
		}
		plan.Steps = append(plan.Steps, steps...)
	}

	commandSteps, err := commandInstallSteps(
		report.Toolchain,
		report.Platform,
		sources,
		report.Missing,
	)
	if err != nil {
		return Plan{}, err
	}
	plan.Steps = append(plan.Steps, commandSteps...)

	downloadSteps, err := downloadInstallSteps(
		report.Toolchain,
		report.Platform,
		sources,
		report.Missing,
	)
	if err != nil {
		return Plan{}, err
	}
	plan.Steps = append(plan.Steps, downloadSteps...)

	systemPkgs := collectSystemPackages(manager, sources, report.Missing)
	if len(systemPkgs) > 0 {
		plan.Steps = append(plan.Steps, newSystemStep(manager, systemPkgs))
	}

	plan.Unresolved = unresolvedMissing(
		report.Platform,
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
		if step.Kind != stepKindDownload &&
			len(step.Command) == 0 {
			continue
		}
		if step.RequiresRoot && runtime.GOOS != "windows" &&
			os.Geteuid() != 0 {
			result.Steps = append(result.Steps, StepResult{
				Step:   step,
				Status: stepStatusDeferred,
				Error: fmt.Sprintf(
					"step %q requires root privileges; rerun as root "+
						"or execute the printed command with sudo",
					step.Label,
				),
			})
			continue
		}

		if err := ensureStepWorkingDirs(plan.Toolchain, step); err != nil {
			result.Steps = append(result.Steps, StepResult{
				Step:   step,
				Status: stepStatusFailed,
				Error:  err.Error(),
			})
			continue
		}

		out, exitCode, err := executePlanStep(ctx, plan.Toolchain, step)
		if err != nil {
			result.Steps = append(result.Steps, StepResult{
				Step:     step,
				Status:   stepStatusFailed,
				Output:   out,
				Error:    err.Error(),
				ExitCode: exitCode,
			})
			continue
		}
		result.Steps = append(result.Steps, StepResult{
			Step:     step,
			Status:   stepStatusApplied,
			Output:   out,
			ExitCode: exitCode,
		})
	}
	if hasStepStatus(result.Steps, stepStatusFailed) {
		return result, fmt.Errorf("one or more install steps failed")
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
	platform := Platform{
		GOOS:           runtime.GOOS,
		PackageManager: manager,
	}

	pkgs := map[string]struct{}{}
	for _, selected := range selectInstallActionsForMissing(
		platform,
		sources,
		missing,
		func(action InstallAction) bool {
			return action.Kind == manager
		},
	) {
		for _, pkg := range packagesForAction(selected.Action) {
			pkgs[pkg] = struct{}{}
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

func collectInstallPythonPackages(
	platform Platform,
	sources []Source,
	missing Missing,
) []string {
	actions := selectInstallActionsForMissing(
		platform,
		sources,
		missing,
		isPythonInstallKind,
	)

	pkgs := make([]string, 0, len(actions))
	for _, action := range actions {
		pkgs = append(pkgs, packagesForAction(action.Action)...)
	}
	return normalizeStrings(pkgs)
}

func commandInstallSteps(
	toolchain Toolchain,
	platform Platform,
	sources []Source,
	missing Missing,
) ([]Step, error) {
	actions := selectInstallActionsForMissing(
		platform,
		sources,
		missing,
		isCommandInstallKind,
	)
	steps := make([]Step, 0, len(actions))
	for _, action := range actions {
		step, err := commandInstallStep(toolchain, action.Action)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func downloadInstallSteps(
	toolchain Toolchain,
	platform Platform,
	sources []Source,
	missing Missing,
) ([]Step, error) {
	actions := selectInstallActionsForMissing(
		platform,
		sources,
		missing,
		func(action InstallAction) bool {
			return action.Kind == InstallKindDownload
		},
	)
	steps := make([]Step, 0, len(actions))
	for _, action := range actions {
		step, err := downloadInstallStep(
			toolchain,
			action.SourceName,
			action.Action,
		)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func selectInstallActionsForMissing(
	platform Platform,
	sources []Source,
	missing Missing,
	include func(InstallAction) bool,
) []sourceInstallAction {
	explicitBins := missingBinSet(Missing{Bins: missing.Bins})
	out := make([]sourceInstallAction, 0)
	seen := map[string]struct{}{}
	satisfiedGroups := map[string]struct{}{}
	for _, source := range sources {
		for _, action := range normalizeInstallActions(source.Install) {
			if !actionMatchesPlatform(action, platform.GOOS) {
				continue
			}
			if !include(action) {
				continue
			}
			if !actionNeeded(
				action,
				explicitBins,
				missing.AnyBins,
				satisfiedGroups,
			) {
				continue
			}
			key := installActionKey(action)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, sourceInstallAction{
				SourceName: source.Name,
				Action:     action,
			})
			markSatisfiedAnyBinGroups(
				action,
				missing.AnyBins,
				satisfiedGroups,
			)
		}
	}
	return out
}

func missingBinSet(missing Missing) map[string]struct{} {
	out := map[string]struct{}{}
	for _, name := range missing.Bins {
		out[name] = struct{}{}
	}
	for _, group := range missing.AnyBins {
		for _, name := range group {
			out[name] = struct{}{}
		}
	}
	return out
}

func actionNeeded(
	action InstallAction,
	explicitBins map[string]struct{},
	anyBins [][]string,
	satisfiedGroups map[string]struct{},
) bool {
	// Actions without declared bins cannot be filtered precisely, so
	// keep the conservative default and include them in the plan.
	if len(action.Bins) == 0 {
		return true
	}
	if coversAnyBin(action.Bins, explicitBins) {
		return true
	}
	for _, group := range anyBins {
		key := anyBinGroupKey(group)
		if _, ok := satisfiedGroups[key]; ok {
			continue
		}
		if actionCoversAnyGroupBin(action, group) {
			return true
		}
	}
	return false
}

func markSatisfiedAnyBinGroups(
	action InstallAction,
	anyBins [][]string,
	satisfiedGroups map[string]struct{},
) {
	for _, group := range anyBins {
		if !actionCoversAnyGroupBin(action, group) {
			continue
		}
		satisfiedGroups[anyBinGroupKey(group)] = struct{}{}
	}
}

func actionCoversAnyGroupBin(
	action InstallAction,
	group []string,
) bool {
	for _, name := range group {
		for _, bin := range action.Bins {
			if bin == name {
				return true
			}
		}
	}
	return false
}

func anyBinGroupKey(group []string) string {
	return strings.Join(group, "\x00")
}

func actionMatchesPlatform(
	action InstallAction,
	goos string,
) bool {
	if len(action.OS) == 0 {
		return true
	}
	goos = normalizeOSName(goos)
	for _, allowed := range action.OS {
		if normalizeOSName(allowed) == goos {
			return true
		}
	}
	return false
}

func installActionKey(action InstallAction) string {
	return strings.Join([]string{
		action.Kind,
		action.ID,
		action.Formula,
		action.Package,
		action.Module,
		action.URL,
		action.Archive,
		action.TargetDir,
		strings.Join(action.Packages, ","),
		strings.Join(action.Bins, ","),
		strings.Join(action.OS, ","),
	}, "\x00")
}

func isPythonInstallKind(action InstallAction) bool {
	switch action.Kind {
	case InstallKindPIP, InstallKindUV:
		return true
	default:
		return false
	}
}

func isCommandInstallKind(action InstallAction) bool {
	switch action.Kind {
	case InstallKindGo, InstallKindNode, InstallKindNPM:
		return true
	default:
		return false
	}
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
	platform Platform,
	sources []Source,
	missing Missing,
) Missing {
	coveredBins := map[string]struct{}{}
	for _, source := range sources {
		for _, action := range source.Install {
			normalized := normalizeInstallActions(
				[]InstallAction{action},
			)
			if len(normalized) == 0 {
				continue
			}
			action = normalized[0]
			if !isActionCoveredByPlanner(platform, action) {
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

func isActionCoveredByPlanner(
	platform Platform,
	action InstallAction,
) bool {
	if !actionMatchesPlatform(action, platform.GOOS) {
		return false
	}

	switch action.Kind {
	case platform.PackageManager:
		return true
	case InstallKindGo, InstallKindNode, InstallKindNPM:
		return true
	case InstallKindPIP, InstallKindUV:
		return true
	case InstallKindDownload:
		return true
	default:
		return false
	}
}

func packagesForAction(action InstallAction) []string {
	switch action.Kind {
	case InstallKindBrew:
		if action.Formula != "" {
			return []string{brewPackageName(action)}
		}
	case InstallKindGo:
		if action.Module != "" {
			return []string{action.Module}
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

func brewPackageName(action InstallAction) string {
	formula := strings.TrimSpace(action.Formula)
	tap := strings.TrimSpace(action.Tap)
	if tap == "" || formula == "" || strings.Contains(formula, "/") {
		return formula
	}
	return tap + "/" + formula
}

func commandInstallStep(
	toolchain Toolchain,
	action InstallAction,
) (Step, error) {
	switch action.Kind {
	case InstallKindGo:
		return goInstallStep(toolchain, action)
	case InstallKindNode, InstallKindNPM:
		return npmInstallStep(toolchain, action)
	default:
		return Step{}, fmt.Errorf(
			"unsupported command install kind %q",
			action.Kind,
		)
	}
}

func goInstallStep(
	toolchain Toolchain,
	action InstallAction,
) (Step, error) {
	module := strings.TrimSpace(action.Module)
	if module == "" {
		module = strings.TrimSpace(action.Package)
	}
	if module == "" {
		return Step{}, fmt.Errorf(
			"go install action %q is missing module",
			action.Label,
		)
	}

	binDir := ManagedBinDir(toolchain.StateDir)
	command := []string{"go", "install", module}
	return Step{
		Label:       actionLabel(action, "Install Go tool"),
		Kind:        stepKindCommand,
		Command:     command,
		CommandLine: shellQuote(command...),
		EnsureDirs:  []string{binDir},
		Env: map[string]string{
			envGoBin: binDir,
		},
	}, nil
}

func npmInstallStep(
	toolchain Toolchain,
	action InstallAction,
) (Step, error) {
	packages := packagesForAction(action)
	if len(packages) == 0 {
		return Step{}, fmt.Errorf(
			"npm install action %q is missing package",
			action.Label,
		)
	}

	prefix := ManagedToolPrefix(toolchain.StateDir)
	command := append(
		[]string{"npm", "install", "-g", "--prefix", prefix},
		packages...,
	)
	return Step{
		Label:       actionLabel(action, "Install npm package"),
		Kind:        stepKindCommand,
		Command:     command,
		CommandLine: shellQuote(command...),
		EnsureDirs:  []string{prefix},
	}, nil
}

func actionLabel(action InstallAction, fallback string) string {
	if strings.TrimSpace(action.Label) != "" {
		return strings.TrimSpace(action.Label)
	}
	return fallback
}

func executePlanStep(
	ctx context.Context,
	toolchain Toolchain,
	step Step,
) (string, int, error) {
	if step.Kind == stepKindDownload {
		out, err := executeDownloadStep(ctx, step)
		if err != nil {
			return out, -1, err
		}
		return out, 0, nil
	}

	cmd, err := planStepCommand(toolchain, step)
	if err != nil {
		return "", 0, fmt.Errorf(
			"step %q is invalid: %w",
			step.Label,
			err,
		)
	}

	out, err := combinedOutputContext(ctx, cmd)
	exitCode := 0
	if err == nil {
		return string(out), exitCode, nil
	}

	exitCode = -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
		exitCode = exitErr.ProcessState.ExitCode()
	}
	return string(out), exitCode, err
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

func ensureStepWorkingDirs(
	toolchain Toolchain,
	step Step,
) error {
	switch step.Kind {
	case stepKindCommand:
		dirs := normalizeStrings(step.EnsureDirs)
		for _, dir := range dirs {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		if len(dirs) > 0 {
			return nil
		}
		if dir := strings.TrimSpace(step.Env[envGoBin]); dir != "" {
			return os.MkdirAll(dir, 0o755)
		}
		if len(step.Command) >= 6 &&
			commandBase(step.Command[0]) == "npm" &&
			step.Command[1] == "install" &&
			step.Command[2] == "-g" &&
			step.Command[3] == "--prefix" {
			return os.MkdirAll(step.Command[4], 0o755)
		}
	case stepKindPython:
		if strings.TrimSpace(toolchain.StateDir) != "" &&
			toolchain.Python.Managed {
			return os.MkdirAll(
				ManagedBinDir(toolchain.StateDir),
				0o755,
			)
		}
	case stepKindDownload:
		target := strings.TrimSpace(step.TargetPath)
		if target == "" {
			return fmt.Errorf("download step %q has empty target", step.Label)
		}
		if step.Extract {
			return os.MkdirAll(target, 0o755)
		}
		return os.MkdirAll(filepath.Dir(target), 0o755)
	}
	return nil
}

func hasStepStatus(
	steps []StepResult,
	status string,
) bool {
	for _, step := range steps {
		if step.Status == status {
			return true
		}
	}
	return false
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
