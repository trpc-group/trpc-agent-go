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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	defaultToolchainDir = "toolchain"
	defaultPythonEnvDir = "python"

	envPath                = "PATH"
	envVirtualEnv          = "VIRTUAL_ENV"
	envOpenClawToolchain   = "OPENCLAW_TOOLCHAIN_ROOT"
	envOpenClawPython      = "OPENCLAW_TOOLCHAIN_PYTHON"
	envPipDisableVersion   = "PIP_DISABLE_PIP_VERSION_CHECK"
	pipDisableVersionValue = "1"
)

type Platform struct {
	GOOS           string `json:"goos"`
	GOARCH         string `json:"goarch"`
	PackageManager string `json:"package_manager,omitempty"`
}

type PythonRuntime struct {
	Found     bool   `json:"found"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Managed   bool   `json:"managed"`
	EnvRoot   string `json:"env_root,omitempty"`
	Bootstrap string `json:"bootstrap,omitempty"`
}

type Toolchain struct {
	StateDir string        `json:"state_dir,omitempty"`
	Root     string        `json:"root,omitempty"`
	BinDir   string        `json:"bin_dir,omitempty"`
	Active   bool          `json:"active"`
	Python   PythonRuntime `json:"python"`
}

type BinStatus struct {
	Name  string `json:"name"`
	Found bool   `json:"found"`
	Path  string `json:"path,omitempty"`
}

type AnyBinStatus struct {
	Names     []string    `json:"names"`
	Satisfied bool        `json:"satisfied"`
	Found     []BinStatus `json:"found,omitempty"`
}

type PythonStatus struct {
	Module  string `json:"module"`
	Package string `json:"package,omitempty"`
	Found   bool   `json:"found"`
}

type SourceReport struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Bins        []BinStatus     `json:"bins,omitempty"`
	AnyBins     []AnyBinStatus  `json:"any_bins,omitempty"`
	Python      []PythonStatus  `json:"python,omitempty"`
	Install     []InstallAction `json:"install,omitempty"`
}

type Missing struct {
	Bins    []string        `json:"bins,omitempty"`
	AnyBins [][]string      `json:"any_bins,omitempty"`
	Python  []PythonPackage `json:"python,omitempty"`
}

type Report struct {
	Platform  Platform       `json:"platform"`
	Toolchain Toolchain      `json:"toolchain"`
	Sources   []SourceReport `json:"sources,omitempty"`
	Missing   Missing        `json:"missing,omitempty"`
}

func Inspect(stateDir string, sources []Source) (Report, error) {
	return inspect(stateDir, sources, true)
}

func InspectStartup(
	stateDir string,
	sources []Source,
) (Report, error) {
	return inspect(stateDir, sources, false)
}

func inspect(
	stateDir string,
	sources []Source,
	includePython bool,
) (Report, error) {
	sources = MergeSources(sources...)

	toolchain := DetectToolchain(stateDir)
	platform := Platform{
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		PackageManager: DetectPackageManager(),
	}
	report := Report{
		Platform:  platform,
		Toolchain: toolchain,
		Sources:   make([]SourceReport, 0, len(sources)),
	}

	for _, source := range sources {
		sourceReport, missing, err := inspectSource(
			toolchain,
			source,
			includePython,
		)
		if err != nil {
			return Report{}, err
		}
		report.Sources = append(report.Sources, sourceReport)
		report.Missing = mergeMissing(report.Missing, missing)
	}
	return report, nil
}

func InspectProfiles(
	stateDir string,
	profiles []string,
) (Report, error) {
	sources, err := SourcesForProfiles(profiles)
	if err != nil {
		return Report{}, err
	}
	return Inspect(stateDir, sources)
}

func DetectToolchain(stateDir string) Toolchain {
	stateDir = strings.TrimSpace(stateDir)
	root := ManagedPythonRoot(stateDir)
	binDir := ManagedBinDir(stateDir)
	active := dirExists(binDir)

	python := FindPythonRuntime(stateDir)
	return Toolchain{
		StateDir: stateDir,
		Root:     root,
		BinDir:   binDir,
		Active:   active,
		Python:   python,
	}
}

func ManagedToolchainRoot(stateDir string) string {
	if strings.TrimSpace(stateDir) == "" {
		return ""
	}
	return filepath.Join(
		strings.TrimSpace(stateDir),
		defaultToolchainDir,
	)
}

func ManagedPythonRoot(stateDir string) string {
	root := ManagedToolchainRoot(stateDir)
	if root == "" {
		return ""
	}
	return filepath.Join(root, defaultPythonEnvDir)
}

func ManagedBinDir(stateDir string) string {
	root := ManagedPythonRoot(stateDir)
	if root == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(root, "Scripts")
	}
	return filepath.Join(root, "bin")
}

func ManagedPythonCandidates(stateDir string) []string {
	binDir := ManagedBinDir(stateDir)
	if binDir == "" {
		return nil
	}
	names := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		names = []string{"python.exe"}
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, filepath.Join(binDir, name))
	}
	return out
}

func ToolEnv(stateDir string) map[string]string {
	binDir := ManagedBinDir(stateDir)
	if !dirExists(binDir) {
		return nil
	}

	pathValue := prependPath(binDir, os.Getenv(envPath))
	out := map[string]string{
		envPath:              pathValue,
		envVirtualEnv:        ManagedPythonRoot(stateDir),
		envOpenClawToolchain: ManagedToolchainRoot(stateDir),
		envOpenClawPython:    filepath.Join(binDir, "python3"),
		envPipDisableVersion: pipDisableVersionValue,
	}
	if runtime.GOOS == "windows" {
		out[envOpenClawPython] = filepath.Join(binDir, "python.exe")
	}
	return out
}

func FindPythonRuntime(stateDir string) PythonRuntime {
	managedCandidates := ManagedPythonCandidates(stateDir)
	for _, candidate := range managedCandidates {
		if !fileExists(candidate) {
			continue
		}
		version := pythonVersion(candidate)
		return PythonRuntime{
			Found:     true,
			Path:      candidate,
			Version:   version,
			Managed:   true,
			EnvRoot:   ManagedPythonRoot(stateDir),
			Bootstrap: systemPythonCandidate(),
		}
	}

	system := systemPythonCandidate()
	if system == "" {
		return PythonRuntime{
			Bootstrap: "",
			EnvRoot:   ManagedPythonRoot(stateDir),
		}
	}
	return PythonRuntime{
		Found:     true,
		Path:      system,
		Version:   pythonVersion(system),
		Managed:   false,
		EnvRoot:   ManagedPythonRoot(stateDir),
		Bootstrap: system,
	}
}

func DetectPackageManager() string {
	for _, manager := range []string{
		InstallKindBrew,
		InstallKindAPT,
		InstallKindDNF,
		InstallKindYUM,
	} {
		if _, err := exec.LookPath(manager); err == nil {
			return manager
		}
	}
	return ""
}

func CheckPythonPackages(
	python PythonRuntime,
	pkgs []PythonPackage,
) ([]PythonStatus, error) {
	pkgs = normalizePythonPackages(pkgs)
	if len(pkgs) == 0 {
		return nil, nil
	}

	out := make([]PythonStatus, 0, len(pkgs))
	for _, pkg := range pkgs {
		out = append(out, PythonStatus{
			Module:  pkg.Module,
			Package: pkg.Package,
			Found:   false,
		})
	}
	if !python.Found {
		return out, nil
	}

	modules := make([]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		modules = append(modules, pkg.Module)
	}
	script := strings.Join([]string{
		"import importlib.util",
		"import json",
		"import sys",
		"mods = json.loads(sys.argv[1])",
		"print(json.dumps({m: importlib.util.find_spec(m) is not None " +
			"for m in mods}))",
	}, "; ")
	rawMods, err := json.Marshal(modules)
	if err != nil {
		return nil, err
	}
	cmd, err := pythonExecCommand(
		python.Path,
		"-c",
		script,
		string(rawMods),
	)
	if err != nil {
		return out, fmt.Errorf(
			"build python package check command: %w",
			err,
		)
	}
	outBytes, err := cmd.CombinedOutput()
	if err != nil {
		return out, nil
	}

	found := map[string]bool{}
	if err := json.Unmarshal(outBytes, &found); err != nil {
		return out, nil
	}
	for i := range out {
		out[i].Found = found[out[i].Module]
	}
	return out, nil
}

func inspectSource(
	toolchain Toolchain,
	source Source,
	includePython bool,
) (SourceReport, Missing, error) {
	source = normalizeSource(source)
	report := SourceReport{
		Name:        source.Name,
		Description: source.Description,
		Install:     append([]InstallAction(nil), source.Install...),
	}
	var missing Missing

	for _, name := range source.Requires.Bins {
		status := checkBin(name)
		report.Bins = append(report.Bins, status)
		if !status.Found {
			missing.Bins = append(missing.Bins, status.Name)
		}
	}

	if len(source.Requires.AnyBins) > 0 {
		any := AnyBinStatus{
			Names: append([]string(nil), source.Requires.AnyBins...),
		}
		for _, name := range source.Requires.AnyBins {
			status := checkBin(name)
			if status.Found {
				any.Found = append(any.Found, status)
			}
		}
		any.Satisfied = len(any.Found) > 0
		report.AnyBins = append(report.AnyBins, any)
		if !any.Satisfied {
			missing.AnyBins = append(
				missing.AnyBins,
				append([]string(nil), any.Names...),
			)
		}
	}

	if includePython {
		python, err := CheckPythonPackages(
			toolchain.Python,
			source.Requires.Python,
		)
		if err != nil {
			return SourceReport{}, Missing{}, err
		}
		report.Python = python
		for i, status := range python {
			if status.Found {
				continue
			}
			missing.Python = append(
				missing.Python,
				source.Requires.Python[i],
			)
		}
	}
	return report, normalizeMissing(missing), nil
}

func checkBin(name string) BinStatus {
	name = strings.TrimSpace(name)
	if name == "" {
		return BinStatus{}
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return BinStatus{Name: name}
	}
	return BinStatus{
		Name:  name,
		Found: true,
		Path:  path,
	}
}

func mergeMissing(left, right Missing) Missing {
	left.Bins = append(left.Bins, right.Bins...)
	left.AnyBins = append(left.AnyBins, right.AnyBins...)
	left.Python = append(left.Python, right.Python...)
	return normalizeMissing(left)
}

func normalizeMissing(m Missing) Missing {
	m.Bins = normalizeStrings(m.Bins)
	m.Python = normalizePythonPackages(m.Python)

	seenAny := map[string]struct{}{}
	outAny := make([][]string, 0, len(m.AnyBins))
	for _, group := range m.AnyBins {
		group = normalizeStrings(group)
		if len(group) == 0 {
			continue
		}
		key := strings.Join(group, "\x00")
		if _, ok := seenAny[key]; ok {
			continue
		}
		seenAny[key] = struct{}{}
		outAny = append(outAny, group)
	}
	m.AnyBins = outAny
	return m
}

func HasMissing(report Report) bool {
	return len(report.Missing.Bins) > 0 ||
		len(report.Missing.AnyBins) > 0 ||
		len(report.Missing.Python) > 0
}

func pythonVersion(path string) string {
	script := "import sys; print(sys.version.split()[0])"
	cmd, err := pythonExecCommand(path, "-c", script)
	if err != nil {
		return ""
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func systemPythonCandidate() string {
	for _, name := range []string{"python3", "python"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return path
		}
	}
	if runtime.GOOS == "windows" {
		path, err := exec.LookPath("python.exe")
		if err == nil {
			return path
		}
	}
	return ""
}

func prependPath(prefix string, current string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return current
	}
	if current == "" {
		return prefix
	}
	return prefix + string(os.PathListSeparator) + current
}

func dirExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

var errPythonNotFound = errors.New("python interpreter not found")
