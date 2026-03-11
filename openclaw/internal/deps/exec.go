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
	"strings"
)

const (
	pythonCommand    = "python"
	python3Command   = "python3"
	pythonExeCommand = "python.exe"
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
	default:
		return nil, fmt.Errorf("unsupported step kind %q", step.Kind)
	}
	if err != nil {
		return nil, err
	}
	cmd = newExecCommand(spec, step.Command[1:]...)
	cmd.Env = env
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
	name := commandBase(command)
	switch {
	case name == pythonCommand:
		return resolveExecutable(command, pythonCommand)
	case name == python3Command:
		return resolveExecutable(command, python3Command)
	case strings.EqualFold(name, pythonExeCommand):
		return resolveExecutable(command, pythonExeCommand)
	default:
		return executableSpec{}, fmt.Errorf(
			"unsupported python executable %q",
			command,
		)
	}
}

func systemCommandSpec(manager string) (executableSpec, error) {
	switch strings.ToLower(commandBase(manager)) {
	case InstallKindAPT:
		return resolveExecutable("", InstallKindAPT)
	case InstallKindBrew:
		return resolveExecutable("", InstallKindBrew)
	case InstallKindDNF:
		return resolveExecutable("", InstallKindDNF)
	case InstallKindYUM:
		return resolveExecutable("", InstallKindYUM)
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
) (executableSpec, error) {
	command = strings.TrimSpace(command)
	if command != "" && commandDir(command) != "" {
		path, err := filepath.Abs(filepath.Clean(command))
		if err != nil {
			return executableSpec{}, err
		}
		return executableSpec{path: path}, nil
	}

	path, err := exec.LookPath(fallback)
	if err != nil {
		return executableSpec{}, err
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
