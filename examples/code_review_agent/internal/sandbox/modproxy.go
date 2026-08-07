//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sandbox provides a local Go module proxy for offline sandbox execution.
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ModuleProxy prepares a local module cache for use in the sandbox.
// It resolves and caches dependencies so sandbox execution does not
// require network access.
type ModuleProxy struct {
	cacheDir string
	goPath   string
}

// NewModuleProxy creates a module proxy rooted at cacheDir.
func NewModuleProxy(cacheDir string) *ModuleProxy {
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "cr-mod-cache")
	}
	return &ModuleProxy{cacheDir: cacheDir, goPath: cacheDir}
}

// Setup ensures the module cache is populated for the given repo path.
// It runs `go mod download` in the repo directory with GOMODCACHE set.
func (mp *ModuleProxy) Setup(repoPath string) error {
	if err := os.MkdirAll(mp.cacheDir, 0o755); err != nil {
		return fmt.Errorf("create module cache dir: %w", err)
	}

	cmd := exec.Command("go", "mod", "download")
	cmd.Dir = repoPath
	cmd.Env = append(os.Environ(),
		"GOMODCACHE="+mp.cacheDir,
		"GOPATH="+mp.goPath,
		"GOFLAGS=-mod=mod",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go mod download: %w\n%s", err, string(output))
	}
	return nil
}

// Env returns environment variables to inject for sandbox execution
// using the module cache.
func (mp *ModuleProxy) Env() map[string]string {
	return map[string]string{
		"GOMODCACHE":   mp.cacheDir,
		"GOPATH":       mp.goPath,
		"GOCACHE":      filepath.Join(mp.cacheDir, "go-cache"),
		"GOFLAGS":      "-mod=mod",
		"GONOSUMDB":    "*",
		"GONOSUMCHECK": "*",
		"GOINSECURE":   "*",
		"GOPROXY":      "off",
	}
}

// EnvSlice returns the environment as key=value strings for exec.Cmd.Env.
func (mp *ModuleProxy) EnvSlice() []string {
	env := mp.Env()
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, k+"="+v)
	}
	return result
}
