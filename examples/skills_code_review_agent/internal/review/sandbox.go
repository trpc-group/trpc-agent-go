//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type SandboxRunner interface {
	RunChecks(ctx context.Context, taskID string, repoPath string, pd ParsedDiff) SandboxResult
	Close() error
}

type WorkspaceSandboxRunner struct {
	executorName     string
	engine           codeexecutor.Engine
	closeFn          func() error
	timeout          time.Duration
	outputLimitBytes int
	outputDir        string
}

func NewSandboxRunner(cfg ReviewConfig) (SandboxRunner, error) {
	return NewSandboxRunnerWithContext(context.Background(), cfg)
}

func NewSandboxRunnerWithContext(ctx context.Context, cfg ReviewConfig) (SandboxRunner, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	limit := cfg.OutputLimitBytes
	if limit <= 0 {
		limit = 64 * 1024
	}
	outputDir := cfg.OutputDir
	if outputDir == "" {
		outputDir = "output"
	}
	executor := strings.ToLower(strings.TrimSpace(cfg.Executor))
	if executor == "" {
		executor = "container"
	}
	switch executor {
	case "container":
		pidsLimit := int64(256)
		dockerfilePath, cleanupBuildContext, err := prepareSandboxBuildContext(
			cfg.ContainerBaseImage,
			cfg.InstallStaticcheck,
		)
		if err != nil {
			return nil, err
		}
		ex, err := containerexec.New(
			containerexec.WithDockerFilePath(dockerfilePath),
			containerexec.WithHostConfig(dockercontainer.HostConfig{
				AutoRemove:  true,
				Privileged:  false,
				NetworkMode: "none",
				CapDrop:     []string{"ALL"},
				SecurityOpt: []string{"no-new-privileges"},
				Resources: dockercontainer.Resources{
					Memory:    1024 * 1024 * 1024,
					NanoCPUs:  2_000_000_000,
					PidsLimit: &pidsLimit,
				},
			}),
			containerexec.WithContainerConfig(dockercontainer.Config{
				Image:      "trpc-agent-go-code-review:latest",
				WorkingDir: "/",
				Cmd:        []string{"tail", "-f", "/dev/null"},
				Tty:        true,
				OpenStdin:  true,
			}),
		)
		if err != nil {
			if cleanupBuildContext != nil {
				_ = cleanupBuildContext()
			}
			return nil, err
		}
		closeFn := ex.Close
		if cleanupBuildContext != nil {
			closeFn = func() error {
				closeErr := ex.Close()
				cleanupErr := cleanupBuildContext()
				if closeErr != nil {
					return closeErr
				}
				return cleanupErr
			}
		}
		return &WorkspaceSandboxRunner{
			executorName:     "container",
			engine:           ex.Engine(),
			closeFn:          closeFn,
			timeout:          timeout,
			outputLimitBytes: limit,
			outputDir:        outputDir,
		}, nil
	case "e2b":
		initCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		ex, err := e2bexec.NewWithContext(initCtx)
		if err != nil {
			return nil, err
		}
		return &WorkspaceSandboxRunner{
			executorName:     "e2b",
			engine:           ex.Engine(),
			closeFn:          ex.Close,
			timeout:          timeout,
			outputLimitBytes: limit,
			outputDir:        outputDir,
		}, nil
	case "local":
		if !cfg.AllowLocalFallback {
			return nil, errors.New("local executor is development-only; pass --allow-local-fallback to enable it")
		}
		ex := localexec.New()
		return &WorkspaceSandboxRunner{
			executorName:     "local-dev-fallback",
			engine:           ex.Engine(),
			timeout:          timeout,
			outputLimitBytes: limit,
			outputDir:        outputDir,
		}, nil
	case "fake", "none":
		return NoopSandboxRunner{executorName: executor}, nil
	case "fake-fail":
		return FakeFailSandboxRunner{}, nil
	default:
		return nil, fmt.Errorf("unknown executor %q", cfg.Executor)
	}
}

func prepareSandboxBuildContext(baseImage string, installStaticcheck bool) (string, func() error, error) {
	sourceDir := filepath.Join(exampleDir(), "sandbox")
	baseImage = strings.TrimSpace(baseImage)
	if baseImage == "" && !installStaticcheck {
		return sourceDir, nil, nil
	}
	if baseImage != "" && !validContainerImageRef(baseImage) {
		return "", nil, fmt.Errorf("invalid container base image %q", baseImage)
	}
	data, err := os.ReadFile(filepath.Join(sourceDir, "Dockerfile"))
	if err != nil {
		return "", nil, err
	}
	dockerfile := string(data)
	if baseImage != "" {
		dockerfile = strings.Replace(
			dockerfile,
			"ARG REVIEW_BASE_IMAGE=golang:1.23-bookworm",
			"ARG REVIEW_BASE_IMAGE="+baseImage,
			1,
		)
	}
	if installStaticcheck {
		dockerfile = strings.Replace(
			dockerfile,
			"ARG INSTALL_STATICCHECK=false",
			"ARG INSTALL_STATICCHECK=true",
			1,
		)
	}
	dir, err := os.MkdirTemp("", "trpc-code-review-sandbox-*")
	if err != nil {
		return "", nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return dir, func() error { return os.RemoveAll(dir) }, nil
}

func validContainerImageRef(ref string) bool {
	if ref == "" || strings.Contains(ref, "..") {
		return false
	}
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("./:_-@", r):
		default:
			return false
		}
	}
	return true
}

func executorLabel(executor string) string {
	executor = strings.ToLower(strings.TrimSpace(executor))
	if executor == "" {
		return "container"
	}
	if executor == "local" {
		return "local-dev-fallback"
	}
	return executor
}

func (r *WorkspaceSandboxRunner) Close() error {
	if r == nil || r.closeFn == nil {
		return nil
	}
	return r.closeFn()
}

func (r *WorkspaceSandboxRunner) RunChecks(ctx context.Context, taskID string, repoPath string, pd ParsedDiff) SandboxResult {
	policy := ReviewPermissionPolicy{TaskID: taskID}
	var runs []SandboxRun
	var decisions []PermissionDecisionRecord
	var artifacts []ArtifactRecord
	ws, err := r.engine.Manager().CreateWorkspace(ctx, taskID, codeexecutor.WorkspacePolicy{Isolated: true})
	if err != nil {
		return SandboxResult{Runs: []SandboxRun{failedSetupRun(taskID, r.executorName, "create_workspace", err)}}
	}
	defer r.engine.Manager().Cleanup(ctx, ws)
	skillPath := filepath.Join(exampleDir(), "skills", "code-review")
	if err := r.engine.FS().StageDirectory(ctx, ws, skillPath, filepath.Join(codeexecutor.DirSkills, "code-review"), codeexecutor.StageOptions{ReadOnly: true}); err != nil {
		return SandboxResult{Runs: []SandboxRun{failedSetupRun(taskID, r.executorName, "stage_skill", err)}}
	}
	if err := r.engine.FS().PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    filepath.Join(codeexecutor.DirWork, "change.diff"),
		Content: []byte(redactSecrets(pd.Raw)),
		Mode:    0o600,
	}}); err != nil {
		return SandboxResult{Runs: []SandboxRun{failedSetupRun(taskID, r.executorName, "stage_diff", err)}}
	}
	hasRepo := strings.TrimSpace(repoPath) != ""
	repoChecksUnavailable := ""
	if hasRepo && r.executorName == "e2b" {
		hasRepo = false
		repoChecksUnavailable = "e2b_egress_not_enforced"
	}
	repoCwd := filepath.Join(codeexecutor.DirWork, "repo")
	if hasRepo {
		absRepo, err := filepath.Abs(repoPath)
		if err != nil {
			return SandboxResult{Runs: []SandboxRun{failedSetupRun(taskID, r.executorName, "abs_repo", err)}}
		}
		if r.executorName == "container" && repoHasUnvendoredExternalModules(absRepo) {
			hasRepo = false
			repoChecksUnavailable = "dependency_unavailable"
		}
	}
	if hasRepo {
		absRepo, err := filepath.Abs(repoPath)
		if err != nil {
			return SandboxResult{Runs: []SandboxRun{failedSetupRun(taskID, r.executorName, "abs_repo", err)}}
		}
		stageRepo, stagedRepoCwd, cleanupRepo, err := prepareSandboxRepoSnapshotForPath(ctx, absRepo)
		if err != nil {
			return SandboxResult{Runs: []SandboxRun{failedSetupRun(taskID, r.executorName, "prepare_repo_snapshot", err)}}
		}
		defer cleanupRepo()
		if err := r.engine.FS().StageDirectory(ctx, ws, stageRepo, filepath.Join(codeexecutor.DirWork, "repo"), codeexecutor.StageOptions{}); err != nil {
			return SandboxResult{Runs: []SandboxRun{failedSetupRun(taskID, r.executorName, "stage_repo", err)}}
		}
		repoCwd = filepath.Join(codeexecutor.DirWork, "repo", filepath.FromSlash(stagedRepoCwd))
	}
	checks := []struct {
		cmd  string
		args []string
		cwd  string
	}{
		{
			cmd:  "bash",
			args: []string{filepath.Join(codeexecutor.DirSkills, "code-review", "scripts", "diff_summary.sh"), filepath.Join(codeexecutor.DirWork, "change.diff"), filepath.Join(codeexecutor.DirOut, "diff_summary.json")},
			cwd:  ".",
		},
	}
	if hasRepo {
		checks = append(checks,
			struct {
				cmd  string
				args []string
				cwd  string
			}{cmd: "go", args: []string{"test", "./..."}, cwd: repoCwd},
			struct {
				cmd  string
				args []string
				cwd  string
			}{cmd: "go", args: []string{"vet", "./..."}, cwd: repoCwd},
			struct {
				cmd  string
				args []string
				cwd  string
			}{cmd: "staticcheck", args: []string{"./..."}, cwd: repoCwd},
		)
	} else if repoChecksUnavailable != "" {
		runs = append(runs, unavailableRepoCheckRuns(taskID, r.executorName, repoChecksUnavailable)...)
	} else {
		runs = append(runs, SandboxRun{
			ID:        newID("run"),
			TaskID:    taskID,
			Command:   "go",
			Args:      []string{"test", "./...", "vet", "./...", "staticcheck", "./..."},
			Executor:  r.executorName,
			Status:    "skipped",
			ErrorType: "no_repo_path",
			StartedAt: time.Now(),
			Stderr:    "repo path not provided; repository Go checks skipped after diff summary",
		})
	}
	for _, check := range checks {
		record, decision, err := policy.Decide(ctx, check.cmd, check.args)
		decisions = append(decisions, record)
		if err != nil {
			runs = append(runs, permissionRun(taskID, r.executorName, check.cmd, check.args, "permission_error", err.Error()))
			continue
		}
		if decision.Action != tool.PermissionActionAllow {
			runs = append(runs, permissionRun(taskID, r.executorName, check.cmd, check.args, string(decision.Action), decision.Reason))
			continue
		}
		runs = append(runs, r.runProgram(ctx, ws, taskID, check.cmd, check.args, check.cwd))
	}
	if files, err := r.engine.FS().Collect(ctx, ws, []string{filepath.Join(codeexecutor.DirOut, "diff_summary.json")}); err == nil {
		for _, f := range files {
			if artifact, err := r.materializeCollectedArtifact(taskID, f); err == nil {
				artifacts = append(artifacts, artifact)
			}
		}
	}
	return SandboxResult{
		Runs:        runs,
		Decisions:   decisions,
		Findings:    ParseSandboxFindings(runs),
		Artifacts:   artifacts,
		SkillLoaded: true,
	}
}

func (r *WorkspaceSandboxRunner) runProgram(ctx context.Context, ws codeexecutor.Workspace, taskID string, cmd string, args []string, cwd string) SandboxRun {
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	res, err := r.engine.Runner().RunProgram(runCtx, ws, codeexecutor.RunProgramSpec{
		Cmd:      cmd,
		Args:     args,
		Cwd:      cwd,
		Timeout:  r.timeout,
		CleanEnv: true,
		Env:      goReviewEnv(),
	})
	out, outTrunc := limitText(redactSecrets(res.Stdout), r.outputLimitBytes)
	stderr, errTrunc := limitText(redactSecrets(res.Stderr), r.outputLimitBytes)
	status := "success"
	errType := ""
	if err != nil {
		status = "failed"
		errType = classifySandboxError(err)
		stderr = strings.TrimSpace(stderr + "\n" + redactSecrets(err.Error()))
	}
	if res.ExitCode != 0 {
		status = "failed"
		if errType == "" {
			errType = "non_zero_exit"
		}
		if cmd == "staticcheck" && staticcheckLooksUnavailable(res.ExitCode, stderr) {
			errType = "tool_unavailable"
		}
	}
	if res.TimedOut || errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		status = "failed"
		errType = "timeout"
	}
	if cmd == "staticcheck" && errType == "tool_unavailable" {
		status = "skipped"
		stderr = "staticcheck is not available in the sandbox image; install it with --container-install-staticcheck or use a prebuilt image that includes it"
	}
	return SandboxRun{
		ID:              newID("run"),
		TaskID:          taskID,
		Command:         cmd,
		Args:            args,
		Executor:        r.executorName,
		Status:          status,
		ExitCode:        res.ExitCode,
		Stdout:          out,
		Stderr:          stderr,
		ErrorType:       errType,
		StartedAt:       start,
		DurationMS:      time.Since(start).Milliseconds(),
		TimedOut:        res.TimedOut || errors.Is(runCtx.Err(), context.DeadlineExceeded),
		OutputTruncated: outTrunc || errTrunc,
	}
}

func (r *WorkspaceSandboxRunner) materializeCollectedArtifact(taskID string, f codeexecutor.File) (ArtifactRecord, error) {
	name := filepath.Base(f.Name)
	content := []byte(redactSecrets(f.Content))
	if int64(len(content)) > defaultArtifactPolicy().MaxBytesPerFile {
		return ArtifactRecord{}, fmt.Errorf("artifact %s exceeds max size %d", name, defaultArtifactPolicy().MaxBytesPerFile)
	}
	outputDir := r.outputDir
	if outputDir == "" {
		outputDir = "output"
	}
	artifactDir := filepath.Join(outputDir, taskID)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return ArtifactRecord{}, err
	}
	path := filepath.Join(artifactDir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return ArtifactRecord{}, err
	}
	return ArtifactRecord{
		ID:        newID("artifact"),
		TaskID:    taskID,
		Name:      name,
		Path:      path,
		MimeType:  firstNonEmpty(f.MIMEType, "application/json"),
		SizeBytes: int64(len(content)),
		CreatedAt: time.Now(),
	}, nil
}

func unavailableRepoCheckRuns(taskID, executor, reason string) []SandboxRun {
	var stderr string
	switch reason {
	case "e2b_egress_not_enforced":
		stderr = "repository checks are disabled for E2B because outbound network egress is not denied; only diff summary is executed"
	case "dependency_unavailable":
		stderr = "repository declares external modules without a vendor directory; offline container checks cannot resolve dependencies"
	default:
		stderr = "repository checks are unavailable"
	}
	specs := []struct {
		cmd  string
		args []string
	}{
		{"go", []string{"test", "./..."}},
		{"go", []string{"vet", "./..."}},
		{"staticcheck", []string{"./..."}},
	}
	out := make([]SandboxRun, 0, len(specs))
	for _, spec := range specs {
		out = append(out, SandboxRun{
			ID:        newID("run"),
			TaskID:    taskID,
			Command:   spec.cmd,
			Args:      spec.args,
			Executor:  executor,
			Status:    "skipped",
			ErrorType: reason,
			StartedAt: time.Now(),
			Stderr:    stderr,
		})
	}
	return out
}

func staticcheckLooksUnavailable(exitCode int, stderr string) bool {
	if exitCode != 127 {
		return false
	}
	msg := strings.ToLower(stderr)
	return strings.Contains(msg, "no such file") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "executable file not found")
}

func failedSetupRun(taskID, executor, phase string, err error) SandboxRun {
	return SandboxRun{
		ID:        newID("run"),
		TaskID:    taskID,
		Command:   phase,
		Executor:  executor,
		Status:    "failed",
		ErrorType: "sandbox_setup",
		StartedAt: time.Now(),
		Stderr:    redactSecrets(err.Error()),
	}
}

func permissionRun(taskID, executor, cmd string, args []string, status, reason string) SandboxRun {
	return SandboxRun{
		ID:        newID("run"),
		TaskID:    taskID,
		Command:   cmd,
		Args:      args,
		Executor:  executor,
		Status:    status,
		ErrorType: "permission_decision",
		StartedAt: time.Now(),
		Stderr:    reason,
	}
}

func goReviewEnv() map[string]string {
	return map[string]string{
		"PATH":        "/usr/local/go/bin:/go/bin:/usr/local/bin:/usr/bin:/bin",
		"HOME":        "/tmp",
		"GOCACHE":     "/tmp/go-cache",
		"GOMODCACHE":  "/tmp/go/pkg/mod",
		"GOTOOLCHAIN": "local",
		"CGO_ENABLED": "0",
	}
}

func repoHasUnvendoredExternalModules(repoPath string) bool {
	if _, err := os.Stat(filepath.Join(repoPath, "vendor")); err == nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return false
	}
	return goModHasRequire(string(data))
}

func goModHasRequire(data string) bool {
	requires, localReplaces := parseGoModRequiresAndLocalReplaces(data)
	for module := range requires {
		if !localReplaces[module] {
			return true
		}
	}
	return false
}

func parseGoModRequiresAndLocalReplaces(data string) (map[string]bool, map[string]bool) {
	requires := map[string]bool{}
	localReplaces := map[string]bool{}
	block := ""
	for _, raw := range strings.Split(data, "\n") {
		line := stripGoModComment(raw)
		if line == "" {
			continue
		}
		if line == ")" && block != "" {
			block = ""
			continue
		}
		if line == "require (" {
			block = "require"
			continue
		}
		if line == "replace (" {
			block = "replace"
			continue
		}
		switch {
		case block == "require":
			if module := requireModule(line); module != "" {
				requires[module] = true
			}
		case block == "replace":
			if module, ok := localReplaceModule(line); ok {
				localReplaces[module] = true
			}
		case strings.HasPrefix(line, "require "):
			if module := requireModule(strings.TrimSpace(strings.TrimPrefix(line, "require "))); module != "" {
				requires[module] = true
			}
		case strings.HasPrefix(line, "replace "):
			if module, ok := localReplaceModule(strings.TrimSpace(strings.TrimPrefix(line, "replace "))); ok {
				localReplaces[module] = true
			}
		}
	}
	return requires, localReplaces
}

func stripGoModComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

func requireModule(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	return fields[0]
}

func localReplaceModule(line string) (string, bool) {
	left, right, ok := strings.Cut(line, "=>")
	if !ok {
		return "", false
	}
	leftFields := strings.Fields(strings.TrimSpace(left))
	rightFields := strings.Fields(strings.TrimSpace(right))
	if len(leftFields) == 0 || len(rightFields) == 0 {
		return "", false
	}
	if !isLocalReplaceTarget(rightFields[0]) {
		return "", false
	}
	return leftFields[0], true
}

func isLocalReplaceTarget(target string) bool {
	return target == "." ||
		strings.HasPrefix(target, "./") ||
		strings.HasPrefix(target, "../") ||
		filepath.IsAbs(target)
}

func prepareSandboxRepoSnapshotForPath(ctx context.Context, repoPath string) (string, string, func() error, error) {
	root, prefix := sandboxGitRootAndPrefix(ctx, repoPath)
	dir, cleanup, err := prepareSandboxRepoSnapshot(ctx, root)
	if err != nil {
		return "", "", nil, err
	}
	return dir, prefix, cleanup, nil
}

func sandboxGitRootAndPrefix(ctx context.Context, repoPath string) (string, string) {
	rootRaw, rootErr := gitOutput(ctx, repoPath, "rev-parse", "--show-toplevel")
	prefixRaw, prefixErr := gitOutput(ctx, repoPath, "rev-parse", "--show-prefix")
	if rootErr != nil || prefixErr != nil {
		return repoPath, ""
	}
	root := strings.TrimSpace(string(rootRaw))
	prefix := filepath.ToSlash(strings.TrimSpace(string(prefixRaw)))
	prefix = strings.TrimSuffix(prefix, "/")
	if root == "" || !filepath.IsAbs(root) || shouldSkipSandboxStagePath(prefix) {
		return repoPath, ""
	}
	return root, prefix
}

func prepareSandboxRepoSnapshot(ctx context.Context, repoPath string) (string, func() error, error) {
	files, err := sandboxRepoFileList(ctx, repoPath)
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "trpc-code-review-repo-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() error { return os.RemoveAll(dir) }
	for _, file := range files {
		if shouldSkipSandboxStagePath(file) {
			continue
		}
		if err := copySandboxFile(repoPath, dir, file); err != nil {
			_ = cleanup()
			return "", nil, err
		}
	}
	return dir, cleanup, nil
}

func sandboxRepoFileList(ctx context.Context, repoPath string) ([]string, error) {
	raw, err := gitOutput(ctx, repoPath, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err == nil {
		return splitNULFileList(raw), nil
	}
	return walkSandboxRepoFiles(repoPath)
}

func splitNULFileList(raw []byte) []string {
	parts := strings.Split(string(raw), "\x00")
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		file := normalizeSandboxRelPath(part)
		if file != "" {
			files = append(files, file)
		}
	}
	return files
}

func walkSandboxRepoFiles(repoPath string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(repoPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repoPath, path)
		if err != nil {
			return err
		}
		rel = normalizeSandboxRelPath(rel)
		if rel == "" {
			return nil
		}
		if d.IsDir() {
			if shouldSkipSandboxStagePath(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, rel)
		return nil
	})
	return files, err
}

func copySandboxFile(repoPath, snapshotDir, rel string) error {
	rel = normalizeSandboxRelPath(rel)
	if rel == "" || shouldSkipSandboxStagePath(rel) {
		return nil
	}
	src := filepath.Join(repoPath, filepath.FromSlash(rel))
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("stat staged file %s: %w", rel, err)
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil
	}
	dst := filepath.Join(snapshotDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open staged file %s: %w", rel, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create staged file %s: %w", rel, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy staged file %s: %w", rel, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close staged file %s: %w", rel, err)
	}
	return nil
}

func normalizeSandboxRelPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || path == "." || strings.HasPrefix(path, "/") {
		return ""
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return ""
	}
	return clean
}

func shouldSkipSandboxStagePath(path string) bool {
	path = normalizeSandboxRelPath(path)
	if path == "" {
		return true
	}
	lower := strings.ToLower(path)
	for _, part := range strings.Split(lower, "/") {
		switch part {
		case ".git", ".hg", ".svn", "node_modules":
			return true
		case ".env", ".env.local", ".env.production", ".netrc":
			return true
		case "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "credentials":
			return true
		}
	}
	switch {
	case strings.HasSuffix(lower, ".pem"),
		strings.HasSuffix(lower, ".key"),
		strings.HasSuffix(lower, ".p12"),
		strings.HasSuffix(lower, ".pfx"),
		strings.Contains(lower, "secret"):
		return true
	default:
		return false
	}
}

func limitText(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	return s[:max] + "\n...[truncated]", true
}

func classifySandboxError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "deadline") || strings.Contains(msg, "timeout"):
		return "timeout"
	case strings.Contains(msg, "executable file not found") || strings.Contains(msg, "not found"):
		return "tool_unavailable"
	default:
		return "executor_error"
	}
}

type NoopSandboxRunner struct {
	executorName string
}

func (r NoopSandboxRunner) Close() error { return nil }

func (r NoopSandboxRunner) RunChecks(ctx context.Context, taskID string, repoPath string, _ ParsedDiff) SandboxResult {
	policy := ReviewPermissionPolicy{TaskID: taskID}
	checks := []struct {
		cmd  string
		args []string
	}{
		{
			cmd:  "bash",
			args: []string{filepath.Join(codeexecutor.DirSkills, "code-review", "scripts", "diff_summary.sh"), filepath.Join(codeexecutor.DirWork, "change.diff"), filepath.Join(codeexecutor.DirOut, "diff_summary.json")},
		},
	}
	if strings.TrimSpace(repoPath) != "" {
		checks = append(checks,
			struct {
				cmd  string
				args []string
			}{cmd: "go", args: []string{"test", "./..."}},
			struct {
				cmd  string
				args []string
			}{cmd: "go", args: []string{"vet", "./..."}},
			struct {
				cmd  string
				args []string
			}{cmd: "staticcheck", args: []string{"./..."}},
		)
	}
	var runs []SandboxRun
	var decisions []PermissionDecisionRecord
	for _, check := range checks {
		record, decision, err := policy.Decide(ctx, check.cmd, check.args)
		decisions = append(decisions, record)
		run := SandboxRun{
			ID:        newID("run"),
			TaskID:    taskID,
			Command:   check.cmd,
			Args:      check.args,
			Executor:  r.executorName,
			Status:    "skipped",
			ErrorType: "dry_run",
			StartedAt: time.Now(),
			Stderr:    "sandbox command skipped by dry-run/fake executor after permission decision",
		}
		if err != nil {
			run.Status = "failed"
			run.ErrorType = "permission_error"
			run.Stderr = err.Error()
		} else if decision.Action != tool.PermissionActionAllow {
			run.Status = string(decision.Action)
			run.ErrorType = "permission_decision"
			run.Stderr = decision.Reason
		}
		runs = append(runs, run)
	}
	if strings.TrimSpace(repoPath) == "" {
		runs = append(runs, SandboxRun{
			ID:        newID("run"),
			TaskID:    taskID,
			Command:   "go",
			Args:      []string{"test", "./...", "vet", "./...", "staticcheck", "./..."},
			Executor:  r.executorName,
			Status:    "skipped",
			ErrorType: "no_repo_path",
			StartedAt: time.Now(),
			Stderr:    "repo path not provided; repository Go checks skipped in dry-run/fake executor",
		})
	}
	return SandboxResult{
		Runs:      runs,
		Decisions: decisions,
	}
}

type FakeFailSandboxRunner struct{}

func (FakeFailSandboxRunner) Close() error { return nil }

func (FakeFailSandboxRunner) RunChecks(ctx context.Context, taskID string, _ string, _ ParsedDiff) SandboxResult {
	record, _, _ := ReviewPermissionPolicy{TaskID: taskID}.Decide(ctx, "go", []string{"test", "./..."})
	runs := []SandboxRun{{
		ID:        newID("run"),
		TaskID:    taskID,
		Command:   "go",
		Args:      []string{"test", "./..."},
		Executor:  "fake-fail",
		Status:    "failed",
		ExitCode:  1,
		ErrorType: "executor_error",
		StartedAt: time.Now(),
		Stderr:    "service/handler.go:12: simulated sandbox failure",
	}}
	return SandboxResult{Runs: runs, Decisions: []PermissionDecisionRecord{record}, Findings: ParseSandboxFindings(runs)}
}

func exampleDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if isExampleRoot(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	candidate := filepath.Join(wd, "examples", "skills_code_review_agent")
	if isExampleRoot(candidate) {
		return candidate
	}
	return wd
}

func isExampleRoot(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "sandbox", "Dockerfile")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "code-review", "SKILL.md")); err != nil {
		return false
	}
	return true
}
