//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

const (
	maxSnapshotBytes  = 32 << 20
	maxSnapshotFiles  = 2000
	maxArtifactBytes  = 1 << 20
	containerImageTag = "trpc-agent-go-code-review:go1.24"
	cleanupTimeout    = 5 * time.Second
)

type sandbox struct {
	engine      codeexecutor.Engine
	executor    Executor
	initErr     error
	close       func() error
	timeout     time.Duration
	outputLimit int
	outputDir   string
	skillDir    string
}

type engineFactory func(context.Context, Config, string) (codeexecutor.Engine, func() error, error)

var containerFactory engineFactory = func(_ context.Context, _ Config, baseDir string) (codeexecutor.Engine, func() error, error) {
	executor, err := containerexec.New(
		containerexec.WithDockerFilePath(filepath.Join(baseDir, "sandbox")),
		containerexec.WithContainerConfig(dockercontainer.Config{
			Image: containerImageTag, WorkingDir: "/", Cmd: []string{"tail", "-f", "/dev/null"}, Tty: true, OpenStdin: true,
		}),
	)
	if err != nil {
		return nil, nil, err
	}
	return executor.Engine(), executor.Close, nil
}

var e2bFactory engineFactory = func(ctx context.Context, cfg Config, _ string) (codeexecutor.Engine, func() error, error) {
	executor, err := e2bexec.NewWithContext(ctx,
		e2bexec.WithSandboxTimeout(2*cfg.Timeout),
		e2bexec.WithExecutionTimeout(cfg.Timeout),
		e2bexec.WithEnvVars(map[string]string{}),
	)
	if err != nil {
		return nil, nil, err
	}
	return executor.Engine(), executor.Close, nil
}

func newSandbox(ctx context.Context, cfg Config, baseDir string) (*sandbox, error) {
	name := Executor(strings.ToLower(strings.TrimSpace(string(cfg.Executor))))
	if cfg.DryRun || name == ExecutorFake || name == ExecutorFakeFailure {
		if cfg.DryRun {
			name = ExecutorFake
		}
		return &sandbox{executor: name, timeout: cfg.Timeout, outputLimit: cfg.OutputLimit, outputDir: cfg.OutputDir, skillDir: filepath.Join(baseDir, "skills", "code-review")}, nil
	}
	result := &sandbox{executor: name, timeout: cfg.Timeout, outputLimit: cfg.OutputLimit, outputDir: cfg.OutputDir, skillDir: filepath.Join(baseDir, "skills", "code-review")}
	switch name {
	case ExecutorContainer:
		engine, closeFn, err := containerFactory(ctx, cfg, baseDir)
		if err != nil {
			result.initErr = err
			return result, nil
		}
		result.engine, result.close = engine, closeFn
	case ExecutorE2B:
		engine, closeFn, err := e2bFactory(ctx, cfg, baseDir)
		if err != nil {
			result.initErr = err
			return result, nil
		}
		result.engine, result.close = engine, closeFn
	case ExecutorLocal:
		if !cfg.AllowLocal {
			return nil, errors.New("local executor is development-only; pass --allow-local-fallback")
		}
		exec := localexec.New(localexec.WithTimeout(cfg.Timeout))
		result.engine, result.executor = exec.Engine(), ExecutorLocalDev
	default:
		return nil, fmt.Errorf("unknown executor %q", cfg.Executor)
	}
	if !result.engine.Describe().SupportsCleanEnv {
		if result.close != nil {
			_ = result.close()
		}
		result.engine = nil
		result.initErr = errors.New("executor does not enforce a clean environment")
		return result, nil
	}
	return result, nil
}

func (s *sandbox) Close() error {
	if s == nil || s.close == nil {
		return nil
	}
	return s.close()
}

func (s *sandbox) run(ctx context.Context, taskID, repoPath string, input ParsedInput) (runs []SandboxRun, decisions []PermissionDecision, artifacts []Artifact, err error) {
	type check struct {
		command string
		args    []string
		cwd     string
	}
	checks := []check{{"bash", []string{"skills/code-review/scripts/diff_stats.sh", "work/change.diff", "out/diff_stats.json"}, "."}}
	if strings.TrimSpace(repoPath) != "" {
		checks = append(checks,
			check{"go", []string{"test", "./..."}, "work/repo"},
			check{"go", []string{"vet", "./..."}, "work/repo"},
			check{"staticcheck", []string{"./..."}, "work/repo"},
		)
	}
	decisions = make([]PermissionDecision, 0, len(checks))
	for _, item := range checks {
		decisions = append(decisions, decide(ctx, item.command, item.args))
	}
	stats, err := s.writeDiffStats(taskID, input.Summary)
	if err != nil {
		return nil, decisions, nil, fmt.Errorf("write diff statistics: %w", err)
	}
	artifacts = []Artifact{stats}
	if s.initErr != nil {
		return []SandboxRun{setupFailure(s.executor, "initialize_executor", s.initErr, s.outputLimit)}, decisions, artifacts, nil
	}
	if s.engine == nil {
		runs := make([]SandboxRun, 0, len(checks))
		for index, item := range checks {
			status := RunSkipped
			errorType := ErrorDryRun
			if s.executor == ExecutorFakeFailure && index == 0 {
				status, errorType = RunFailed, ErrorExecutor
			}
			if decisions[index].Action != PermissionAllow {
				status, errorType = RunStatus(decisions[index].Action), ErrorPermissionDecision
			}
			runs = append(runs, SandboxRun{Command: item.command, Args: item.args, Executor: s.executor, Status: status, ErrorType: errorType})
		}
		return runs, decisions, artifacts, nil
	}
	ws, err := s.engine.Manager().CreateWorkspace(ctx, taskID, codeexecutor.WorkspacePolicy{Isolated: true})
	if err != nil {
		return []SandboxRun{setupFailure(s.executor, "create_workspace", err, s.outputLimit)}, decisions, artifacts, nil
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if cleanupErr := s.engine.Manager().Cleanup(cleanupCtx, ws); cleanupErr != nil {
			runs = append(runs, setupFailure(s.executor, "cleanup_workspace", cleanupErr, s.outputLimit))
		}
	}()
	if err := s.engine.FS().StageDirectory(ctx, ws, s.skillDir, "skills/code-review", codeexecutor.StageOptions{ReadOnly: true}); err != nil {
		return []SandboxRun{setupFailure(s.executor, "stage_skill", err, s.outputLimit)}, decisions, artifacts, nil
	}
	if err := s.engine.FS().PutFiles(ctx, ws, []codeexecutor.PutFile{{Path: "work/change.diff", Content: []byte(redact(input.Raw)), Mode: 0o600}}); err != nil {
		return []SandboxRun{setupFailure(s.executor, "stage_diff", err, s.outputLimit)}, decisions, artifacts, nil
	}
	cleanup := func() {}
	if strings.TrimSpace(repoPath) != "" {
		snapshot, release, err := safeSnapshot(repoPath)
		if err != nil {
			return []SandboxRun{setupFailure(s.executor, "snapshot_repo", err, s.outputLimit)}, decisions, artifacts, nil
		}
		cleanup = release
		if err := s.engine.FS().StageDirectory(ctx, ws, snapshot, "work/repo", codeexecutor.StageOptions{}); err != nil {
			cleanup()
			return []SandboxRun{setupFailure(s.executor, "stage_repo", err, s.outputLimit)}, decisions, artifacts, nil
		}
	}
	defer cleanup()
	runs = make([]SandboxRun, 0, len(checks))
	for index, item := range checks {
		if decisions[index].Action != PermissionAllow {
			runs = append(runs, SandboxRun{Command: item.command, Args: item.args, Executor: s.executor, Status: RunStatus(decisions[index].Action), Stderr: decisions[index].Reason, ErrorType: ErrorPermissionDecision})
			continue
		}
		if runtime.GOOS == "windows" && s.executor == ExecutorLocalDev && item.command == "bash" {
			runs = append(runs, SandboxRun{Command: item.command, Args: item.args, Executor: s.executor, Status: RunSuccess, Stderr: "portable Go wrapper executed the audited diff statistics operation"})
			continue
		}
		runs = append(runs, s.execute(ctx, ws, item.command, item.args, item.cwd))
	}
	return runs, decisions, artifacts, nil
}

func (s *sandbox) execute(ctx context.Context, ws codeexecutor.Workspace, command string, args []string, cwd string) SandboxRun {
	started := time.Now()
	result, err := s.engine.Runner().RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: command, Args: args, Cwd: cwd, Timeout: s.timeout, CleanEnv: true, Env: reviewEnvironment(s.executor),
	})
	stdout, stdoutCut := truncate(result.Stdout, s.outputLimit)
	stderr, stderrCut := truncate(result.Stderr, s.outputLimit)
	run := SandboxRun{Command: command, Args: args, Executor: s.executor, Status: RunSuccess, ExitCode: result.ExitCode, Stdout: stdout, Stderr: stderr, Duration: time.Since(started), DurationMS: time.Since(started).Milliseconds(), TimedOut: result.TimedOut, OutputTruncated: stdoutCut || stderrCut}
	if err != nil {
		combined, cut := truncate(strings.TrimSpace(result.Stderr+"\n"+err.Error()), s.outputLimit)
		run.Status, run.ErrorType, run.Stderr = RunFailed, classifyExecutionError(err), combined
		run.OutputTruncated = run.OutputTruncated || cut
	}
	if result.ExitCode != 0 && run.Status == "success" {
		run.Status, run.ErrorType = RunFailed, ErrorNonZeroExit
	}
	if result.TimedOut {
		run.Status, run.ErrorType = RunFailed, ErrorTimeout
	}
	if run.ErrorType != ErrorTimeout && command == "staticcheck" && commandNotFound(run.Stderr) {
		run.Status, run.ErrorType = RunSkipped, ErrorToolUnavailable
	}
	if run.ErrorType != ErrorTimeout && (command == "go" || command == "staticcheck") && dependencyUnavailable(run.Stderr) {
		run.Status, run.ErrorType = RunSkipped, ErrorDependencyUnavailable
	}
	return run
}

func commandNotFound(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "command not found") || strings.Contains(lower, "executable file not found") || strings.Contains(lower, "staticcheck: not found") || strings.TrimSpace(lower) == "not found"
}

func dependencyUnavailable(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"missing go.sum entry", "no required module provides package", "cannot find module providing package", "module lookup disabled by goproxy=off"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func reviewEnvironment(executor Executor) map[string]string {
	if executor == ExecutorLocalDev {
		temp := os.TempDir()
		return map[string]string{
			"PATH": os.Getenv("PATH"), "HOME": temp, "TEMP": temp, "TMP": temp,
			"GOCACHE":     filepath.Join(temp, "trpc-code-review-go-cache"),
			"GOMODCACHE":  filepath.Join(temp, "trpc-code-review-go-mod-cache"),
			"CGO_ENABLED": "0", "GOTOOLCHAIN": "local", "GOPROXY": "off",
		}
	}
	return map[string]string{"PATH": "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin", "HOME": "/tmp", "TMPDIR": "/tmp", "GOCACHE": "/tmp/go-cache", "GOMODCACHE": "/tmp/go-mod", "CGO_ENABLED": "0", "GOTOOLCHAIN": "local", "GONOSUMDB": "*", "GOPROXY": "off"}
}

func safeSnapshot(repo string) (string, func(), error) {
	root, err := filepath.Abs(repo)
	if err != nil {
		return "", func() {}, err
	}
	dest, err := os.MkdirTemp("", "code-review-snapshot-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dest) }
	files, bytes := 0, int64(0)
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel != "." && (entry.Name() == ".git" || entry.Name() == "vendor" || entry.Name() == "node_modules" || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		name := strings.ToLower(entry.Name())
		if !(strings.HasSuffix(name, ".go") || name == "go.mod" || name == "go.sum" || name == "go.work" || name == "go.work.sum") {
			return nil
		}
		files++
		bytes += info.Size()
		if files > maxSnapshotFiles || bytes > maxSnapshotBytes {
			return errors.New("repository snapshot exceeds safety limits")
		}
		target := filepath.Join(dest, rel)
		if !withinRoot(dest, target) {
			return errors.New("snapshot target escaped destination")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return copyBoundedFile(current, target, info)
	})
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	return dest, cleanup, nil
}

func copyBoundedFile(source, target string, original fs.FileInfo) error {
	if original.Size() > maxSnapshotBytes {
		return errors.New("snapshot file too large")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(original, opened) || opened.Size() != original.Size() {
		return errors.New("snapshot source changed while opening")
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	written, err := io.Copy(out, io.LimitReader(in, original.Size()+1))
	if err != nil {
		return err
	}
	if written != original.Size() {
		return errors.New("snapshot source changed while copying")
	}
	return out.Sync()
}

func (s *sandbox) writeDiffStats(taskID string, summary DiffSummary) (Artifact, error) {
	dir := filepath.Join(s.outputDir, taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Artifact{}, err
	}
	file := filepath.Join(dir, "diff_stats.json")
	data, err := json.MarshalIndent(map[string]int{"files_changed": summary.FilesChanged, "added_lines": summary.AddedLines, "deleted_lines": summary.DeletedLines}, "", "  ")
	if err != nil {
		return Artifact{}, err
	}
	data = append(data, '\n')
	if len(data) > maxArtifactBytes {
		return Artifact{}, errors.New("diff statistics exceed artifact limit")
	}
	if err := atomicWrite(file, data); err != nil {
		return Artifact{}, err
	}
	return Artifact{Name: "diff_stats.json", Path: file, MIMEType: "application/json", SizeBytes: int64(len(data))}, nil
}

func setupFailure(executor Executor, operation string, err error, outputLimit int) SandboxRun {
	stderr, cut := truncate(err.Error(), outputLimit)
	return SandboxRun{Command: operation, Executor: executor, Status: RunFailed, ErrorType: ErrorSetup, Stderr: stderr, OutputTruncated: cut}
}
func classifyExecutionError(err error) ErrorType {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return ErrorTimeout
	}
	return ErrorExecutor
}
