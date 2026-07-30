//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewinput

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type gitClient struct {
	timeout        time.Duration
	maxOutputBytes int64
	maxDiffBytes   int64
}

type commandResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

type gitOutputBuffer struct {
	buffer   bytes.Buffer
	maxBytes int64
	exceeded bool
	cancel   context.CancelFunc
}

func (b *gitOutputBuffer) Write(data []byte) (int, error) {
	accepted := len(data)
	remaining := b.maxBytes - int64(b.buffer.Len())
	if remaining > 0 {
		keep := min(int64(len(data)), remaining)
		_, _ = b.buffer.Write(data[:keep])
	}
	if int64(len(data)) > remaining {
		b.exceeded = true
		b.cancel()
	}
	return accepted, nil
}

// run treats an ordinary non-zero Git exit as data rather than immediately as
// a Go error. Callers need to distinguish expected statuses such as
// `git diff --no-index` returning 1 from genuine command failures.
func (g gitClient) run(ctx context.Context, dir string, stdin []byte, args ...string) (result commandResult, err error) {
	defaults := (Limits{}).withDefaults()
	if g.timeout <= 0 {
		g.timeout = defaults.GitTimeout
	}
	if g.maxOutputBytes <= 0 {
		g.maxOutputBytes = defaults.MaxGitOutputBytes
	}
	commandCtx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "git", args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	stdout := gitOutputBuffer{maxBytes: g.maxOutputBytes, cancel: cancel}
	stderr := gitOutputBuffer{maxBytes: g.maxOutputBytes, cancel: cancel}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	result = commandResult{
		stdout: stdout.buffer.Bytes(),
		stderr: stderr.buffer.Bytes(),
	}
	if stdout.exceeded || stderr.exceeded {
		return result, fmt.Errorf(
			"git output exceeds the %d-byte limit",
			g.maxOutputBytes,
		)
	}
	if err == nil {
		return result, nil
	}
	if commandCtx.Err() != nil {
		return result, fmt.Errorf("git %s: %w", strings.Join(args, " "), commandCtx.Err())
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.exitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, fmt.Errorf("run git %s: %w", strings.Join(args, " "), err)
}

func (g gitClient) resolveRoot(ctx context.Context, repoPath string) (root string, err error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return "", fmt.Errorf("resolve repo path: %w", err)
	}
	result, err := g.run(ctx, abs, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if result.exitCode != 0 {
		return "", fmt.Errorf("repo path is not a Git worktree: %s", conciseCommandError(result))
	}
	root = strings.TrimSpace(string(result.stdout))
	if root == "" {
		return "", errors.New("git returned an empty worktree root")
	}
	return filepath.Abs(root)
}

// collectDiff builds one review diff from staged and unstaged tracked changes
// relative to HEAD plus untracked, non-ignored files. An unborn repository has
// no HEAD, so every indexed or untracked file is represented as an addition.
func (g gitClient) collectDiff(ctx context.Context, root string, paths []string) (diff []byte, err error) {
	head, err := g.run(ctx, root, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return nil, err
	}
	hasHead := head.exitCode == 0
	var combined bytes.Buffer
	if hasHead {
		args := []string{"diff", "--binary", "--find-renames", "--no-ext-diff", "--no-textconv", "HEAD", "--"}
		args = append(args, paths...)
		result, err := g.run(ctx, root, nil, args...)
		if err != nil {
			return nil, err
		}
		if result.exitCode != 0 {
			return nil, fmt.Errorf("read Git worktree diff: %s", conciseCommandError(result))
		}
		if err := g.appendDiff(&combined, result.stdout); err != nil {
			return nil, err
		}
	}

	untracked, err := g.listUntracked(ctx, root, paths, !hasHead)
	if err != nil {
		return nil, err
	}
	for _, rel := range untracked {
		// git diff --no-index reports a difference with exit code 1. It reads
		// files only and does not mutate the user's index or working tree.
		result, err := g.run(ctx, root, nil, "diff", "--no-index", "--binary", "--", "/dev/null", rel)
		if err != nil {
			return nil, err
		}
		if result.exitCode != 0 && result.exitCode != 1 {
			return nil, fmt.Errorf("build diff for untracked file %s: %s", rel, conciseCommandError(result))
		}
		if err := g.appendDiff(&combined, result.stdout); err != nil {
			return nil, err
		}
	}
	if len(bytes.TrimSpace(combined.Bytes())) == 0 {
		return nil, errors.New("Git worktree has no changes in the requested review scope")
	}
	return combined.Bytes(), nil
}

func (g gitClient) appendDiff(combined *bytes.Buffer, data []byte) error {
	if g.maxDiffBytes <= 0 {
		g.maxDiffBytes = (Limits{}).withDefaults().MaxDiffBytes
	}
	if int64(len(data)) > g.maxDiffBytes-int64(combined.Len()) {
		return fmt.Errorf(
			"git diff exceeds the %d-byte limit",
			g.maxDiffBytes,
		)
	}
	_, _ = combined.Write(data)
	return nil
}

// listUntracked normally returns only untracked files. includeTracked is used
// for an unborn repository, where every indexed file also needs an addition
// diff because no HEAD exists to cover it.
func (g gitClient) listUntracked(ctx context.Context, root string, paths []string, includeTracked bool) (files []string, err error) {
	args := []string{"ls-files", "--others", "--exclude-standard", "-z"}
	if includeTracked {
		args = []string{"ls-files", "--cached", "--others", "--exclude-standard", "-z"}
	}
	args = append(args, "--")
	args = append(args, paths...)
	result, err := g.run(ctx, root, nil, args...)
	if err != nil {
		return nil, err
	}
	if result.exitCode != 0 {
		return nil, fmt.Errorf("list Git input files: %s", conciseCommandError(result))
	}
	return splitNUL(result.stdout), nil
}

func (g gitClient) listSnapshotFiles(ctx context.Context, root string) (files []string, err error) {
	tracked, err := g.run(ctx, root, nil, "ls-files", "--stage", "-z", "--cached", "--")
	if err != nil {
		return nil, err
	}
	if tracked.exitCode != 0 {
		return nil, fmt.Errorf("list tracked repository snapshot files: %s", conciseCommandError(tracked))
	}
	for _, record := range splitNUL(tracked.stdout) {
		tab := strings.IndexByte(record, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("parse tracked snapshot entry %q", record)
		}
		metadata := strings.Fields(record[:tab])
		if len(metadata) != 3 {
			return nil, fmt.Errorf("parse tracked snapshot metadata %q", record[:tab])
		}
		if metadata[0] == "160000" {
			continue
		}
		files = append(files, filepath.ToSlash(record[tab+1:]))
	}
	untracked, err := g.listUntracked(ctx, root, nil, false)
	if err != nil {
		return nil, err
	}
	return append(files, untracked...), nil
}

// ensureDiffApplied makes a supplied patch and repo snapshot describe the same
// post-change tree. A successful reverse check means the caller supplied an
// already-patched worktree; otherwise a forward check must succeed before the
// task-owned snapshot is modified.
func (g gitClient) ensureDiffApplied(ctx context.Context, snapshot string, diff []byte) error {
	reverse, err := g.run(ctx, snapshot, diff, "apply", "--no-index", "--reverse", "--check", "-")
	if err != nil {
		return err
	}
	if reverse.exitCode == 0 {
		return nil
	}
	forward, err := g.run(ctx, snapshot, diff, "apply", "--no-index", "--check", "-")
	if err != nil {
		return err
	}
	if forward.exitCode != 0 {
		return fmt.Errorf("repo snapshot does not match the supplied diff: %s", conciseCommandError(forward))
	}
	apply, err := g.run(ctx, snapshot, diff, "apply", "--no-index", "-")
	if err != nil {
		return err
	}
	if apply.exitCode != 0 {
		return fmt.Errorf("apply supplied diff to repo snapshot: %s", conciseCommandError(apply))
	}
	return nil
}

func splitNUL(data []byte) []string {
	parts := bytes.Split(data, []byte{0})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			out = append(out, filepath.ToSlash(string(part)))
		}
	}
	return out
}

func conciseCommandError(result commandResult) string {
	message := strings.TrimSpace(string(result.stderr))
	if message == "" {
		message = strings.TrimSpace(string(result.stdout))
	}
	if len(message) > 1024 {
		message = message[:1024] + "...(truncated)"
	}
	if message == "" {
		message = fmt.Sprintf("exit code %d", result.exitCode)
	}
	return message
}
