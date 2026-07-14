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
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const emptyTreeHash = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

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
	if strings.TrimSpace(opts.DiffFile) != "" && (strings.TrimSpace(opts.RepoPath) != "" || strings.TrimSpace(opts.FileList) != "") {
		return Source{}, fmt.Errorf("choose only one input source: %s", strings.Join(selected, ", "))
	}
	switch {
	case strings.TrimSpace(opts.DiffFile) != "":
		return readDiffFile(opts.DiffFile)
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
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Source{}, fmt.Errorf("read fixture dir: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".diff") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return Source{}, fmt.Errorf("read fixture %s: %w", name, err)
		}
		raw = normalizeFixtureDiff(raw)
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.Write(raw)
		if !strings.HasSuffix(string(raw), "\n") {
			b.WriteString("\n")
		}
	}
	return Source{
		Type:         review.InputTypeFixture,
		Diff:         b.String(),
		FixtureNames: names,
		Summary:      fmt.Sprintf("Reviewed %d diff fixtures.", len(names)),
	}, nil
}

func normalizeFixtureDiff(raw []byte) []byte {
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	raw = bytes.ReplaceAll(raw, []byte("\r"), []byte("\n"))
	return raw
}

func readDiffFile(path string) (Source, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Source{}, fmt.Errorf("read diff file: %w", err)
	}
	return Source{
		Type:    review.InputTypeDiffFile,
		Diff:    string(raw),
		Summary: fmt.Sprintf("Reviewed unified diff file %s.", filepath.Base(path)),
	}, nil
}

func readRepoDiff(ctx context.Context, repoPath string) (Source, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return Source{}, fmt.Errorf("resolve repo path: %w", err)
	}
	baseRef, err := repoDiffBase(ctx, abs)
	if err != nil {
		return Source{}, fmt.Errorf("resolve git diff base: %w", err)
	}
	raw, err := gitOutput(ctx, abs, "diff", "--no-ext-diff", "--binary", "--no-color", baseRef)
	if err != nil {
		return Source{}, fmt.Errorf("read git diff: %w", err)
	}
	untracked, err := gitOutput(ctx, abs, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return Source{}, fmt.Errorf("read untracked files: %w", err)
	}
	diff := string(raw)
	untrackedDiff, err := untrackedFileDiffs(abs, untracked)
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

func repoDiffBase(ctx context.Context, repoPath string) (string, error) {
	if _, err := gitOutput(ctx, repoPath, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		return emptyTreeHash, nil
	}
	return "HEAD", nil
}

func gitOutput(ctx context.Context, repoPath string, args ...string) ([]byte, error) {
	cmdArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return raw, nil
}

func untrackedFileDiffs(repoPath string, raw []byte) (string, error) {
	files := splitNUL(raw)
	sort.Strings(files)
	var b strings.Builder
	for _, file := range files {
		diff, err := untrackedFileDiff(repoPath, file)
		if err != nil {
			return "", err
		}
		if diff == "" {
			continue
		}
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
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
	abs := filepath.Join(repoPath, filepath.FromSlash(file))
	info, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("stat untracked file %s: %w", file, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return untrackedSymlinkDiff(abs, file)
	}
	if info.IsDir() {
		return "", nil
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("read untracked file %s: %w", file, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git %s %s\n", gitQuotePath("a/"+file), gitQuotePath("b/"+file))
	fmt.Fprintf(&b, "new file mode 100644\n")
	fmt.Fprintf(&b, "--- /dev/null\n")
	fmt.Fprintf(&b, "+++ %s\n", gitQuotePath("b/"+file))
	if bytes.Contains(raw, []byte{0}) {
		fmt.Fprintf(&b, "Binary files /dev/null and %s differ\n", gitQuotePath("b/"+file))
		return b.String(), nil
	}
	lines, noNewline := diffLines(string(raw))
	if len(lines) == 0 {
		return b.String(), nil
	}
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, line := range lines {
		fmt.Fprintf(&b, "+%s\n", line)
	}
	if noNewline {
		b.WriteString(`\ No newline at end of file`)
		b.WriteString("\n")
	}
	return b.String(), nil
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

func diffLines(text string) ([]string, bool) {
	if text == "" {
		return nil, false
	}
	noNewline := !strings.HasSuffix(text, "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil, noNewline
	}
	return strings.Split(text, "\n"), noNewline
}

func readFileList(path string, repoPath string) (Source, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Source{}, fmt.Errorf("read file list: %w", err)
	}
	var files []string
	for _, line := range strings.Split(string(raw), "\n") {
		file := strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(file)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		files = append(files, filepath.ToSlash(file))
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
