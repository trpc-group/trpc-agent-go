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
	"io"
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
		info, err := os.Stat(input.DiffFile)
		if err != nil {
			return nil, "", fmt.Errorf("stat diff file: %w", err)
		}
		if info.Size() > maxInputDiffBytes {
			return nil, "", fmt.Errorf("diff exceeds %d-byte limit", maxInputDiffBytes)
		}
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
	if err := appendUntrackedDiffs(ctx, repo, input.FilePaths, &stdout); err != nil {
		return nil, "", err
	}
	if stdout.exceeded {
		return nil, "", fmt.Errorf("git diff exceeds %d-byte limit", maxInputDiffBytes)
	}
	return stdout.Bytes(), "git-workspace", nil
}

func appendUntrackedDiffs(ctx context.Context, repo string, filePaths []string, output *limitedBuffer) error {
	args := []string{"ls-files", "--others", "--exclude-standard", "-z", "--"}
	args = append(args, filePaths...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo
	cmd.Env = append(filteredGitEnv(), "GIT_OPTIONAL_LOCKS=0")
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("list untracked files: %w", err)
	}
	for _, rawName := range bytes.Split(data, []byte{0}) {
		if len(rawName) == 0 {
			continue
		}
		name := filepath.ToSlash(string(rawName))
		path := filepath.Join(repo, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("lstat untracked file %q: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("untracked file %q is not a regular file", name)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve untracked file %q: %w", name, err)
		}
		inside, err := pathInside(repo, resolved)
		if err != nil {
			return fmt.Errorf("validate untracked file %q: %w", name, err)
		}
		if !inside {
			return fmt.Errorf("untracked file %q resolves outside repository", name)
		}
		if info.Size() > int64(maxInputDiffBytes-output.Len()) {
			return fmt.Errorf("git diff exceeds %d-byte limit", maxInputDiffBytes)
		}
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open untracked file %q: %w", name, err)
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil || !os.SameFile(info, openedInfo) {
			_ = file.Close()
			if statErr != nil {
				return fmt.Errorf("restat untracked file %q: %w", name, statErr)
			}
			return fmt.Errorf("untracked file %q changed while opening", name)
		}
		content, readErr := io.ReadAll(io.LimitReader(file, int64(maxInputDiffBytes-output.Len())+1))
		closeErr := file.Close()
		if readErr != nil {
			return fmt.Errorf("read untracked file %q: %w", name, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close untracked file %q: %w", name, closeErr)
		}
		if len(content) > maxInputDiffBytes-output.Len() {
			return fmt.Errorf("git diff exceeds %d-byte limit", maxInputDiffBytes)
		}
		if bytes.IndexByte(content, 0) >= 0 {
			return fmt.Errorf("untracked binary file %q cannot be reviewed safely", name)
		}
		lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
		if len(content) == 0 {
			lines = nil
		}
		header := fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", name, name, name, len(lines))
		_, _ = output.Write([]byte(header))
		for _, line := range lines {
			_, _ = output.Write([]byte("+" + strings.TrimSuffix(line, "\r") + "\n"))
		}
	}
	return nil
}

func pathInside(root, candidate string) (bool, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return false, err
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false, err
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel), nil
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
