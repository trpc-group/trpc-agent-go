//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package inputsource reads the review input modes supported by the example.
package inputsource

import (
	"bufio"
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

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const (
	emptyTreeHash          = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	maxReviewInputBytes    = int64(512 << 20)
	maxUntrackedInputBytes = int64(512 << 20)
	maxInputFileCount      = 100_000
	maxUntrackedFileCount  = maxInputFileCount
	maxUntrackedListBytes  = int64(16 << 20)
	maxGitConfigBytes      = int64(1 << 20)
)

// Options describes all supported review input sources.
type Options struct {
	FixtureDir string
	DiffFile   string
	RepoPath   string
	FileList   string
}

// Source is the normalized input handed to the review orchestrator.
type Source struct {
	Type         string
	Diff         string
	FixtureNames []string
	FileList     []string
	RepoPath     string
	WorkDir      string
	Summary      string
}

// Read resolves exactly one configured input source. Fixture input remains the
// deterministic default used by tests and golden reports.
func Read(ctx context.Context, opts Options) (Source, error) {
	if err := ctx.Err(); err != nil {
		return Source{}, err
	}
	selected := configured(opts)
	if strings.TrimSpace(opts.DiffFile) != "" && strings.TrimSpace(opts.FileList) != "" {
		return Source{}, fmt.Errorf("choose only one input source: %s", strings.Join(selected, ", "))
	}
	switch {
	case strings.TrimSpace(opts.DiffFile) != "":
		return readDiffFile(opts.DiffFile, opts.RepoPath)
	case strings.TrimSpace(opts.FileList) != "":
		return readFileList(opts.FileList, opts.RepoPath)
	case strings.TrimSpace(opts.RepoPath) != "":
		return readRepoDiff(ctx, opts.RepoPath)
	default:
		dir := opts.FixtureDir
		if dir == "" {
			dir = "testdata/fixtures"
		}
		return readFixtures(dir)
	}
}

func configured(opts Options) []string {
	var out []string
	if strings.TrimSpace(opts.DiffFile) != "" {
		out = append(out, "--diff-file")
	}
	if strings.TrimSpace(opts.RepoPath) != "" {
		out = append(out, "--repo-path")
	}
	if strings.TrimSpace(opts.FileList) != "" {
		out = append(out, "--file-list")
	}
	return out
}

func readFixtures(dir string) (Source, error) {
	return readFixturesWithLimit(dir, maxReviewInputBytes)
}

func readFixturesWithLimit(dir string, maxBytes int64) (Source, error) {
	return readFixturesWithLimits(dir, maxBytes, maxInputFileCount)
}

func readFixturesWithLimits(dir string, maxBytes int64, maxFiles int) (Source, error) {
	names, err := readFixtureNames(dir, maxFiles)
	if err != nil {
		return Source{}, err
	}
	var b strings.Builder
	for _, name := range names {
		remaining := maxBytes - int64(b.Len())
		if b.Len() > 0 {
			remaining--
		}
		if remaining < 0 {
			return Source{}, fmt.Errorf("fixture input exceeds %d bytes", maxBytes)
		}
		raw, err := readFileLimited(filepath.Join(dir, name), remaining)
		if err != nil {
			return Source{}, fmt.Errorf("read fixture %s: %w", name, err)
		}
		raw = normalizeFixtureDiff(raw)
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.Write(raw)
		if !bytes.HasSuffix(raw, []byte("\n")) {
			b.WriteString("\n")
		}
		if int64(b.Len()) > maxBytes {
			return Source{}, fmt.Errorf("fixture input exceeds %d bytes", maxBytes)
		}
	}
	return Source{
		Type:         review.InputTypeFixture,
		Diff:         b.String(),
		FixtureNames: names,
		Summary:      fmt.Sprintf("Reviewed %d diff fixtures.", len(names)),
	}, nil
}

func readFixtureNames(dir string, maxFiles int) ([]string, error) {
	if maxFiles <= 0 {
		return nil, fmt.Errorf("fixture file count limit must be positive")
	}
	handle, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("read fixture dir: %w", err)
	}
	defer handle.Close()
	capacity := 128
	if maxFiles < capacity {
		capacity = maxFiles
	}
	names := make([]string, 0, capacity)
	for {
		entries, readErr := handle.ReadDir(128)
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".diff") {
				continue
			}
			if len(names) >= maxFiles {
				return nil, fmt.Errorf("fixture file count exceeded %d", maxFiles)
			}
			names = append(names, entry.Name())
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read fixture dir: %w", readErr)
		}
	}
	sort.Strings(names)
	return names, nil
}

func normalizeFixtureDiff(raw []byte) []byte {
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	raw = bytes.ReplaceAll(raw, []byte("\r"), []byte("\n"))
	return raw
}

func readDiffFile(path string, repoPath string) (Source, error) {
	return readDiffFileWithLimit(path, repoPath, maxReviewInputBytes)
}

func readDiffFileWithLimit(path string, repoPath string, maxBytes int64) (Source, error) {
	raw, err := readFileLimited(path, maxBytes)
	if err != nil {
		return Source{}, fmt.Errorf("read diff file: %w", err)
	}
	absRepo, err := resolveRepoPath(repoPath)
	if err != nil {
		return Source{}, err
	}
	summary := fmt.Sprintf("Reviewed unified diff file %s.", filepath.Base(path))
	if absRepo != "" {
		summary = fmt.Sprintf("Reviewed unified diff file %s for repository %s.", filepath.Base(path), absRepo)
	}
	return Source{
		Type:     review.InputTypeDiffFile,
		Diff:     string(raw),
		RepoPath: absRepo,
		WorkDir:  absRepo,
		Summary:  summary,
	}, nil
}

func readRepoDiff(ctx context.Context, repoPath string) (Source, error) {
	return readRepoDiffWithLimit(ctx, repoPath, maxReviewInputBytes)
}

func readRepoDiffWithLimit(ctx context.Context, repoPath string, maxBytes int64) (Source, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return Source{}, fmt.Errorf("resolve repo path: %w", err)
	}
	gitOptions, err := gitReadOnlyOptions(ctx, abs)
	if err != nil {
		return Source{}, fmt.Errorf("inspect git helper configuration: %w", err)
	}
	baseRef, err := repoDiffBase(ctx, abs, gitOptions...)
	if err != nil {
		return Source{}, fmt.Errorf("resolve git diff base: %w", err)
	}
	raw, err := gitOutputLimited(ctx, abs, maxBytes, appendGitOptions(gitOptions, "diff", "--no-ext-diff", "--no-textconv", "--binary", "--no-color", baseRef)...)
	if err != nil {
		return Source{}, fmt.Errorf("read git diff: %w", err)
	}
	untracked, err := gitOutputLimited(ctx, abs, maxUntrackedListBytes, appendGitOptions(gitOptions, "ls-files", "--others", "--exclude-standard", "-z")...)
	if err != nil {
		return Source{}, fmt.Errorf("read untracked files: %w", err)
	}
	diff := string(raw)
	remaining := maxBytes
	if maxBytes >= 0 {
		remaining -= int64(len(raw))
	}
	untrackedDiff, err := untrackedFileDiffsWithLimit(abs, untracked, remaining)
	if err != nil {
		return Source{}, err
	}
	if diff != "" && untrackedDiff != "" && !strings.HasSuffix(diff, "\n") {
		diff += "\n"
	}
	diff += untrackedDiff
	return Source{
		Type:     review.InputTypeRepo,
		Diff:     diff,
		RepoPath: abs,
		WorkDir:  abs,
		Summary:  fmt.Sprintf("Reviewed git workspace diff from %s.", abs),
	}, nil
}

func repoDiffBase(ctx context.Context, repoPath string, gitOptions ...string) (string, error) {
	if _, err := gitOutput(ctx, repoPath, appendGitOptions(gitOptions, "rev-parse", "--verify", "--quiet", "HEAD")...); err != nil {
		return emptyTreeHash, nil
	}
	return "HEAD", nil
}

func gitOutput(ctx context.Context, repoPath string, args ...string) ([]byte, error) {
	return gitOutputLimited(ctx, repoPath, -1, args...)
}

func gitOutputLimited(ctx context.Context, repoPath string, maxBytes int64, args ...string) ([]byte, error) {
	cmdArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = gitCommandEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	reader := io.Reader(stdout)
	if maxBytes >= 0 {
		reader = io.LimitReader(stdout, maxBytes+1)
	}
	raw, readErr := io.ReadAll(reader)
	if maxBytes >= 0 && int64(len(raw)) > maxBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("git output exceeded %d bytes", maxBytes)
	}
	err = cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return raw, nil
}

func gitReadOnlyOptions(ctx context.Context, repoPath string) ([]string, error) {
	raw, err := gitOutputLimited(ctx, repoPath, maxGitConfigBytes, "config", "--includes", "--name-only", "--null", "--list")
	if err != nil {
		return nil, err
	}
	options := []string{
		"-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false",
		"-c", "diff.external=",
	}
	seen := make(map[string]struct{})
	for _, rawKey := range bytes.Split(raw, []byte{0}) {
		key := string(rawKey)
		driver, ok := gitFilterDriver(key)
		if !ok {
			continue
		}
		if _, ok := seen[driver]; ok {
			continue
		}
		seen[driver] = struct{}{}
		for _, setting := range []string{"clean", "process", "smudge"} {
			options = append(options, "-c", "filter."+driver+"."+setting+"=")
		}
		options = append(options, "-c", "filter."+driver+".required=false")
	}
	return options, nil
}

func gitFilterDriver(key string) (string, bool) {
	const prefix = "filter."
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	for _, setting := range []string{"clean", "process", "smudge", "required"} {
		suffix := "." + setting
		if !strings.HasSuffix(key, suffix) {
			continue
		}
		driver := strings.TrimSuffix(strings.TrimPrefix(key, prefix), suffix)
		return driver, driver != ""
	}
	return "", false
}

func appendGitOptions(options []string, args ...string) []string {
	out := make([]string, 0, len(options)+len(args))
	out = append(out, options...)
	out = append(out, args...)
	return out
}

func gitCommandEnv() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env)+5)
	for _, item := range env {
		name, _, _ := strings.Cut(item, "=")
		switch {
		case name == "GIT_CONFIG_COUNT",
			strings.HasPrefix(name, "GIT_CONFIG_KEY_"),
			strings.HasPrefix(name, "GIT_CONFIG_VALUE_"),
			name == "GIT_CONFIG_PARAMETERS",
			name == "GIT_CONFIG_GLOBAL",
			name == "GIT_CONFIG_SYSTEM",
			name == "GIT_CONFIG_NOSYSTEM",
			name == "GIT_EXTERNAL_DIFF",
			name == "GIT_DIFF_OPTS",
			name == "GIT_PAGER",
			name == "GIT_PAGER_IN_USE",
			name == "GIT_DIR",
			name == "GIT_WORK_TREE",
			name == "GIT_INDEX_FILE",
			name == "GIT_OBJECT_DIRECTORY",
			name == "GIT_ALTERNATE_OBJECT_DIRECTORIES",
			name == "GIT_COMMON_DIR",
			name == "GIT_ASKPASS",
			name == "GIT_SSH",
			name == "GIT_SSH_COMMAND",
			name == "GIT_PROXY_COMMAND":
			continue
		default:
			filtered = append(filtered, item)
		}
	}
	filtered = append(filtered,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_PAGER=cat",
		"GIT_TERMINAL_PROMPT=0",
	)
	return filtered
}

func untrackedFileDiffs(repoPath string, raw []byte) (string, error) {
	return untrackedFileDiffsWithLimit(repoPath, raw, maxUntrackedInputBytes)
}

func untrackedFileDiffsWithLimit(repoPath string, raw []byte, maxBytes int64) (string, error) {
	files := splitNUL(raw)
	if len(files) > maxUntrackedFileCount {
		return "", fmt.Errorf("untracked file count exceeded %d", maxUntrackedFileCount)
	}
	sort.Strings(files)
	var b strings.Builder
	var inputBytes int64
	for _, file := range files {
		remaining := int64(-1)
		if maxBytes >= 0 {
			remaining = minInt64(maxBytes-inputBytes, maxBytes-int64(b.Len()))
		}
		diff, readBytes, err := untrackedFileDiffWithLimit(repoPath, file, remaining)
		if err != nil {
			return "", err
		}
		inputBytes += readBytes
		if diff == "" {
			continue
		}
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			if maxBytes >= 0 && int64(b.Len()+1+len(diff)) > maxBytes {
				return "", fmt.Errorf("untracked diff exceeds %d bytes", maxBytes)
			}
			b.WriteString("\n")
		}
		if maxBytes >= 0 && int64(b.Len()+len(diff)) > maxBytes {
			return "", fmt.Errorf("untracked diff exceeds %d bytes", maxBytes)
		}
		b.WriteString(diff)
	}
	return b.String(), nil
}

func splitNUL(raw []byte) []string {
	parts := bytes.Split(raw, []byte{0})
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		file := filepath.ToSlash(string(part))
		if file != "" {
			files = append(files, file)
		}
	}
	return files
}

func untrackedFileDiff(repoPath string, file string) (string, error) {
	diff, _, err := untrackedFileDiffWithLimit(repoPath, file, maxUntrackedInputBytes)
	return diff, err
}

func untrackedFileDiffWithLimit(repoPath string, file string, maxBytes int64) (string, int64, error) {
	abs := filepath.Join(repoPath, filepath.FromSlash(file))
	info, err := os.Lstat(abs)
	if err != nil {
		return "", 0, fmt.Errorf("stat untracked file %s: %w", file, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		diff, err := untrackedSymlinkDiff(abs, file)
		if err == nil && maxBytes >= 0 && int64(len(diff)) > maxBytes {
			return "", 0, fmt.Errorf("untracked diff for %s exceeds %d bytes", file, maxBytes)
		}
		return diff, 0, err
	}
	if info.IsDir() {
		return "", 0, nil
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("unsupported untracked file type %s: %s", file, info.Mode().String())
	}
	if maxBytes >= 0 && info.Size() > maxBytes {
		return "", 0, fmt.Errorf("untracked file %s exceeds %d bytes", file, maxBytes)
	}
	in, err := os.Open(abs)
	if err != nil {
		return "", 0, fmt.Errorf("read untracked file %s: %w", file, err)
	}
	defer in.Close()
	var b strings.Builder
	write := func(text string) error {
		if maxBytes >= 0 && int64(b.Len()+len(text)) > maxBytes {
			return fmt.Errorf("untracked diff for %s exceeds %d bytes", file, maxBytes)
		}
		b.WriteString(text)
		return nil
	}
	for _, line := range []string{
		fmt.Sprintf("diff --git %s %s\n", gitQuotePath("a/"+file), gitQuotePath("b/"+file)),
		"new file mode 100644\n",
		"--- /dev/null\n",
		fmt.Sprintf("+++ %s\n", gitQuotePath("b/"+file)),
	} {
		if err := write(line); err != nil {
			return "", 0, err
		}
	}

	reader := newUntrackedReader(in, maxBytes)
	lineCount, noNewline, binary, err := scanUntrackedFile(reader, maxBytes)
	if err != nil {
		return "", 0, fmt.Errorf("read untracked file %s: %w", file, err)
	}
	readBytes := info.Size()
	if binary {
		if err := write(fmt.Sprintf("Binary files /dev/null and %s differ\n", gitQuotePath("b/"+file))); err != nil {
			return "", 0, err
		}
		return b.String(), readBytes, nil
	}
	if lineCount == 0 {
		return b.String(), readBytes, nil
	}
	if err := write(fmt.Sprintf("@@ -0,0 +1,%d @@\n", lineCount)); err != nil {
		return "", 0, err
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return "", 0, fmt.Errorf("rewind untracked file %s: %w", file, err)
	}
	reader = newUntrackedReader(in, maxBytes)
	lineStart := true
	for {
		part, readErr := reader.ReadSlice('\n')
		if len(part) > 0 {
			if lineStart {
				if err := write("+"); err != nil {
					return "", 0, err
				}
				lineStart = false
			}
			if err := write(string(part)); err != nil {
				return "", 0, err
			}
			if part[len(part)-1] == '\n' {
				lineStart = true
			}
		}
		if readErr == nil || errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		return "", 0, fmt.Errorf("read untracked file %s: %w", file, readErr)
	}
	if noNewline {
		if err := write("\n\\ No newline at end of file\n"); err != nil {
			return "", 0, err
		}
	}
	return b.String(), readBytes, nil
}

func newUntrackedReader(in *os.File, maxBytes int64) *bufio.Reader {
	if maxBytes >= 0 {
		return bufio.NewReader(io.LimitReader(in, maxBytes+1))
	}
	return bufio.NewReader(in)
}

func scanUntrackedFile(reader *bufio.Reader, maxBytes int64) (int, bool, bool, error) {
	lineCount := 0
	noNewline := false
	var readBytes int64
	for {
		part, err := reader.ReadSlice('\n')
		if len(part) > 0 {
			readBytes += int64(len(part))
			if maxBytes >= 0 && readBytes > maxBytes {
				return 0, false, false, fmt.Errorf("untracked file exceeds %d bytes", maxBytes)
			}
			if bytes.Contains(part, []byte{0}) {
				return 0, false, true, nil
			}
			if part[len(part)-1] == '\n' {
				lineCount++
				noNewline = false
			} else if errors.Is(err, io.EOF) {
				lineCount++
				noNewline = true
			}
		}
		if err == nil || errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		return 0, false, false, err
	}
	if readBytes == 1 && lineCount == 1 && !noNewline {
		lineCount = 0
	}
	return lineCount, noNewline, false, nil
}

func untrackedSymlinkDiff(abs string, file string) (string, error) {
	target, err := os.Readlink(abs)
	if err != nil {
		return "", fmt.Errorf("read untracked symlink %s: %w", file, err)
	}
	target = filepath.ToSlash(target)
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git %s %s\n", gitQuotePath("a/"+file), gitQuotePath("b/"+file))
	fmt.Fprintf(&b, "new file mode 120000\n")
	fmt.Fprintf(&b, "--- /dev/null\n")
	fmt.Fprintf(&b, "+++ %s\n", gitQuotePath("b/"+file))
	fmt.Fprintf(&b, "@@ -0,0 +1 @@\n")
	fmt.Fprintf(&b, "+%s\n", target)
	return b.String(), nil
}

func readFileList(path string, repoPath string) (Source, error) {
	return readFileListWithLimit(path, repoPath, maxUntrackedListBytes)
}

func readFileListWithLimit(path string, repoPath string, maxBytes int64) (Source, error) {
	return readFileListWithLimits(path, repoPath, maxBytes, maxInputFileCount)
}

func readFileListWithLimits(path string, repoPath string, maxBytes int64, maxFiles int) (Source, error) {
	raw, err := readFileLimited(path, maxBytes)
	if err != nil {
		return Source{}, fmt.Errorf("read file list: %w", err)
	}
	if maxFiles <= 0 {
		return Source{}, fmt.Errorf("file-list entry count limit must be positive")
	}
	var files []string
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), len(raw)+1)
	for scanner.Scan() {
		file := strings.TrimSuffix(scanner.Text(), "\r")
		if file == "" || strings.HasPrefix(file, "#") {
			continue
		}
		if len(files) >= maxFiles {
			return Source{}, fmt.Errorf("file-list entry count exceeded %d", maxFiles)
		}
		files = append(files, filepath.ToSlash(file))
	}
	if err := scanner.Err(); err != nil {
		return Source{}, fmt.Errorf("scan file list: %w", err)
	}
	sort.Strings(files)
	absRepo, err := resolveRepoPath(repoPath)
	if err != nil {
		return Source{}, err
	}
	summary := fmt.Sprintf("Loaded %d changed file paths from %s for planning and sandbox context; content-based deterministic rules require diff input.", len(files), filepath.Base(path))
	if absRepo != "" {
		summary = fmt.Sprintf("Loaded %d changed file paths from %s for repository %s for planning and sandbox context; content-based deterministic rules require diff input.", len(files), filepath.Base(path), absRepo)
	}
	return Source{
		Type:     review.InputTypeFileList,
		FileList: files,
		RepoPath: absRepo,
		WorkDir:  absRepo,
		Summary:  summary,
	}, nil
}

func minInt64(a int64, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func readFileLimited(path string, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 {
		return nil, fmt.Errorf("invalid file size limit %d", maxBytes)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("input %s must be a regular file; symlinks are not supported", path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("input %s must be a regular file, got %s", path, info.Mode().String())
	}
	in, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	raw, err := io.ReadAll(io.LimitReader(in, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maxBytes)
	}
	return raw, nil
}

func resolveRepoPath(repoPath string) (string, error) {
	if strings.TrimSpace(repoPath) == "" {
		return "", nil
	}
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return "", fmt.Errorf("resolve repo path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat repo path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repo path %s is not a directory", abs)
	}
	return abs, nil
}

func gitQuotePath(path string) string {
	needsQuote := false
	for index := 0; index < len(path); index++ {
		value := path[index]
		if value <= ' ' || value == 0x7f || value >= 0x80 || value == '"' || value == '\\' {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return path
	}
	var quoted strings.Builder
	quoted.WriteByte('"')
	for index := 0; index < len(path); index++ {
		value := path[index]
		switch value {
		case '"', '\\':
			quoted.WriteByte('\\')
			quoted.WriteByte(value)
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
		default:
			if value < 0x20 || value >= 0x7f {
				fmt.Fprintf(&quoted, `\%03o`, value)
			} else {
				quoted.WriteByte(value)
			}
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}
