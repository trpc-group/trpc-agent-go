//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package input 把支持的审查输入收敛成 unified diff，并提取最小 Go 工程元数据。
package input

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const (
	defaultMaxInputBytes int64 = 1 << 20
	gitCommandTimeout          = 30 * time.Second
)

// Config 保存输入加载配置。
type Config struct {
	// FixturesRoot 是受控 fixture 目录。
	FixturesRoot string
	// MaxInputBytes limits loaded or generated diff input.
	MaxInputBytes int64
}

// Request 描述一次审查输入来源。
type Request struct {
	// DiffFile 是外部 unified diff 文件。
	DiffFile string
	// FileList 是按行分隔的变更文件列表。
	FileList string
	// RepoPath 是本地 Git 工作区或普通目录。
	RepoPath string
	// Fixture 是 Config.FixturesRoot 下的 fixture 名称。
	Fixture string
	// BaseRef 是 repo diff 的基础 git ref。
	BaseRef string
	// HeadRef 是 repo diff 的目标 git ref。
	HeadRef string
}

// Read 读取或生成 unified diff 输入。
func Read(cfg Config, req Request) ([]byte, string, error) {
	maxBytes := normalizeMaxInputBytes(cfg.MaxInputBytes)
	if req.DiffFile != "" {
		b, err := readFileWithLimit(req.DiffFile, maxBytes, "diff file")
		return b, req.DiffFile, err
	}
	if req.FileList != "" {
		b, err := diffFromFileList(req.FileList, req.RepoPath, maxBytes)
		return b, req.FileList, err
	}
	if req.Fixture != "" {
		return readFixtureInput(cfg.FixturesRoot, req.Fixture, maxBytes)
	}
	if req.RepoPath != "" {
		b, err := diffFromRepo(req.RepoPath, req.BaseRef, req.HeadRef, maxBytes)
		return b, req.RepoPath, err
	}
	return nil, "", errors.New("diff file, file list, repo path, or fixture is required")
}

func readFixtureInput(root string, name string, maxBytes int64) ([]byte, string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, "", errors.New("fixtures root is required")
	}
	cleanName := filepath.Clean(strings.TrimSpace(name))
	if cleanName == "." || filepath.IsAbs(cleanName) || strings.HasPrefix(cleanName, "..") {
		return nil, "", fmt.Errorf("invalid fixture name %q", name)
	}
	path := filepath.Join(root, cleanName)
	b, err := readFileWithLimit(path, maxBytes, "fixture")
	return b, path, err
}

func diffFromRepo(repoPath string, baseRef string, headRef string, maxBytes int64) ([]byte, error) {
	if repoPath == "" {
		return nil, errors.New("repo path is required")
	}
	worktree, err := isGitWorktree(repoPath)
	if err != nil {
		return nil, err
	}
	if worktree {
		if hasExplicitRefs(baseRef, headRef) {
			return runGitDiff(gitDiffArgs(repoPath, baseRef, headRef), maxBytes)
		}
		return diffFromGitWorktree(repoPath, maxBytes)
	}
	return diffFromDirectory(repoPath, maxBytes)
}

func gitDiffArgs(repoPath string, baseRef string, headRef string) []string {
	args := []string{"-C", repoPath, "diff", "--no-ext-diff", "--no-textconv", "--unified=3"}
	if strings.TrimSpace(baseRef) != "" && strings.TrimSpace(headRef) != "" {
		args = append(args, strings.TrimSpace(baseRef)+"..."+strings.TrimSpace(headRef))
	}
	return args
}

func hasExplicitRefs(baseRef string, headRef string) bool {
	return strings.TrimSpace(baseRef) != "" && strings.TrimSpace(headRef) != ""
}

func diffFromGitWorktree(repoPath string, maxBytes int64) ([]byte, error) {
	b := newLimitedBuffer(maxBytes, "git worktree diff")

	tracked, err := diffTrackedGitChanges(repoPath, b.Remaining())
	if err != nil {
		return nil, err
	}
	if _, err := b.Write(tracked); err != nil {
		return nil, err
	}

	untrackedLimit := b.Remaining()
	if len(tracked) > 0 && !bytes.HasSuffix(tracked, []byte{'\n'}) && untrackedLimit > 0 {
		untrackedLimit--
	}
	untracked, err := diffUntrackedGitFiles(repoPath, untrackedLimit)
	if err != nil {
		return nil, err
	}
	if len(tracked) > 0 && len(untracked) > 0 && !bytes.HasSuffix(tracked, []byte{'\n'}) {
		if err := b.WriteByte('\n'); err != nil {
			return nil, err
		}
	}
	if _, err := b.Write(untracked); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func diffTrackedGitChanges(repoPath string, maxBytes int64) ([]byte, error) {
	hasHead, err := gitHeadExists(repoPath)
	if err != nil {
		return nil, err
	}
	if hasHead {
		return runGitDiff([]string{"-C", repoPath, "diff", "--no-ext-diff", "--no-textconv", "--unified=3", "HEAD"}, maxBytes)
	}
	b := newLimitedBuffer(maxBytes, "git tracked diff")
	staged, err := runGitDiff([]string{"-C", repoPath, "diff", "--cached", "--no-ext-diff", "--no-textconv", "--unified=3"}, b.Remaining())
	if err != nil {
		return nil, err
	}
	if _, err := b.Write(staged); err != nil {
		return nil, err
	}
	unstagedLimit := b.Remaining()
	if len(staged) > 0 && !bytes.HasSuffix(staged, []byte{'\n'}) && unstagedLimit > 0 {
		unstagedLimit--
	}
	unstaged, err := runGitDiff([]string{"-C", repoPath, "diff", "--no-ext-diff", "--no-textconv", "--unified=3"}, unstagedLimit)
	if err != nil {
		return nil, err
	}
	if len(staged) > 0 && len(unstaged) > 0 && !bytes.HasSuffix(staged, []byte{'\n'}) {
		if err := b.WriteByte('\n'); err != nil {
			return nil, err
		}
	}
	if _, err := b.Write(unstaged); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func diffUntrackedGitFiles(repoPath string, maxBytes int64) ([]byte, error) {
	_, repoPrefix, err := gitRepoContext(repoPath, maxBytes)
	if err != nil {
		return nil, err
	}
	out, err := runGitCommand(
		[]string{"-C", repoPath, "ls-files", "--others", "--exclude-standard", "-z"},
		maxBytes,
		"git ls-files output",
	)
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	b := newLimitedBuffer(maxBytes, "untracked diff")
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		path := filepath.Join(repoPath, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("stat untracked file %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("untracked file %q is not a regular file", name)
		}
		content, err := readFileWithLimit(path, b.Remaining(), fmt.Sprintf("untracked file %q", name))
		if err != nil {
			return nil, fmt.Errorf("read untracked file %q: %w", name, err)
		}
		display := filepath.ToSlash(filepath.Join(repoPrefix, filepath.FromSlash(name)))
		if err := diffForNewFile(b, display, content); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

func gitHeadExists(repoPath string) (bool, error) {
	_, err := runGitCommand([]string{"-C", repoPath, "rev-parse", "--verify", "HEAD"}, 1024, "git HEAD check")
	if err == nil {
		return true, nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false, err
	}
	return false, nil
}

func gitRepoContext(repoPath string, maxBytes int64) (string, string, error) {
	root, err := runGitCommand([]string{"-C", repoPath, "rev-parse", "--show-toplevel"}, maxBytes, "git repo root")
	if err != nil {
		return "", "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	prefix, err := runGitCommand([]string{"-C", repoPath, "rev-parse", "--show-prefix"}, maxBytes, "git repo prefix")
	if err != nil {
		return "", "", fmt.Errorf("git rev-parse --show-prefix: %w", err)
	}
	return strings.TrimSpace(string(root)), strings.TrimSpace(string(prefix)), nil
}

func runGitDiff(args []string, maxBytes int64) ([]byte, error) {
	out, err := runGitCommand(args, maxBytes, "git diff output")
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	return out, nil
}

func isGitWorktree(repoPath string) (bool, error) {
	out, err := runGitCommand([]string{"-C", repoPath, "rev-parse", "--is-inside-work-tree"}, 1024, "git worktree check")
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		return false, nil
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

func diffFromDirectory(repoPath string, maxBytes int64) ([]byte, error) {
	b := newLimitedBuffer(maxBytes, "directory diff")
	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(repoPath, entry.Name())
		content, err := readFileWithLimit(path, b.Remaining(), fmt.Sprintf("directory file %q", entry.Name()))
		if err != nil {
			continue
		}
		if err := diffForNewFile(b, entry.Name(), content); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

func diffFromFileList(listPath string, repoPath string, maxBytes int64) ([]byte, error) {
	content, err := readFileWithLimit(listPath, maxBytes, "file list")
	if err != nil {
		return nil, err
	}
	baseDir := filepath.Dir(listPath)
	restrictToBase := false
	if strings.TrimSpace(repoPath) != "" {
		baseDir = repoPath
		restrictToBase = true
	}
	b := newLimitedBuffer(maxBytes, "file list diff")
	for _, raw := range strings.Split(string(content), "\n") {
		name := strings.TrimSpace(raw)
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		path, display, err := resolveListedFile(name, baseDir, restrictToBase)
		if err != nil {
			return nil, err
		}
		fileContent, err := readFileWithLimit(path, b.Remaining(), fmt.Sprintf("listed file %q", name))
		if err != nil {
			return nil, fmt.Errorf("read listed file %q: %w", name, err)
		}
		if err := diffForNewFile(b, display, fileContent); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

func resolveListedFile(name string, baseDir string, restrictToBase bool) (string, string, error) {
	path := filepath.Clean(name)
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	display := filepath.Base(path)
	if rel, err := filepath.Rel(baseDir, path); err == nil {
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if restrictToBase || !filepath.IsAbs(name) {
				return "", "", fmt.Errorf("listed file %q escapes base directory", name)
			}
		} else {
			display = rel
		}
	}
	if !restrictToBase {
		return path, display, nil
	}
	resolvedBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve base directory %q: %w", baseDir, err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve listed file %q: %w", name, err)
	}
	if rel, err := filepath.Rel(resolvedBase, resolvedPath); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("listed file %q escapes base directory", name)
	}
	return resolvedPath, display, nil
}

func diffForNewFile(b *limitedBuffer, name string, content []byte) error {
	display := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(name), string(filepath.Separator)))
	lines := contentLines(content)
	if _, err := fmt.Fprintf(b, "diff --git a/%s b/%s\n", display, display); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(b, "--- /dev/null\n+++ b/%s\n", display); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(b, "@@ -0,0 +1,%d @@\n", len(lines)); err != nil {
		return err
	}
	for _, line := range lines {
		if _, err := fmt.Fprintf(b, "+%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func contentLines(content []byte) []string {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		return lines[:len(lines)-1]
	}
	return lines
}

// Metadata 从 diff 输入中提取最小 Go 工程元数据。
func Metadata(diff []byte, repoPath string) review.InputMetadata {
	parsed, err := review.ParseUnifiedDiff(string(diff))
	if err != nil {
		return review.InputMetadata{ModulePath: modulePath(repoPath)}
	}
	goFiles := map[string]struct{}{}
	packages := map[string]struct{}{}
	testFiles := map[string]struct{}{}
	for _, file := range parsed.Files {
		if !strings.HasSuffix(file.Path, ".go") {
			continue
		}
		goFiles[file.Path] = struct{}{}
		if strings.HasSuffix(file.Path, "_test.go") {
			testFiles[file.Path] = struct{}{}
		}
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if pkg := packageNameFromLine(line.Text); pkg != "" {
					packages[pkg] = struct{}{}
				}
			}
		}
	}
	return review.InputMetadata{
		ChangedGoFiles:   sortedKeys(goFiles),
		PackageNames:     sortedKeys(packages),
		ModulePath:       modulePath(repoPath),
		HasTests:         len(testFiles) > 0,
		TouchedTestFiles: sortedKeys(testFiles),
	}
}

// MetadataForRequest 提取元数据，并补充请求中的 git refs。
func MetadataForRequest(diff []byte, req Request) review.InputMetadata {
	meta := Metadata(diff, req.RepoPath)
	meta.BaseRef = strings.TrimSpace(req.BaseRef)
	meta.HeadRef = strings.TrimSpace(req.HeadRef)
	return meta
}

func packageNameFromLine(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) >= 2 && fields[0] == "package" {
		return fields[1]
	}
	return ""
}

func modulePath(repoPath string) string {
	if strings.TrimSpace(repoPath) == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1]
		}
	}
	return ""
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, filepath.ToSlash(value))
	}
	sort.Strings(out)
	return out
}

var errInputTooLarge = errors.New("input size limit exceeded")

func normalizeMaxInputBytes(n int64) int64 {
	if n <= 0 {
		return defaultMaxInputBytes
	}
	return n
}

func readFileWithLimit(path string, maxBytes int64, label string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	if info, err := file.Stat(); err == nil && info.Mode().IsRegular() && info.Size() > maxBytes {
		return nil, sizeLimitError(label, maxBytes)
	}

	buf := newLimitedBuffer(maxBytes, label)
	if _, err := io.Copy(buf, io.LimitReader(file, maxBytes+1)); err != nil && !errors.Is(err, errInputTooLarge) {
		return nil, err
	}
	if err := buf.Err(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func runGitCommand(args []string, maxBytes int64, label string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	stdout := newLimitedBuffer(maxBytes, label)
	stderr := newLimitedBuffer(maxBytes, label)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("git command timed out: %w", ctx.Err())
	}
	if outErr := stdout.Err(); outErr != nil {
		return nil, outErr
	}
	if errErr := stderr.Err(); errErr != nil {
		return nil, errErr
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", err, message)
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct {
	buf     bytes.Buffer
	max     int64
	label   string
	written int64
	err     error
}

func newLimitedBuffer(maxBytes int64, label string) *limitedBuffer {
	return &limitedBuffer{max: maxBytes, label: label}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if b.written+int64(len(p)) > b.max {
		remaining := b.max - b.written
		if remaining > 0 {
			n, _ := b.buf.Write(p[:remaining])
			b.written += int64(n)
		}
		b.err = sizeLimitError(b.label, b.max)
		return 0, b.err
	}
	n, err := b.buf.Write(p)
	b.written += int64(n)
	return n, err
}

func (b *limitedBuffer) WriteByte(c byte) error {
	_, err := b.Write([]byte{c})
	return err
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buf.Bytes()
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}

func (b *limitedBuffer) Remaining() int64 {
	if b.max-b.written <= 0 {
		return 0
	}
	return b.max - b.written
}

func (b *limitedBuffer) Err() error {
	return b.err
}

func sizeLimitError(label string, maxBytes int64) error {
	return fmt.Errorf("%w: %s exceeds %d bytes", errInputTooLarge, label, maxBytes)
}
