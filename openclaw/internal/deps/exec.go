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
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type executableSpec struct {
	path string
}

func planStepCommand(
	toolchain Toolchain,
	step Step,
) (*exec.Cmd, error) {
	if len(step.Command) == 0 {
		return nil, fmt.Errorf("empty step command")
	}

	var (
		spec executableSpec
		env  []string
		err  error
		cmd  *exec.Cmd
	)
	switch step.Kind {
	case stepKindSystem:
		spec, err = systemCommandSpec(step.Command[0])
		env = os.Environ()
	case stepKindPython, stepKindVenv:
		spec, err = pythonCommandSpec(step.Command[0])
		env = mergedPlanEnv(toolchain)
	case stepKindCommand:
		env = mergedPlanEnv(toolchain)
		spec, err = resolveExecutable(
			step.Command[0],
			"",
			envValue(env, envPath),
		)
	default:
		return nil, fmt.Errorf("unsupported step kind %q", step.Kind)
	}
	if err != nil {
		return nil, err
	}
	cmd = newExecCommand(spec, step.Command[1:]...)
	cmd.Env = mergeStepEnv(env, step.Env)
	return cmd, nil
}

func pythonExecCommand(
	pythonPath string,
	args ...string,
) (*exec.Cmd, error) {
	spec, err := pythonCommandSpec(pythonPath)
	if err != nil {
		return nil, err
	}
	cmd := newExecCommand(spec, args...)
	cmd.Env = os.Environ()
	return cmd, nil
}

func combinedOutputContext(
	ctx context.Context,
	cmd *exec.Cmd,
) ([]byte, error) {
	if cmd == nil {
		return nil, fmt.Errorf("nil command")
	}
	if ctx == nil {
		return cmd.CombinedOutput()
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		return out.Bytes(), err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return out.Bytes(), err
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return out.Bytes(), ctx.Err()
	}
}

func pythonCommandSpec(command string) (executableSpec, error) {
	return resolveExecutable(command, "", "")
}

func systemCommandSpec(manager string) (executableSpec, error) {
	name := strings.ToLower(commandBase(manager))
	switch name {
	case InstallKindAPT:
		return resolveExecutable(manager, name, "")
	case InstallKindBrew:
		return resolveExecutable(manager, name, "")
	case InstallKindDNF:
		return resolveExecutable(manager, name, "")
	case InstallKindYUM:
		return resolveExecutable(manager, name, "")
	default:
		return executableSpec{}, fmt.Errorf(
			"unsupported package manager %q",
			manager,
		)
	}
}

func resolveExecutable(
	command string,
	fallback string,
	searchPath string,
) (executableSpec, error) {
	name := strings.TrimSpace(command)
	if name == "" {
		name = strings.TrimSpace(fallback)
	}
	if name == "" {
		return executableSpec{}, fmt.Errorf("empty executable name")
	}

	if commandDir(name) != "" {
		path, err := filepath.Abs(filepath.Clean(name))
		if err != nil {
			return executableSpec{}, err
		}
		return validateExecutablePath(path)
	}

	path, err := lookPath(name, searchPath)
	if err != nil {
		return executableSpec{}, err
	}
	return validateExecutablePath(path)
}

func lookPath(
	name string,
	searchPath string,
) (string, error) {
	if strings.TrimSpace(searchPath) == "" {
		return exec.LookPath(name)
	}
	for _, dir := range filepath.SplitList(searchPath) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if runtime.GOOS == "windows" {
			for _, ext := range []string{"", ".exe", ".cmd", ".bat"} {
				if _, err := os.Stat(candidate + ext); err == nil {
					return candidate + ext, nil
				}
			}
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return exec.LookPath(name)
}

func validateExecutablePath(path string) (executableSpec, error) {
	info, err := os.Stat(path)
	if err != nil {
		return executableSpec{}, err
	}
	if info.IsDir() {
		return executableSpec{}, fmt.Errorf("%q is a directory", path)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return executableSpec{}, fmt.Errorf("%q is not executable", path)
	}
	return executableSpec{path: path}, nil
}

func newExecCommand(
	spec executableSpec,
	args ...string,
) *exec.Cmd {
	return &exec.Cmd{
		Path: spec.path,
		Args: append([]string{spec.path}, args...),
	}
}

func mergeStepEnv(
	base []string,
	overrides map[string]string,
) []string {
	if len(overrides) == 0 {
		return base
	}
	out := append([]string(nil), base...)
	for key, value := range overrides {
		out = setPlanEnv(out, key, value)
	}
	return out
}

func envValue(
	env []string,
	key string,
) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func commandDir(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	dir := filepath.Dir(filepath.Clean(command))
	if dir == "." {
		return ""
	}
	return dir
}

func commandBase(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(command))
}
