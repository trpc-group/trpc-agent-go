//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
)

// ContainerRuntime adapts the public container workspace engine.
type ContainerRuntime struct {
	engine codeexecutor.Engine
	ws     codeexecutor.Workspace
	close  func() error
}

// NewContainerRuntime creates the default isolated container runtime.
func NewContainerRuntime(dockerfile string) (*ContainerRuntime, error) {
	hostCfg := dockercontainer.HostConfig{
		AutoRemove:     true,
		Privileged:     false,
		NetworkMode:    "none",
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"},
		Resources:      dockercontainer.Resources{Memory: 512 << 20, NanoCPUs: 1_000_000_000, PidsLimit: ptrInt64(128)},
		ReadonlyRootfs: false,
	}
	ce, err := container.New(
		container.WithDockerFilePath(dockerfile),
		container.WithHostConfig(hostCfg),
	)
	if err != nil {
		return nil, err
	}
	engine := ce.Engine()
	if engine == nil {
		_ = ce.Close()
		return nil, fmt.Errorf("container engine unavailable")
	}
	return &ContainerRuntime{engine: engine, close: ce.Close}, nil
}

// Stage creates a workspace and stages the snapshot read-only.
func (r *ContainerRuntime) Stage(ctx context.Context, snap Snapshot) error {
	ws, err := r.engine.Manager().CreateWorkspace(ctx, "code-review", codeexecutor.WorkspacePolicy{Isolated: true, MaxDiskBytes: 128 << 20})
	if err != nil {
		return err
	}
	r.ws = ws
	if snap.Path == "" {
		if err := r.stageDirectoryFiles(ctx, snap.SkillPath, "skills/code-review"); err != nil {
			return err
		}
		return r.fixReadOnlyTree(ctx, "skills/code-review")
	}
	if err := r.stageDirectoryFiles(ctx, snap.Path, "work/repo"); err != nil {
		return err
	}
	if err := r.fixReadOnlyTree(ctx, "work/repo"); err != nil {
		return err
	}
	if snap.SkillPath != "" {
		if err := r.stageDirectoryFiles(ctx, snap.SkillPath, "skills/code-review"); err != nil {
			return err
		}
		return r.fixReadOnlyTree(ctx, "skills/code-review")
	}
	return nil
}

func (r *ContainerRuntime) stageDirectoryFiles(ctx context.Context, src, to string) error {
	files, err := putFilesFromDirectory(src, to)
	if err != nil {
		return err
	}
	return r.engine.FS().PutFiles(ctx, r.ws, files)
}

func (r *ContainerRuntime) fixReadOnlyTree(ctx context.Context, rel string) error {
	res, err := r.engine.Runner().RunProgram(ctx, r.ws, containerReadOnlyFixupSpec(rel))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("normalize staged permissions for %s: %s", rel, cleanOutput(res.Stderr))
	}
	return nil
}

// Run executes the bundled script through the workspace engine.
func (r *ContainerRuntime) Run(ctx context.Context, cmd Command) (Result, error) {
	if r.ws.Path == "" {
		return Result{}, fmt.Errorf("snapshot is not staged")
	}
	timeout := cmd.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	cwd := cmd.Cwd
	if cwd == "" {
		cwd = "work/repo"
	}
	start := time.Now()
	res, err := r.engine.Runner().RunProgram(ctx, r.ws, codeexecutor.RunProgramSpec{
		Cmd:      containerScriptPath(cwd),
		Args:     cmd.Args,
		Cwd:      cwd,
		CleanEnv: true,
		Timeout:  timeout,
		Env:      map[string]string{"PATH": "/usr/local/go/bin:/usr/bin:/bin", "HOME": "/tmp"},
		Limits:   codeexecutor.ResourceLimits{CPUPercent: 100, MemoryMB: 512, MaxPIDs: 128},
	})
	if err != nil {
		return Result{}, err
	}
	out := Result{CommandID: cmd.ID, Stdout: cleanOutput(res.Stdout), Stderr: cleanOutput(res.Stderr), ExitCode: res.ExitCode, TimedOut: res.TimedOut, DurationMS: time.Since(start).Milliseconds()}
	return classifyResult(truncateResult(out, cmd.MaxStdoutBytes, cmd.MaxStderrBytes)), nil
}

// Cleanup removes the container workspace.
func (r *ContainerRuntime) Cleanup(ctx context.Context) error {
	if r.ws.Path == "" {
		return nil
	}
	return r.engine.Manager().Cleanup(ctx, r.ws)
}

// Close stops the owned container executor and closes its Docker client.
func (r *ContainerRuntime) Close() error {
	if r == nil || r.close == nil {
		return nil
	}
	close := r.close
	r.close = nil
	return close()
}

func ptrInt64(v int64) *int64 {
	return &v
}

func putFilesFromDirectory(src, to string) ([]codeexecutor.PutFile, error) {
	var files []codeexecutor.PutFile
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported staged file %s", path)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		if rel == "." || rel == "" || filepath.IsAbs(rel) || rel == ".." || len(rel) >= 3 && rel[:3] == "../" {
			return fmt.Errorf("unsafe staged path %q", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mode := uint32(0o644)
		if info.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		files = append(files, codeexecutor.PutFile{Path: filepath.ToSlash(filepath.Join(to, rel)), Content: data, Mode: mode})
		return nil
	})
	return files, err
}

func containerReadOnlyFixupSpec(rel string) codeexecutor.RunProgramSpec {
	return codeexecutor.RunProgramSpec{
		Cmd:      "chmod",
		Args:     []string{"-R", "a+rX,a-w", rel},
		CleanEnv: true,
		Timeout:  10 * time.Second,
		Env:      map[string]string{"PATH": "/usr/bin:/bin"},
		Limits:   codeexecutor.ResourceLimits{CPUPercent: 50, MemoryMB: 64, MaxPIDs: 16},
	}
}

func containerScriptPath(cwd string) string {
	if cwd == "" {
		cwd = "work/repo"
	}
	rel, err := filepath.Rel(cwd, "skills/code-review/scripts/run_checks.sh")
	if err != nil {
		return "../../skills/code-review/scripts/run_checks.sh"
	}
	return filepath.ToSlash(rel)
}
