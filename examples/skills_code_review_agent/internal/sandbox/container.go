//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"

	tcontainer "github.com/docker/docker/api/types/container"

	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
)

const (
	// SandboxContainerImage is the tagged image built from docker/Dockerfile
	// (Go + bash + python3 required by containerexec.New).
	SandboxContainerImage = "trpc-agent-go/cr-sandbox:1.24"
)

func newContainerCodeExecutor(skillsRoot string) (*containerexec.CodeExecutor, error) {
	absSkills, err := filepath.Abs(skillsRoot)
	if err != nil {
		return nil, err
	}
	dockerDir, err := resolveDockerDir()
	if err != nil {
		return nil, err
	}
	return containerexec.New(
		containerexec.WithDockerFilePath(dockerDir),
		containerexec.WithBindMount(absSkills, "/opt/trpc-agent/skills", "ro"),
		containerexec.WithAutoInputs(true),
		containerexec.WithContainerConfig(tcontainer.Config{
			Image:      SandboxContainerImage,
			WorkingDir: "/",
			Cmd:        []string{"tail", "-f", "/dev/null"},
			Tty:        true,
			OpenStdin:  true,
		}),
	)
}

func resolveDockerDir() (string, error) {
	candidates := []string{
		"docker",
		filepath.Join("examples", "skills_code_review_agent", "docker"),
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, "docker"),
			filepath.Join(cwd, "examples", "skills_code_review_agent", "docker"),
		)
		for dir := cwd; ; dir = filepath.Dir(dir) {
			candidates = append(candidates, filepath.Join(dir, "examples", "skills_code_review_agent", "docker"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	for _, c := range candidates {
		if info, err := os.Stat(filepath.Join(c, "Dockerfile")); err == nil && !info.IsDir() {
			abs, err := filepath.Abs(c)
			if err != nil {
				return "", err
			}
			return abs, nil
		}
	}
	return "", fmt.Errorf("docker/Dockerfile for CR sandbox image not found")
}

func asWorkspaceExecutor(ex any) (workspaceExecutor, error) {
	exec, ok := ex.(workspaceExecutor)
	if !ok {
		return nil, fmt.Errorf("runtime does not support workspace execution")
	}
	return exec, nil
}
