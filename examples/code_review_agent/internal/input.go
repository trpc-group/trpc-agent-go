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
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const maxInputDiffBytes = 16 * 1024 * 1024

// LoadReviewInput reads a unified diff or obtains the current Git workspace
// diff. FilePaths limits a workspace review to the named repository-relative
// paths. Git is invoked without external diff helpers or optional locks.
func LoadReviewInput(ctx context.Context, input ReviewInput) ([]byte, string, error) {
	return loadReviewInputAt(ctx, input, "HEAD")
}

// loadReviewInputAt is the pinned-commit variant used by Review. Resolving
// HEAD before reading the workspace prevents a concurrent ref update from
// changing the baseline between diff capture and sandbox construction.
func loadReviewInputAt(
	ctx context.Context,
	input ReviewInput,
	baseCommit string,
) ([]byte, string, error) {
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
	pathspecs, err := literalGitPathspecs(input.FilePaths)
	if err != nil {
		return nil, "", err
	}
	if baseCommit == "" {
		return nil, "", fmt.Errorf("workspace review base commit is required")
	}
	data, err := loadGitWorkspaceDiffAt(ctx, repo, pathspecs, baseCommit)
	if err != nil {
		return nil, "", err
	}
	return data, "git-workspace", nil
}

func loadGitWorkspaceDiffAt(
	ctx context.Context,
	repo string,
	pathspecs []string,
	baseCommit string,
) ([]byte, error) {
	commit, err := resolveGitCommit(ctx, repo, baseCommit)
	if err != nil {
		return nil, err
	}
	view, cleanup, err := newIsolatedGitView(ctx, repo, commit)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	args := []string{
		"diff",
		"--no-ext-diff",
		"--no-textconv",
		"--ignore-submodules=dirty",
		"--",
	}
	args = append(args, pathspecs...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo
	cmd.Env = view.env
	var stdout, stderr limitedBuffer
	stdout.limit = maxInputDiffBytes
	stderr.limit = 64 * 1024
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("read git workspace diff: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.exceeded {
		return nil, fmt.Errorf("git diff exceeds %d-byte limit", maxInputDiffBytes)
	}
	if err := appendUntrackedDiffsWithEnv(
		ctx,
		repo,
		pathspecs,
		&stdout,
		maxInputDiffBytes,
		view.env,
	); err != nil {
		return nil, err
	}
	if stdout.exceeded {
		return nil, fmt.Errorf("git diff exceeds %d-byte limit", maxInputDiffBytes)
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func literalGitPathspecs(filePaths []string) ([]string, error) {
	pathspecs := make([]string, 0, len(filePaths))
	for _, name := range filePaths {
		if name == "" {
			return nil, fmt.Errorf("file path cannot be empty")
		}
		name = filepath.Clean(name)
		if name == "." {
			return nil, fmt.Errorf("file path must name a file or directory inside the repository")
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("file path must stay inside repository: %q", name)
		}
		// Disable Git pathspec magic. A caller-provided filename such as
		// :(exclude)* must be interpreted literally, never as a selector that
		// changes which files are reviewed.
		pathspecs = append(pathspecs, ":(literal)"+filepath.ToSlash(name))
	}
	return pathspecs, nil
}

func appendUntrackedDiffs(ctx context.Context, repo string, pathspecs []string, output *limitedBuffer) error {
	return appendUntrackedDiffsWithPathLimit(ctx, repo, pathspecs, output, maxInputDiffBytes)
}

func appendUntrackedDiffsWithPathLimit(
	ctx context.Context,
	repo string,
	pathspecs []string,
	output *limitedBuffer,
	pathListLimit int,
) error {
	return appendUntrackedDiffsWithEnv(
		ctx,
		repo,
		pathspecs,
		output,
		pathListLimit,
		isolatedGitEnv(),
	)
}

func appendUntrackedDiffsWithEnv(
	ctx context.Context,
	repo string,
	pathspecs []string,
	output *limitedBuffer,
	pathListLimit int,
	env []string,
) error {
	args := []string{"ls-files", "--others", "--exclude-standard", "-z", "--"}
	args = append(args, pathspecs...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo
	cmd.Env = env
	var names, stderr limitedBuffer
	names.limit = pathListLimit
	stderr.limit = 64 * 1024
	cmd.Stdout, cmd.Stderr = &names, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("list untracked files: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if names.exceeded {
		return fmt.Errorf("untracked path list exceeds %d-byte limit", pathListLimit)
	}
	for _, rawName := range bytes.Split(names.Bytes(), []byte{0}) {
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
		oldToken := quoteGitPathToken("a/" + name)
		newToken := quoteGitPathToken("b/" + name)
		gitMode := "100644"
		if openedInfo.Mode()&0o111 != 0 {
			gitMode = "100755"
		}
		lineCount := bytes.Count(content, []byte{'\n'})
		if len(content) > 0 && content[len(content)-1] != '\n' {
			lineCount++
		}
		header := fmt.Sprintf(
			"diff --git %s %s\nnew file mode %s\n--- /dev/null\n+++ %s\n@@ -0,0 +1,%d @@\n",
			oldToken,
			newToken,
			gitMode,
			newToken,
			lineCount,
		)
		_, _ = output.Write([]byte(header))
		for len(content) > 0 {
			newline := bytes.IndexByte(content, '\n')
			_, _ = output.Write([]byte("+"))
			if newline < 0 {
				_, _ = output.Write(content)
				_, _ = output.Write([]byte("\n\\ No newline at end of file\n"))
				break
			}
			_, _ = output.Write(content[:newline+1])
			content = content[newline+1:]
		}
	}
	return nil
}

type isolatedGitView struct {
	env []string
}

// newIsolatedGitView creates throwaway Git metadata whose index is the fixed
// review commit. Host repository config is deliberately not loaded: a
// repository can otherwise execute core.fsmonitor or clean/process filters
// while a supposedly read-only diff is captured. Attributes come from an
// empty tree so built-in conversions cannot make two different workspace byte
// streams produce the same review diff.
func newIsolatedGitView(
	ctx context.Context,
	root string,
	commit string,
) (*isolatedGitView, func(), error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, func() {}, fmt.Errorf("resolve repository: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, func() {}, fmt.Errorf("resolve repository links: %w", err)
	}
	objectDir, err := repositoryObjectDir(ctx, root)
	if err != nil {
		return nil, func() {}, err
	}
	if strings.ContainsAny(objectDir, "\r\n") {
		return nil, func() {}, fmt.Errorf("Git object directory contains a line break")
	}
	gitDir, err := os.MkdirTemp("", "code-review-git-")
	if err != nil {
		return nil, func() {}, fmt.Errorf("create isolated Git metadata: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(gitDir) }
	if err := os.MkdirAll(filepath.Join(gitDir, "objects", "info"), 0o700); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("create isolated Git object directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0o700); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("create isolated Git refs: %w", err)
	}
	coreCompatibility := ""
	if runtime.GOOS == "windows" {
		coreCompatibility = "\tfilemode = false\n\tignorecase = true\n"
	}
	config := "[core]\n\trepositoryformatversion = 0\n\tbare = false\n" +
		coreCompatibility
	switch len(commit) {
	case 40:
	case 64:
		config = "[core]\n\trepositoryformatversion = 1\n\tbare = false\n" +
			coreCompatibility +
			"[extensions]\n\tobjectformat = sha256\n"
	default:
		cleanup()
		return nil, func() {}, fmt.Errorf("unsupported Git object ID length")
	}
	for path, content := range map[string]string{
		"HEAD":   "ref: refs/heads/isolated\n",
		"config": config,
		filepath.Join("objects", "info", "alternates"): objectDir + "\n",
	} {
		if err := os.WriteFile(filepath.Join(gitDir, path), []byte(content), 0o600); err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("initialize isolated Git metadata: %w", err)
		}
	}
	env := append(
		isolatedGitEnv(),
		"GIT_DIR="+gitDir,
		"GIT_WORK_TREE="+root,
		"GIT_INDEX_FILE="+filepath.Join(gitDir, "index"),
	)
	emptyTree, err := runIsolatedGit(
		ctx,
		root,
		env,
		strings.NewReader(""),
		"create empty attribute tree",
		"hash-object",
		"-w",
		"-t",
		"tree",
		"--stdin",
	)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	emptyTree = strings.TrimSpace(emptyTree)
	if _, err := hex.DecodeString(emptyTree); err != nil || len(emptyTree) != len(commit) {
		cleanup()
		return nil, func() {}, fmt.Errorf("Git returned invalid empty tree ID")
	}
	env = append(env, "GIT_ATTR_SOURCE="+emptyTree)
	if _, err := runIsolatedGit(
		ctx,
		root,
		env,
		nil,
		"initialize isolated review index",
		"read-tree",
		commit,
	); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return &isolatedGitView{env: env}, cleanup, nil
}

func repositoryObjectDir(ctx context.Context, root string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-path", "objects")
	cmd.Dir = root
	cmd.Env = isolatedGitEnv()
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = 16*1024, 64*1024
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf(
			"resolve Git object directory: %w: %s",
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	if stdout.exceeded {
		return "", fmt.Errorf("Git object directory exceeds output limit")
	}
	objectDir := strings.TrimSpace(stdout.String())
	if !filepath.IsAbs(objectDir) {
		objectDir = filepath.Join(root, objectDir)
	}
	objectDir, err := filepath.EvalSymlinks(objectDir)
	if err != nil {
		return "", fmt.Errorf("resolve Git object directory links: %w", err)
	}
	info, err := os.Stat(objectDir)
	if err != nil {
		return "", fmt.Errorf("stat Git object directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("Git object directory is not a directory")
	}
	return objectDir, nil
}

func runIsolatedGit(
	ctx context.Context,
	root string,
	env []string,
	stdin io.Reader,
	operation string,
	args ...string,
) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	cmd.Env = env
	cmd.Stdin = stdin
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = 16*1024, 64*1024
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", operation, err, strings.TrimSpace(stderr.String()))
	}
	if stdout.exceeded {
		return "", fmt.Errorf("%s output exceeds limit", operation)
	}
	return stdout.String(), nil
}

// quoteGitPathToken emits the byte-oriented C quoting used by Git diff
// metadata. Quoting every synthetic path avoids ambiguity for whitespace,
// quotes, control bytes, and non-ASCII UTF-8.
func quoteGitPathToken(value string) string {
	var quoted strings.Builder
	quoted.Grow(len(value) + 2)
	quoted.WriteByte('"')
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch character {
		case '\a':
			quoted.WriteString(`\a`)
		case '\b':
			quoted.WriteString(`\b`)
		case '\t':
			quoted.WriteString(`\t`)
		case '\n':
			quoted.WriteString(`\n`)
		case '\v':
			quoted.WriteString(`\v`)
		case '\f':
			quoted.WriteString(`\f`)
		case '\r':
			quoted.WriteString(`\r`)
		case '\\', '"':
			quoted.WriteByte('\\')
			quoted.WriteByte(character)
		default:
			if character < 0x20 || character >= 0x7f {
				_, _ = fmt.Fprintf(&quoted, `\%03o`, character)
			} else {
				quoted.WriteByte(character)
			}
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
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
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.exceeded = true
		return original, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.exceeded = true
	}
	_, _ = b.buffer.Write(p)
	return original, nil
}

func (b *limitedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *limitedBuffer) Len() int { return b.buffer.Len() }

func (b *limitedBuffer) String() string { return b.buffer.String() }

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

func isolatedGitEnv() []string {
	return append(
		filteredGitEnv(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=5",
		"GIT_CONFIG_KEY_0=core.fsmonitor",
		"GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=core.hooksPath",
		"GIT_CONFIG_VALUE_1="+os.DevNull,
		"GIT_CONFIG_KEY_2=diff.external",
		"GIT_CONFIG_VALUE_2=",
		"GIT_CONFIG_KEY_3=protocol.allow",
		"GIT_CONFIG_VALUE_3=never",
		"GIT_CONFIG_KEY_4=submodule.recurse",
		"GIT_CONFIG_VALUE_4=false",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_ALLOW_PROTOCOL=",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_NO_REPLACE_OBJECTS=1",
	)
}

func resolveGitCommit(ctx context.Context, root, ref string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", ref+"^{commit}")
	cmd.Dir = root
	cmd.Env = isolatedGitEnv()
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = 1024, 64*1024
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf(
			"resolve Git commit %q: %w: %s",
			ref,
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	if stdout.exceeded {
		return "", fmt.Errorf("resolved Git commit %q exceeds output limit", ref)
	}
	commit := strings.TrimSpace(stdout.String())
	raw, err := hex.DecodeString(commit)
	if err != nil || (len(raw) != 20 && len(raw) != 32) {
		return "", fmt.Errorf("Git returned invalid commit ID for %q", ref)
	}
	return commit, nil
}
