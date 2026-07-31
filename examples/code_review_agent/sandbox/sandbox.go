// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package sandbox adapts the native trpc-agent-go workspace runtimes for code review checks.
package sandbox

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	tcontainer "github.com/docker/docker/api/types/container"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

// ExecutorType selects the native workspace backend.
type ExecutorType string

const (
	ExecutorLocal     ExecutorType = "local"
	ExecutorContainer ExecutorType = "container"
	ExecutorE2B       ExecutorType = "e2b"
)

// SandboxConfig controls native workspace execution.
type SandboxConfig struct {
	ExecutorType  ExecutorType
	Timeout       time.Duration
	MaxOutputSize int
	EnvWhitelist  []string
}

func DefaultSandboxConfig() *SandboxConfig {
	return &SandboxConfig{
		ExecutorType:  ExecutorContainer,
		Timeout:       5 * time.Minute,
		MaxOutputSize: 1024 * 1024,
		EnvWhitelist:  []string{"PATH", "LANG", "LC_ALL", "CGO_ENABLED", "GOPROXY", "HOME"},
	}
}

type Executor struct {
	repoPath string
	config   *SandboxConfig
	factory  func() (codeexecutor.CodeExecutor, error)
}

func NewExecutor(repoPath string) *Executor {
	return &Executor{repoPath: repoPath, config: DefaultSandboxConfig()}
}

func NewExecutorWithType(repoPath string, executorType ExecutorType) *Executor {
	config := DefaultSandboxConfig()
	config.ExecutorType = executorType
	return &Executor{repoPath: repoPath, config: config}
}

// WithExecutorFactory is intended for deterministic tests of the workspace adapter.
func (e *Executor) WithExecutorFactory(factory func() (codeexecutor.CodeExecutor, error)) *Executor {
	e.factory = factory
	return e
}

func (e *Executor) RunAllChecks(ctx context.Context, taskID string) ([]store.SandboxRun, error) {
	if strings.TrimSpace(e.repoPath) == "" {
		return nil, fmt.Errorf("repository path is required")
	}
	if e.config.Timeout <= 0 {
		return nil, fmt.Errorf("sandbox timeout must be positive")
	}

	exec, err := e.newExecutor()
	if err != nil {
		return nil, err
	}
	engineProvider, ok := exec.(codeexecutor.EngineProvider)
	if !ok {
		return nil, fmt.Errorf("executor %q does not expose a workspace engine", e.config.ExecutorType)
	}
	engine := engineProvider.Engine()
	if engine == nil {
		return nil, fmt.Errorf("executor %q returned a nil workspace engine", e.config.ExecutorType)
	}

	runCtx, cancel := context.WithTimeout(ctx, e.config.Timeout)
	defer cancel()
	ws, err := engine.Manager().CreateWorkspace(runCtx, taskID, codeexecutor.WorkspacePolicy{Isolated: true})
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	defer func() { _ = engine.Manager().Cleanup(context.Background(), ws) }()

	if err := engine.FS().StageDirectory(runCtx, ws, e.repoPath, "work", codeexecutor.StageOptions{ReadOnly: true, AllowMount: false}); err != nil {
		return nil, fmt.Errorf("stage repository: %w", err)
	}

	if e.config.ExecutorType == ExecutorE2B {
		// E2B templates may not preserve toolchain state between RunProgram
		// calls. Keep all Go checks in one remote shell invocation.
		checks := "go test ./... && go vet ./..."
		if e.hasStaticcheck(exec) {
			checks += " && staticcheck ./..."
		}
		run := e.runProgram(runCtx, engine, ws, taskID, "go_test_vet", "/bin/bash", []string{"-lc", checks}, nil)
		return []store.SandboxRun{run}, nil
	}

	runs := make([]store.SandboxRun, 0, 3)
	runs = append(runs, e.runProgram(runCtx, engine, ws, taskID, "go_test", "go", []string{"test", "./..."}, nil))
	if runCtx.Err() != nil {
		return runs, runCtx.Err()
	}
	runs = append(runs, e.runProgram(runCtx, engine, ws, taskID, "go_vet", "go", []string{"vet", "./..."}, nil))
	if runCtx.Err() != nil {
		return runs, runCtx.Err()
	}
	if e.hasStaticcheck(exec) {
		runs = append(runs, e.runProgram(runCtx, engine, ws, taskID, "staticcheck", "staticcheck", []string{"./..."}, nil))
	}
	return runs, nil
}

func (e *Executor) newExecutor() (codeexecutor.CodeExecutor, error) {
	if e.factory != nil {
		return e.factory()
	}
	switch e.config.ExecutorType {
	case ExecutorContainer:
		return containerexec.New(containerexec.WithContainerConfig(containerConfig()))
	case ExecutorE2B:
		apiKey := strings.TrimSpace(os.Getenv("E2B_API_KEY"))
		if apiKey == "" {
			return nil, fmt.Errorf("E2B_API_KEY is required for the e2b executor")
		}
		opts := []e2bexec.Option{
			e2bexec.WithAPIKey(apiKey),
			e2bexec.WithExecutionTimeout(e.config.Timeout),
		}
		if apiURL := strings.TrimSpace(os.Getenv("E2B_API_URL")); apiURL != "" {
			opts = append(opts, e2bexec.WithAPIURL(apiURL))
		}
		if template := strings.TrimSpace(os.Getenv("E2B_TEMPLATE")); template != "" {
			opts = append(opts, e2bexec.WithTemplate(template))
		}
		return e2bexec.New(opts...)
	case ExecutorLocal:
		return localexec.New(localexec.WithTimeout(e.config.Timeout)), nil
	default:
		return nil, fmt.Errorf("unknown executor type %q", e.config.ExecutorType)
	}
}

func containerConfig() tcontainer.Config {
	return tcontainer.Config{
		Image:      "golang:1.25-bookworm",
		WorkingDir: "/",
		Cmd:        []string{"tail", "-f", "/dev/null"},
		Tty:        true,
		OpenStdin:  true,
	}
}

func (e *Executor) hasStaticcheck(_ codeexecutor.CodeExecutor) bool {
	return strings.TrimSpace(os.Getenv("GOLENS_STATICCHECK")) == "1"
}

func (e *Executor) runProgram(ctx context.Context, engine codeexecutor.Engine, ws codeexecutor.Workspace, taskID, script, command string, args []string, extraEnv map[string]string) store.SandboxRun {
	start := time.Now()
	env := make(map[string]string, len(e.config.EnvWhitelist))
	for _, name := range e.config.EnvWhitelist {
		if value, ok := os.LookupEnv(name); ok {
			env[name] = value
		}
	}
	if e.config.ExecutorType == ExecutorContainer || e.config.ExecutorType == ExecutorE2B {
		env["PATH"] = "/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		env["HOME"] = path.Join(ws.Path, "home")
		env["GOCACHE"] = path.Join(ws.Path, "cache")
		env["GOPATH"] = path.Join(ws.Path, "gopath")
		env["GOMODCACHE"] = path.Join(ws.Path, "gomodcache")
		env["TMPDIR"] = path.Join(ws.Path, "tmp")
	}
	for key, value := range extraEnv {
		if key == "GOROOT" && strings.HasPrefix(value, "/") && !strings.Contains(value, " ") {
			env[key] = value
		}
	}
	result, err := engine.Runner().RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:      command,
		Args:     args,
		Env:      env,
		CleanEnv: true,
		Cwd:      "work",
		Timeout:  e.config.Timeout,
		Limits:   codeexecutor.ResourceLimits{MemoryMB: 1024, MaxPIDs: 128},
	})
	exitCode := result.ExitCode
	if err != nil && exitCode == 0 {
		exitCode = -1
	}
	if result.TimedOut || ctx.Err() == context.DeadlineExceeded {
		exitCode = -2
	}
	stdout := result.Stdout
	stderr := result.Stderr
	truncated := false
	if e.config.MaxOutputSize > 0 {
		if len(stdout) > e.config.MaxOutputSize {
			stdout = stdout[:e.config.MaxOutputSize]
			truncated = true
		}
		if len(stderr) > e.config.MaxOutputSize {
			stderr = stderr[:e.config.MaxOutputSize]
			truncated = true
		}
	}
	if err != nil && stderr == "" {
		stderr = err.Error()
	}
	return store.SandboxRun{TaskID: taskID, ScriptName: script, Command: command + " " + strings.Join(args, " "), ExitCode: exitCode, Stdout: stdout, Stderr: stderr, DurationMs: int(time.Since(start).Milliseconds()), Truncated: truncated}
}
