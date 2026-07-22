//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sandbox runs governed checks in production and fallback runtimes.
package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tcontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/strslice"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/governance"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
)

const (
	// ImageTag is the unique local image used by the review sandbox.
	ImageTag                = "trpc-agent-code-review:local"
	maxResultBytes          = 160 << 10
	outerGrace              = 10 * time.Second
	containerCleanupTimeout = 30 * time.Second
	runnerOutputBytes       = 64 << 10
	containerPIDs           = 128
	randomTokenBytes        = 8
	workspaceDiskBytes      = 512 << 20
	containerMemoryBytes    = 1 << 30
	containerNanoCPUs       = 1_000_000_000
	tmpfsSizeMegabytes      = 512
	resultFilePrefix        = "result-"
	workspaceIDPrefix       = "cr-"
	workspaceRoot           = "/tmp/run"
	stagingSourcePath       = "/opt/trpc-agent/skills"
	runnerRelativePath      = "scripts/checkrunner/main.go"
)

// ErrLifecycle classifies container workspace cleanup or executor close failures.
var ErrLifecycle = errors.New("sandbox lifecycle failure")

// Run records one sandbox check result and its bounded evidence.
type Run struct {
	CheckID, Runtime, Status, Stdout, Stderr, Error, Artifact, SHA256 string
	ContainerName                                                     string
	ExitCode                                                          int
	TimedOut, Truncated                                               bool
	Duration                                                          time.Duration
	ArtifactBytes                                                     int64
}
type runnerResult struct {
	CheckID         string `json:"check_id"`
	ExitCode        int    `json:"exit_code"`
	TimedOut        bool   `json:"timed_out"`
	DurationMS      int64  `json:"duration_ms"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	Error           string `json:"error"`
}

// Container executes approved checks with the production container runtime.
type Container struct {
	Authorizer                           governance.Authorizer
	BuildContext, SkillRoot, ModuleCache string
	factory                              func(string) (containerExecutor, error)
}
type containerExecutor interface {
	Engine() codeexecutor.Engine
	Close() error
}
type checkRequest struct {
	checkID, artifact, workspaceID, repoPath string
	timeout                                  time.Duration
	spec                                     governance.CheckSpec
}

// Check authorizes, stages, runs, collects, and destroys one container.
func (c Container) Check(ctx context.Context, checkID, repoPath string, timeout time.Duration) (result Run, resultErr error) {
	result = failedRun(checkID)
	proxy, err := buildModuleProxy(ctx, repoPath, c.ModuleCache)
	if err != nil {
		result.Error = redact.String(err.Error())
		return result, err
	}
	defer func() {
		if cleanupErr := proxy.Close(); cleanupErr != nil {
			result.Status = "failed"
			result.Error = redact.String(cleanupErr.Error())
			resultErr = errors.Join(resultErr, fmt.Errorf("%w: cleanup module proxy: %v", ErrLifecycle, cleanupErr))
		}
	}()
	token, err := randomToken()
	if err != nil {
		return result, err
	}
	artifact := resultFilePrefix + token + ".json"
	workspaceID := workspaceIDPrefix + token
	runnerSource := filepath.Join(c.SkillRoot, filepath.FromSlash(runnerRelativePath))
	spec := governance.CheckSpec{ID: checkID, Runtime: "container", RunnerPath: runnerSource, SkillRoot: c.SkillRoot, Cwd: "repo", Artifact: artifact, Argv: fixedArgv(checkID), Timeout: timeout, DependencyDigest: proxy.Digest, DependencyModules: proxy.Modules, DependencyBytes: proxy.Bytes, DependencyEntries: proxy.Entries, DependencyExpandedBytes: proxy.ExpandedBytes}
	executor, err := c.createExecutor(ctx, repoPath, workspaceID, proxy.Path)
	if err != nil {
		return result, err
	}
	request := checkRequest{checkID: checkID, artifact: artifact, workspaceID: workspaceID, repoPath: repoPath, timeout: timeout, spec: spec}
	result, runErr := executeContainer(ctx, executor, request, c.Authorizer)
	result.ContainerName = workspaceID
	closeErr := executor.Close()
	if closeErr != nil {
		result.Status = "failed"
		result.Error = redact.String(closeErr.Error())
		closeErr = fmt.Errorf("%w: close executor: %v", ErrLifecycle, closeErr)
	}
	return result, errors.Join(runErr, closeErr)
}
func executeContainer(ctx context.Context, executor containerExecutor, request checkRequest, authorizer governance.Authorizer) (run Run, resultErr error) {
	run = failedRun(request.checkID)
	engine := executor.Engine()
	if engine == nil || !engine.Describe().SupportsCleanEnv {
		return run, errors.New("container runtime does not support clean environment")
	}
	workspace, err := engine.Manager().CreateWorkspace(ctx, request.workspaceID, codeexecutor.WorkspacePolicy{Isolated: true, MaxDiskBytes: workspaceDiskBytes})
	if err != nil {
		return run, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), containerCleanupTimeout)
		defer cancel()
		if cleanupErr := engine.Manager().Cleanup(cleanupCtx, workspace); cleanupErr != nil {
			run.Status = "failed"
			run.Error = redact.String(cleanupErr.Error())
			resultErr = errors.Join(resultErr, fmt.Errorf("%w: cleanup workspace: %v", ErrLifecycle, cleanupErr))
		}
	}()
	request.spec.Env = targetEnv(workspace.Path)
	if err := authorizer.Authorize(ctx, request.spec); err != nil {
		return run, err
	}
	if err := engine.FS().StageDirectory(ctx, workspace, request.repoPath, codeexecutor.DirWork, codeexecutor.StageOptions{ReadOnly: true, AllowMount: true}); err != nil {
		return run, fmt.Errorf("stage reviewed repository: %w", err)
	}
	return runWorkspace(ctx, engine, workspace, request)
}
func runWorkspace(ctx context.Context, engine codeexecutor.Engine, workspace codeexecutor.Workspace, request checkRequest) (Run, error) {
	run := failedRun(request.checkID)
	outerTimeout := request.timeout + outerGrace
	callStarted := time.Now()
	outer, runErr := engine.Runner().RunProgram(ctx, workspace, codeexecutor.RunProgramSpec{Cmd: "/usr/local/bin/cr-checkrunner", Args: []string{"--check", request.checkID, "--result", request.artifact, "--timeout", request.timeout.String(), "--output-limit", fmt.Sprint(runnerOutputBytes)}, Env: request.spec.Env, CleanEnv: true, Cwd: ".", Timeout: outerTimeout})
	run.Duration = time.Since(callStarted)
	if runErr != nil {
		run.Error = redact.String(runErr.Error())
		return run, runErr
	}
	if outer.Stderr != "" {
		return run, fmt.Errorf("trusted runner emitted stderr: %q", redact.String(outer.Stderr))
	}
	if outer.TimedOut || outer.ExitCode != 0 {
		return run, fmt.Errorf("trusted runner failed: exit=%d timeout=%t", outer.ExitCode, outer.TimedOut)
	}
	if outer.Stdout != "" {
		return run, errors.New("trusted runner emitted stdout")
	}
	artifactPath := "out/" + request.artifact
	read, readErr := engine.Runner().RunProgram(ctx, workspace, codeexecutor.RunProgramSpec{
		Cmd: "/usr/bin/head", Args: []string{"-c", fmt.Sprint(maxResultBytes + 1), artifactPath},
		CleanEnv: true, Cwd: ".", Timeout: outerGrace,
	})
	if readErr != nil || read.ExitCode != 0 || read.TimedOut || read.Stderr != "" {
		return run, errors.Join(readErr, errors.New("read exact result artifact failed"))
	}
	collected, err := resultFromContent(read.Stdout, request)
	collected.Duration = run.Duration
	return collected, err
}
func resultFromContent(content string, request checkRequest) (Run, error) {
	run := failedRun(request.checkID)
	if len(content) > maxResultBytes {
		return run, errors.New("result exceeds artifact limit")
	}
	run.Artifact = "out/" + request.artifact
	run.ArtifactBytes = int64(len(content))
	digest := sha256.Sum256([]byte(content))
	run.SHA256 = hex.EncodeToString(digest[:])
	parsed, err := decodeRunnerResult(content)
	if err != nil {
		return run, fmt.Errorf("decode runner result: %w", err)
	}
	if parsed.CheckID != request.checkID {
		return run, errors.New("runner result check ID mismatch")
	}
	if err := validateRunnerResult(parsed, request); err != nil {
		return run, err
	}
	run.ExitCode, run.TimedOut = parsed.ExitCode, parsed.TimedOut
	run.Truncated = parsed.StdoutTruncated || parsed.StderrTruncated
	run.Stdout, run.Stderr, run.Error = redact.String(parsed.Stdout), redact.String(parsed.Stderr), redact.String(parsed.Error)
	if parsed.TimedOut {
		run.Status = "timeout"
	} else if parsed.ExitCode == 0 && parsed.Error == "" {
		run.Status = "completed"
	} else {
		run.Status = "failed"
	}
	return run, nil
}
func decodeRunnerResult(content string) (runnerResult, error) {
	var fields map[string]json.RawMessage
	fieldDecoder := json.NewDecoder(strings.NewReader(content))
	if err := fieldDecoder.Decode(&fields); err != nil {
		return runnerResult{}, err
	}
	if err := fieldDecoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return runnerResult{}, errors.New("runner result contains trailing JSON")
	}
	for _, name := range []string{"check_id", "exit_code", "timed_out", "duration_ms", "stdout", "stderr", "stdout_truncated", "stderr_truncated", "error"} {
		if _, ok := fields[name]; !ok {
			return runnerResult{}, fmt.Errorf("missing field %q", name)
		}
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	var parsed runnerResult
	if err := decoder.Decode(&parsed); err != nil {
		return runnerResult{}, err
	}
	return parsed, nil
}
func validateRunnerResult(result runnerResult, request checkRequest) error {
	if result.DurationMS < 0 || time.Duration(result.DurationMS)*time.Millisecond > request.timeout+outerGrace {
		return errors.New("runner result duration is outside bounds")
	}
	if len(result.Stdout) > runnerOutputBytes || len(result.Stderr) > runnerOutputBytes {
		return errors.New("runner result output exceeds bounds")
	}
	if result.TimedOut && result.ExitCode == 0 {
		return errors.New("timed out runner result has success exit code")
	}
	return nil
}
func failedRun(checkID string) Run {
	return Run{CheckID: checkID, Runtime: "container", Status: "failed", ExitCode: -1}
}
func (c Container) newExecutor(ctx context.Context, repoPath, containerName, proxyPath string) (*containerexec.CodeExecutor, error) {
	pids := int64(containerPIDs)
	repoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve staging source: %w", err)
	}
	executor, err := containerexec.New(containerexec.WithDockerFilePath(c.BuildContext), containerexec.WithContainerName(containerName), containerexec.WithContainerConfig(tcontainer.Config{Image: ImageTag, Cmd: []string{"tail", "-f", "/dev/null"}, WorkingDir: "/", User: "0:0", Tty: false, OpenStdin: false}), containerexec.WithHostConfig(tcontainer.HostConfig{AutoRemove: false, NetworkMode: "none", Privileged: false, ReadonlyRootfs: true, Binds: []string{repoPath + ":" + stagingSourcePath + ":ro", proxyPath + ":" + moduleProxyTarget + ":ro"}, CapDrop: strslice.StrSlice{"ALL"}, CapAdd: strslice.StrSlice{"SETUID", "SETGID", "KILL"}, SecurityOpt: []string{"no-new-privileges:true"}, Resources: tcontainer.Resources{Memory: containerMemoryBytes, MemorySwap: containerMemoryBytes, NanoCPUs: containerNanoCPUs, PidsLimit: &pids}, Tmpfs: map[string]string{"/tmp": fmt.Sprintf("rw,exec,nosuid,nodev,size=%dm,mode=1777", tmpfsSizeMegabytes)}}), containerexec.WithAutoInputs(false))
	if err != nil {
		return nil, err
	}
	return executor, nil
}
func (c Container) createExecutor(ctx context.Context, repoPath, containerName, proxyPath string) (containerExecutor, error) {
	if c.factory != nil {
		return c.factory(repoPath)
	}
	return c.newExecutor(ctx, repoPath, containerName, proxyPath)
}
func fixedArgv(checkID string) []string {
	if checkID == "go-vet" {
		return []string{"go", "vet", "-mod=readonly", "./..."}
	}
	return []string{"go", "test", "-mod=readonly", "./..."}
}
func targetEnv(workspace string) map[string]string {
	return map[string]string{"PATH": "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin", "HOME": "/tmp/cr-target/home", "GOCACHE": "/tmp/cr-target/gocache", "GOMODCACHE": "/tmp/cr-target/gomodcache", "TMPDIR": "/tmp/cr-target/tmp", "GOMAXPROCS": "2", "GOPROXY": "file://" + moduleProxyTarget, "GOSUMDB": "off", "GOENV": "off", "GOTOOLCHAIN": "local", "GOVCS": "*:off", "CR_RESULT_DIR": workspace + "/out", "CR_REPO_DIR": workspace + "/work"}
}
func randomToken() (string, error) {
	data := make([]byte, randomTokenBytes)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

// Fake executes no project code while preserving governance and audit flow.
type Fake struct {
	Authorizer governance.Authorizer
	SkillRoot  string
}

// Check authorizes and binds a deterministic fake result.
func (f Fake) Check(ctx context.Context, checkID, _ string, timeout time.Duration) (result Run, resultErr error) {
	started := time.Now()
	result = failedRun(checkID)
	result.Runtime = "fake"
	token, err := randomToken()
	if err != nil {
		return result, err
	}
	artifact := resultFilePrefix + token + ".json"
	spec := governance.CheckSpec{ID: checkID, Runtime: "fake", RunnerPath: filepath.Join(f.SkillRoot, filepath.FromSlash(runnerRelativePath)), SkillRoot: f.SkillRoot, Cwd: "repo", Artifact: artifact, Argv: fixedArgv(checkID), Timeout: timeout, Env: targetEnv(workspaceRoot + "/ws_" + workspaceIDPrefix + token + "_0"), DependencyDigest: emptyProxyDigest()}
	if err := f.Authorizer.Authorize(ctx, spec); err != nil {
		return result, err
	}
	content := []byte("fake:" + checkID)
	digest := sha256.Sum256(content)
	result.Status, result.ExitCode = "completed", 0
	result.Artifact, result.ArtifactBytes = "out/"+artifact, int64(len(content))
	result.SHA256 = hex.EncodeToString(digest[:])
	result.Duration = time.Since(started)
	return result, nil
}

const (
	localOutputBytes     = 64 << 10
	localDirectoryMode   = 0o700
	localTerminationWait = 2 * time.Second
)

// Local is an explicitly enabled development fallback. It declares host write
// and network capability to governance; it is not a production sandbox.
type Local struct {
	Authorizer governance.Authorizer
	SkillRoot  string
	WorkRoot   string
}

// Check runs on the host and is only an explicitly approved development fallback.
func (l Local) Check(ctx context.Context, checkID, repoPath string, timeout time.Duration) (result Run, resultErr error) {
	started := time.Now()
	result = failedRun(checkID)
	result.Runtime = "local"
	repoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return result, err
	}
	info, err := os.Stat(repoPath)
	if err != nil || !info.IsDir() {
		return result, errors.Join(errors.New("local repository is not a directory"), err)
	}
	workRoot, err := l.createWorkRoot(repoPath)
	if err != nil {
		return result, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, os.RemoveAll(workRoot))
	}()
	token, err := randomToken()
	if err != nil {
		return result, err
	}
	spec := governance.CheckSpec{ID: checkID, Runtime: "local", Network: true, HostWrite: true, RunnerPath: filepath.Join(l.SkillRoot, filepath.FromSlash(runnerRelativePath)), SkillRoot: l.SkillRoot, Cwd: "repo", Artifact: resultFilePrefix + token + ".json", RepoSource: repoPath, Argv: fixedArgv(checkID), Timeout: timeout, Env: localEnvironment(workRoot, repoPath)}
	if err := l.Authorizer.Authorize(ctx, spec); err != nil {
		return result, err
	}
	result, err = runLocalCommand(ctx, spec)
	result.Duration = time.Since(started)
	return result, err
}
func (l Local) createWorkRoot(repoPath string) (string, error) {
	root := l.WorkRoot
	if root == "" {
		root = os.TempDir()
	}
	if err := os.MkdirAll(root, localDirectoryMode); err != nil {
		return "", fmt.Errorf("create local work root: %w", err)
	}
	path, err := os.MkdirTemp(root, ".cr-local-")
	if err != nil {
		return "", fmt.Errorf("create local check workspace: %w", err)
	}
	for _, name := range []string{"home", "gocache", "gomodcache", "tmp"} {
		if err := os.Mkdir(filepath.Join(path, name), localDirectoryMode); err != nil {
			return "", errors.Join(fmt.Errorf("create local cache directory: %w", err), os.RemoveAll(path))
		}
	}
	return path, nil
}
func runLocalCommand(ctx context.Context, spec governance.CheckSpec) (result Run, resultErr error) {
	result = failedRun(spec.ID)
	result.Runtime = "local"
	timedCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	// spec.Argv was matched against fixedArgv by governance before execution.
	//nolint:gosec
	command := exec.CommandContext(timedCtx, spec.Argv[0], spec.Argv[1:]...)
	command.Dir = spec.RepoSource
	command.Env = environmentList(spec.Env)
	command.WaitDelay = localTerminationWait
	stdout, stderr := &limitedBuffer{limit: localOutputBytes}, &limitedBuffer{limit: localOutputBytes}
	command.Stdout, command.Stderr = stdout, stderr
	err := command.Run()
	result.Stdout, result.Stderr = redact.String(stdout.String()), redact.String(stderr.String())
	result.Truncated = stdout.truncated || stderr.truncated
	result.ExitCode = processExitCode(command)
	if errors.Is(timedCtx.Err(), context.DeadlineExceeded) {
		result.Status, result.TimedOut = "timeout", true
		result.Error = redact.String(timedCtx.Err().Error())
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.Status, result.Error = "failed", redact.String(exitErr.Error())
		return result, nil
	}
	if err != nil {
		result.Error = redact.String(err.Error())
		return result, fmt.Errorf("run local check: %w", err)
	}
	result.Status, result.ExitCode = "completed", 0
	return result, nil
}
func localEnvironment(root, repoPath string) map[string]string {
	temporary := filepath.Join(root, "tmp")
	return map[string]string{"PATH": os.Getenv("PATH"), "HOME": filepath.Join(root, "home"), "GOCACHE": filepath.Join(root, "gocache"), "GOMODCACHE": filepath.Join(root, "gomodcache"), "GOTMPDIR": temporary, "TMPDIR": temporary, "TMP": temporary, "TEMP": temporary, "GOMAXPROCS": "2", "GOPROXY": "off", "GOSUMDB": "off", "GOENV": "off", "GOWORK": "off", "CR_REPO_DIR": repoPath, "SYSTEMROOT": os.Getenv("SYSTEMROOT")}
}
func environmentList(values map[string]string) []string {
	keys := []string{"PATH", "HOME", "GOCACHE", "GOMODCACHE", "GOTMPDIR", "TMPDIR", "TMP", "TEMP", "GOMAXPROCS", "GOPROXY", "GOSUMDB", "GOENV", "GOWORK", "CR_REPO_DIR", "SYSTEMROOT"}
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}
func processExitCode(command *exec.Cmd) int {
	if command.ProcessState == nil {
		return -1
	}
	return command.ProcessState.ExitCode()
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if remaining > len(value) {
			remaining = len(value)
		}
		if _, err := b.buffer.Write(value[:remaining]); err != nil {
			return 0, err
		}
	}
	if original > remaining {
		b.truncated = true
	}
	return original, nil
}
func (b *limitedBuffer) String() string {
	return b.buffer.String()
}
