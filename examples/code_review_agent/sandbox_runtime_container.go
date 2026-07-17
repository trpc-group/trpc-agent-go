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
	"path/filepath"
	"time"

	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
)

func newContainerRunner(opts ReviewOptions, timeout time.Duration, outputLimit int64) (SandboxRunner, error) {
	dockerPath := filepath.Join("code_review_agent", "sandbox")
	exec, err := containerexec.New(containerexec.WithDockerFilePath(dockerPath))
	if err != nil {
		return nil, err
	}
	return &engineRunner{
		runtime:     "container",
		exec:        exec,
		timeout:     timeout,
		outputLimit: outputLimit,
		repoPath:    opts.RepoPath,
		skillsRoot:  opts.SkillsRoot,
		dryRun:      opts.DryRun,
	}, nil
}

func newE2BRunner(opts ReviewOptions, timeout time.Duration, outputLimit int64) (SandboxRunner, error) {
	exec, err := e2bexec.New(
		e2bexec.WithExecutionTimeout(timeout),
		e2bexec.WithSandboxTimeout(timeout*2),
	)
	if err != nil {
		return nil, err
	}
	return &engineRunner{
		runtime:     "e2b",
		exec:        exec,
		timeout:     timeout,
		outputLimit: outputLimit,
		repoPath:    opts.RepoPath,
		skillsRoot:  opts.SkillsRoot,
		dryRun:      opts.DryRun,
	}, nil
}
