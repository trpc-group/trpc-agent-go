//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tcontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/strslice"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

const defaultReviewContainerImage = "golang:1.24.4-bookworm"

func createSandbox(
	runtimeName string,
	allowLocal bool,
	containerImage string,
) (ReviewSandbox, error) {
	switch strings.ToLower(strings.TrimSpace(runtimeName)) {
	case "fake":
		return &FakeSandbox{}, nil
	case "local":
		if !allowLocal {
			return nil, errors.New(
				"local runtime requires --allow-local and is development-only",
			)
		}
		return createLocalSandbox()
	case "container", "":
		return createContainerSandbox(containerImage)
	default:
		return nil, fmt.Errorf("unsupported runtime %q", runtimeName)
	}
}

func createContainerSandbox(containerImage string) (ReviewSandbox, error) {
	if strings.TrimSpace(containerImage) == "" {
		containerImage = defaultReviewContainerImage
	}
	workspaceRoot, err := os.MkdirTemp("", "trpc-review-container-*")
	if err != nil {
		return nil, fmt.Errorf("create container workspace root: %w", err)
	}
	pidsLimit := int64(128)
	hostConfig := tcontainer.HostConfig{
		AutoRemove:     true,
		Privileged:     false,
		NetworkMode:    "none",
		ReadonlyRootfs: true,
		CapDrop:        strslice.StrSlice{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		Tmpfs: map[string]string{
			"/tmp": "rw,exec,nosuid,nodev,size=768m,mode=1777",
		},
		Resources: tcontainer.Resources{
			Memory: 1024 << 20, NanoCPUs: 2_000_000_000,
			PidsLimit: &pidsLimit,
		},
	}
	hostConfig.Binds = append(
		hostConfig.Binds, workspaceRoot+":/tmp/run:rw",
	)
	env := sandboxEnvironment("/go/pkg/mod")
	if moduleCache := hostGoModuleCache(); moduleCache != "" {
		hostConfig.Binds = append(
			hostConfig.Binds, moduleCache+":/go/pkg/mod:ro",
		)
	}
	executor, err := containerexec.New(
		containerexec.WithContainerConfig(tcontainer.Config{
			Image: containerImage, WorkingDir: "/",
			Cmd: []string{"tail", "-f", "/dev/null"},
			Tty: true, OpenStdin: true,
			User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		}),
		containerexec.WithHostConfig(hostConfig),
	)
	if err != nil {
		_ = os.RemoveAll(workspaceRoot)
		return nil, fmt.Errorf("create container executor: %w", err)
	}
	engine := executor.Engine()
	if engine == nil {
		_ = executor.Close()
		_ = os.RemoveAll(workspaceRoot)
		return nil, errors.New("container executor did not expose an engine")
	}
	closeFn := func() error {
		closeErr := executor.Close()
		removeErr := os.RemoveAll(workspaceRoot)
		return errors.Join(closeErr, removeErr)
	}
	return NewWorkspaceSandbox(engine, closeFn, env)
}

func createLocalSandbox() (ReviewSandbox, error) {
	root, err := os.MkdirTemp("", "trpc-review-local-*")
	if err != nil {
		return nil, fmt.Errorf("create local workspace root: %w", err)
	}
	executor := localexec.New(
		localexec.WithWorkDir(root),
		localexec.WithWorkspaceMode(localexec.WorkspaceModeIsolated),
	)
	engineProvider, ok := any(executor).(codeexecutor.EngineProvider)
	if !ok {
		_ = os.RemoveAll(root)
		return nil, errors.New("local executor did not expose an engine")
	}
	engine := engineProvider.Engine()
	closeFn := func() error {
		return os.RemoveAll(root)
	}
	moduleCache := hostGoModuleCache()
	return NewWorkspaceSandbox(
		engine, closeFn, sandboxEnvironment(moduleCache),
	)
}

func sandboxEnvironment(moduleCache string) map[string]string {
	env := map[string]string{
		"HOME":        "/tmp/code-review-home",
		"GOCACHE":     "/tmp/code-review-go-cache",
		"PATH":        "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"CGO_ENABLED": "0",
		"GOFLAGS":     "-p=4",
		"GOMAXPROCS":  "4",
	}
	if moduleCache != "" {
		env["GOMODCACHE"] = moduleCache
	}
	return env
}

func hostGoModuleCache() string {
	command := exec.Command("go", "env", "GOMODCACHE")
	output, err := command.Output()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if info, err := os.Stat(absolute); err != nil || !info.IsDir() {
		return ""
	}
	return absolute
}
