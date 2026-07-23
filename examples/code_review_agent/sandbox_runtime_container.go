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
	goModCache := ""
	if opts.RepoPath != "" {
		goModCache = hostGoModCache()
	}
	if opts.DryRun {
		return &engineRunner{
			runtime:     "container",
			timeout:     timeout,
			outputLimit: outputLimit,
			repoPath:    opts.RepoPath,
			skillsRoot:  opts.SkillsRoot,
			dryRun:      true,
			goModCache:  goModCache,
		}, nil
	}
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
		goModCache:  goModCache,
	}, nil
}

func newE2BRunner(opts ReviewOptions, timeout time.Duration, outputLimit int64) (SandboxRunner, error) {
	goModCache := ""
	if opts.RepoPath != "" {
		goModCache = hostGoModCache()
	}
	lifetime := totalSandboxLifetime(timeout, reviewCommands(opts))
	if opts.DryRun {
		return &engineRunner{
			runtime:     "e2b",
			timeout:     timeout,
			outputLimit: outputLimit,
			repoPath:    opts.RepoPath,
			skillsRoot:  opts.SkillsRoot,
			dryRun:      true,
			goModCache:  goModCache,
		}, nil
	}
	exec, err := e2bexec.New(
		e2bexec.WithExecutionTimeout(timeout),
		e2bexec.WithSandboxTimeout(lifetime),
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
		goModCache:  goModCache,
	}, nil
}

func totalSandboxLifetime(timeout time.Duration, commands []string) time.Duration {
	if timeout <= 0 {
		return 0
	}
	commandCount := len(commands)
	if commandCount == 0 {
		commandCount = 1
	}
	const perCommandOverhead = 10 * time.Second
	const setupAndCleanupBuffer = 30 * time.Second
	return time.Duration(commandCount)*timeout +
		time.Duration(commandCount+1)*perCommandOverhead +
		setupAndCleanupBuffer
}
