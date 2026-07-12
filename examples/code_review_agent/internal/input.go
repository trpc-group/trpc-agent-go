//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const maxInputDiffBytes = 16 * 1024 * 1024

// LoadReviewInput reads a unified diff or obtains the current Git workspace
// diff. FilePaths limits a workspace review to the named repository-relative
// paths. Git is invoked without external diff helpers or optional locks.
func LoadReviewInput(ctx context.Context, input ReviewInput) ([]byte, string, error) {
	if input.DiffFile != "" {
		data, err := os.ReadFile(input.DiffFile)
		if err != nil {
			return nil, "", fmt.Errorf("read diff file: %w", err)
		}
		if len(data) > maxInputDiffBytes {
			return nil, "", fmt.Errorf("diff exceeds %d-byte limit", maxInputDiffBytes)
		}
		return data, "diff", nil
	}
	if input.RepoPath == "" {
		return nil, "", fmt.Errorf("either diff file or repository path is required")
	}

	repo, err := filepath.Abs(input.RepoPath)
	if err != nil {
		return nil, "", fmt.Errorf("resolve repository path: %w", err)
	}
	args := []string{"-c", "diff.external=", "diff", "--no-ext-diff", "--no-textconv", "HEAD", "--"}
	for _, name := range input.FilePaths {
		name = filepath.Clean(name)
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return nil, "", fmt.Errorf("file path must stay inside repository: %q", name)
		}
		args = append(args, name)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo
	cmd.Env = append(filteredGitEnv(), "GIT_OPTIONAL_LOCKS=0")
	var stdout, stderr limitedBuffer
	stdout.limit = maxInputDiffBytes
	stderr.limit = 64 * 1024
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("read git workspace diff: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.exceeded {
		return nil, "", fmt.Errorf("git diff exceeds %d-byte limit", maxInputDiffBytes)
	}
	return stdout.Bytes(), "git-workspace", nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.exceeded = true
		return original, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.exceeded = true
	}
	_, _ = b.Buffer.Write(p)
	return original, nil
}

func filteredGitEnv() []string {
	allowed := map[string]bool{"PATH": true, "HOME": true, "USERPROFILE": true, "SYSTEMROOT": true, "TMP": true, "TEMP": true}
	var env []string
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok && allowed[strings.ToUpper(key)] {
			env = append(env, item)
		}
	}
	return env
}
