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
	"path/filepath"

	tcontainer "github.com/docker/docker/api/types/container"

	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
)

const (
	// SandboxContainerImage is the default production sandbox image (Go + bash).
	SandboxContainerImage = "golang:1.24-bookworm"
)

func newContainerCodeExecutor(skillsRoot string) (*containerexec.CodeExecutor, error) {
	abs, err := filepath.Abs(skillsRoot)
	if err != nil {
		return nil, err
	}
	return containerexec.New(
		containerexec.WithBindMount(abs, "/opt/trpc-agent/skills", "ro"),
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

func asWorkspaceExecutor(ex any) (workspaceExecutor, error) {
	exec, ok := ex.(workspaceExecutor)
	if !ok {
		return nil, fmt.Errorf("runtime does not support workspace execution")
	}
	return exec, nil
}
