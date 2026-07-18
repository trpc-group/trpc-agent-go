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

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

const (
	maxSnapshotBytes = 32 << 20
	maxSnapshotFiles = 2000
	maxArtifactBytes = 1 << 20
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
	executor, err := containerexec.New(containerexec.WithDockerFilePath(filepath.Join(baseDir, "sandbox")))
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

func (s *sandbox) run(ctx context.Context, taskID, repoPath string, input ParsedInput) ([]SandboxRun, []PermissionDecision, []Artifact) {
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
	decisions := make([]PermissionDecision, 0, len(checks))
	for _, item := range checks {
		decisions = append(decisions, decide(ctx, item.command, item.args))
	}
	artifacts := []Artifact{s.writeDiffStats(taskID, input.Summary)}
	if s.initErr != nil {
		return []SandboxRun{setupFailure(s.executor, "initialize_executor", s.initErr)}, decisions, artifacts
	}
	if s.engine == nil {
		runs := make([]SandboxRun, 0, len(checks))
		for index, item := range checks {
			status := RunSkipped
			errorType := ErrorType("dry_run")
			if s.executor == ExecutorFakeFailure && index == 0 {
				status, errorType = RunFailed, "executor_error"
			}
			if decisions[index].Action != PermissionAllow {
				status, errorType = RunStatus(decisions[index].Action), "permission_decision"
			}
			runs = append(runs, SandboxRun{Command: item.command, Args: item.args, Executor: s.executor, Status: status, ErrorType: errorType})
		}
		return runs, decisions, artifacts
	}
	ws, err := s.engine.Manager().CreateWorkspace(ctx, taskID, codeexecutor.WorkspacePolicy{Isolated: true})
	if err != nil {
		return []SandboxRun{setupFailure(s.executor, "create_workspace", err)}, decisions, artifacts
	}
	defer s.engine.Manager().Cleanup(context.WithoutCancel(ctx), ws)
	if err := s.engine.FS().StageDirectory(ctx, ws, s.skillDir, "skills/code-review", codeexecutor.StageOptions{ReadOnly: true}); err != nil {
		return []SandboxRun{setupFailure(s.executor, "stage_skill", err)}, decisions, artifacts
	}
	if err := s.engine.FS().PutFiles(ctx, ws, []codeexecutor.PutFile{{Path: "work/change.diff", Content: []byte(redact(input.Raw)), Mode: 0o600}}); err != nil {
		return []SandboxRun{setupFailure(s.executor, "stage_diff", err)}, decisions, artifacts
	}
	cleanup := func() {}
	if strings.TrimSpace(repoPath) != "" {
		snapshot, release, err := safeSnapshot(repoPath)
		if err != nil {
			return []SandboxRun{setupFailure(s.executor, "snapshot_repo", err)}, decisions, artifacts
		}
		cleanup = release
		if err := s.engine.FS().StageDirectory(ctx, ws, snapshot, "work/repo", codeexecutor.StageOptions{}); err != nil {
			cleanup()
			return []SandboxRun{setupFailure(s.executor, "stage_repo", err)}, decisions, artifacts
		}
	}
	defer cleanup()
	runs := make([]SandboxRun, 0, len(checks))
	for index, item := range checks {
		if decisions[index].Action != PermissionAllow {
			runs = append(runs, SandboxRun{Command: item.command, Args: item.args, Executor: s.executor, Status: RunStatus(decisions[index].Action), Stderr: decisions[index].Reason, ErrorType: "permission_decision"})
			continue
		}
		if runtime.GOOS == "windows" && s.executor == ExecutorLocalDev && item.command == "bash" {
			runs = append(runs, SandboxRun{Command: item.command, Args: item.args, Executor: s.executor, Status: RunSuccess, Stderr: "portable Go wrapper executed the audited diff statistics operation"})
			continue
		}
		runs = append(runs, s.execute(ctx, ws, item.command, item.args, item.cwd))
	}
	return runs, decisions, artifacts
}

func (s *sandbox) execute(ctx context.Context, ws codeexecutor.Workspace, command string, args []string, cwd string) SandboxRun {
	started := time.Now()
	result, err := s.engine.Runner().RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: command, Args: args, Cwd: cwd, Timeout: s.timeout, CleanEnv: true, Env: reviewEnvironment(s.executor),
	})
	stdout, stdoutCut := truncate(result.Stdout, s.outputLimit)
	stderr, stderrCut := truncate(result.Stderr, s.outputLimit)
	stdout, stderr = redact(stdout), redact(stderr)
	run := SandboxRun{Command: command, Args: args, Executor: s.executor, Status: RunSuccess, ExitCode: result.ExitCode, Stdout: stdout, Stderr: stderr, Duration: time.Since(started), DurationMS: time.Since(started).Milliseconds(), TimedOut: result.TimedOut, OutputTruncated: stdoutCut || stderrCut}
	if err != nil {
		run.Status, run.ErrorType, run.Stderr = RunFailed, classifyExecutionError(err), redact(strings.TrimSpace(stderr+"\n"+err.Error()))
	}
	if result.ExitCode != 0 && run.Status == "success" {
		run.Status, run.ErrorType = RunFailed, "non_zero_exit"
	}
	if result.TimedOut {
		run.Status, run.ErrorType = RunFailed, "timeout"
	}
	if command == "staticcheck" && (strings.Contains(strings.ToLower(run.Stderr), "not found") || run.ExitCode == -1) {
		run.Status, run.ErrorType = RunSkipped, "tool_unavailable"
	}
	return run
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
		return copyBoundedFile(current, target, info.Size())
	})
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	return dest, cleanup, nil
}

func copyBoundedFile(source, target string, size int64) error {
	if size > maxSnapshotBytes {
		return errors.New("snapshot file too large")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.CopyN(out, in, size)
	return err
}

func (s *sandbox) writeDiffStats(taskID string, summary DiffSummary) Artifact {
	dir := filepath.Join(s.outputDir, taskID)
	_ = os.MkdirAll(dir, 0o700)
	file := filepath.Join(dir, "diff_stats.json")
	data, _ := json.MarshalIndent(map[string]int{"files_changed": summary.FilesChanged, "added_lines": summary.AddedLines, "deleted_lines": summary.DeletedLines}, "", "  ")
	if len(data) <= maxArtifactBytes {
		_ = os.WriteFile(file, append(data, '\n'), 0o600)
	}
	return Artifact{Name: "diff_stats.json", Path: file, MIMEType: "application/json", SizeBytes: int64(len(data) + 1)}
}

func setupFailure(executor Executor, operation string, err error) SandboxRun {
	return SandboxRun{Command: operation, Executor: executor, Status: "failed", ErrorType: "setup_error", Stderr: redact(err.Error())}
}
func classifyExecutionError(err error) ErrorType {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return "timeout"
	}
	return "executor_error"
}
