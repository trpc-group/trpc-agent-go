//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package localpython provides a shared local Python process runtime for
// generated-code features. It only manages process-level hardening such as
// environment filtering, working directories, timeout, and process cleanup;
// feature-specific guest protocols stay in the caller packages.
package localpython

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/envscrub"
)

const defaultMaxCodeBytes = 64 << 10

var pythonEnvBlocklist = map[string]struct{}{
	"PYTHONHOME":     {},
	"PYTHONPATH":     {},
	"PYTHONSTARTUP":  {},
	"PYTHONUSERBASE": {},
}

// Config controls a local Python process. The zero value selects python3,
// applies the default code-size limit, uses a minimal hardened environment,
// creates a temporary working directory, and adds no local runtime timeout.
type Config struct {
	// Python selects the Python interpreter. The default is python3.
	Python string
	// Timeout optionally bounds the local process lifetime. The zero value
	// relies on the caller's context without adding a local runtime deadline.
	Timeout time.Duration
	// MaxCodeBytes bounds the generated user code size before launching Python.
	// The default is 64 KiB. Use a negative value to disable this limit.
	MaxCodeBytes int
	// Env sets extra process environment variables. Local Python filters
	// shell, loader, and Python preload/search-path variables, and always
	// enforces its Python hardening environment. Do not pass secrets or the
	// full host environment unless each variable is intentionally available to
	// generated code.
	Env []string
	// WorkDir sets the process working directory. When empty, Local Python
	// creates an empty temporary directory and removes it after the process
	// exits. WorkDir is not automatically added to Python's module search path.
	WorkDir string
}

// Process is a running local Python process.
type Process struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	cleanup     func()
	cleanupOnce sync.Once

	// Dir is the process working directory.
	Dir string
}

// StartScript writes script to a private runtime directory and starts Python
// with interpreterArgs, the script path, and scriptArgs after validating code
// against cfg. The private script directory keeps modules in cfg.WorkDir from
// shadowing imports performed by the runtime bootstrap.
func StartScript(
	ctx context.Context,
	cfg Config,
	code string,
	scriptName string,
	script []byte,
	interpreterArgs []string,
	scriptArgs []string,
	stderr io.Writer,
) (*Process, error) {
	if err := ValidateCodeSize(code, cfg.MaxCodeBytes); err != nil {
		return nil, err
	}
	if strings.TrimSpace(scriptName) == "" {
		return nil, fmt.Errorf("localpython: script name is required")
	}
	workDir, cleanupWorkDir, err := resolveWorkDir(cfg.WorkDir)
	if err != nil {
		return nil, err
	}
	scriptDir := workDir
	cleanupScriptDir := func() {}
	if strings.TrimSpace(cfg.WorkDir) != "" {
		scriptDir, err = os.MkdirTemp("", "trpc-local-python-script-*")
		if err != nil {
			cleanupWorkDir()
			return nil, fmt.Errorf("localpython: create script dir: %w", err)
		}
		cleanupScriptDir = func() { _ = os.RemoveAll(scriptDir) }
	}
	scriptPath := filepath.Join(scriptDir, filepath.Base(scriptName))
	if err := os.WriteFile(scriptPath, script, 0o600); err != nil {
		cleanupScriptDir()
		cleanupWorkDir()
		return nil, fmt.Errorf("localpython: write script: %w", err)
	}
	args := make([]string, 0, len(interpreterArgs)+1+len(scriptArgs))
	args = append(args, interpreterArgs...)
	args = append(args, scriptPath)
	args = append(args, scriptArgs...)
	proc, err := startResolved(ctx, cfg, workDir, args, stderr)
	if err != nil {
		cleanupScriptDir()
		cleanupWorkDir()
		return nil, err
	}
	previousCleanup := proc.cleanup
	proc.cleanup = func() {
		previousCleanup()
		cleanupScriptDir()
		cleanupWorkDir()
	}
	return proc, nil
}

// ValidateCodeSize checks generated user code against maxBytes.
func ValidateCodeSize(code string, maxBytes int) error {
	if maxBytes < 0 {
		return nil
	}
	limit := maxBytes
	if limit == 0 {
		limit = defaultMaxCodeBytes
	}
	if len([]byte(code)) > limit {
		return fmt.Errorf("localpython: code exceeds %d bytes", limit)
	}
	return nil
}

func startResolved(
	ctx context.Context,
	cfg Config,
	workDir string,
	args []string,
	stderr io.Writer,
) (*Process, error) {
	python := strings.TrimSpace(cfg.Python)
	if python == "" {
		python = "python3"
	}
	pythonPath, err := exec.LookPath(python)
	if err != nil {
		return nil, fmt.Errorf("localpython: resolve Python: %w", err)
	}
	if !filepath.IsAbs(pythonPath) {
		pythonPath, err = filepath.Abs(pythonPath)
		if err != nil {
			return nil, fmt.Errorf("localpython: resolve absolute Python path: %w", err)
		}
	}
	runCtx := ctx
	cancel := func() {}
	if cfg.Timeout > 0 {
		var cancelFn context.CancelFunc
		runCtx, cancelFn = context.WithTimeout(ctx, cfg.Timeout)
		cancel = cancelFn
	}
	cmd := exec.CommandContext(runCtx, pythonPath, args...)
	cmd.Dir = workDir
	cmd.Env = HardenedEnv(cfg.Env)
	cmd.Stderr = stderr
	configureProcess(cmd)
	cmd.Cancel = func() error {
		return killProcessGroup(cmd)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("localpython: create stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("localpython: create stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("localpython: start Python: %w", err)
	}
	return &Process{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		Dir:    workDir,
		cleanup: func() {
			cancel()
		},
	}, nil
}

func resolveWorkDir(configured string) (string, func(), error) {
	workDir := strings.TrimSpace(configured)
	if workDir != "" {
		return workDir, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "trpc-local-python-*")
	if err != nil {
		return "", nil, fmt.Errorf("localpython: create workdir: %w", err)
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// HardenedEnv returns a minimal environment plus allowed extra variables.
func HardenedEnv(extra []string) []string {
	env := make([]string, 0, len(extra)+2)
	for _, value := range extra {
		key, val, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || isBlockedEnvKey(key) || isForcedEnvKey(key) {
			continue
		}
		env = append(env, key+"="+val)
	}
	env = append(env,
		"PYTHONIOENCODING=utf-8",
		"PYTHONNOUSERSITE=1",
	)
	return env
}

func isBlockedEnvKey(key string) bool {
	if envscrub.IsMalformedKey(key) || envscrub.IsBlocked(key, true) {
		return true
	}
	_, ok := pythonEnvBlocklist[strings.ToUpper(key)]
	return ok
}

func isForcedEnvKey(key string) bool {
	switch strings.ToUpper(key) {
	case "PYTHONIOENCODING", "PYTHONNOUSERSITE":
		return true
	default:
		return false
	}
}

// Stdin returns the process stdin pipe.
func (p *Process) Stdin() io.WriteCloser { return p.stdin }

// Stdout returns the process stdout pipe.
func (p *Process) Stdout() io.ReadCloser { return p.stdout }

// Kill terminates the process. On supported Unix-like systems, it kills the
// process group; on other systems it falls back to killing the root process.
func (p *Process) Kill() error {
	if p == nil {
		return nil
	}
	return killProcessGroup(p.cmd)
}

// Wait waits for process exit and runs cleanup once.
func (p *Process) Wait() error {
	if p == nil || p.cmd == nil {
		return nil
	}
	err := p.cmd.Wait()
	cleanupProcessTree(p.cmd)
	p.cleanupOnce.Do(func() {
		if p.cleanup != nil {
			p.cleanup()
		}
	})
	return err
}
