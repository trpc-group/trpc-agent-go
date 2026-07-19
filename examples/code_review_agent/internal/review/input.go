//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const maxInputBytes = 8 << 20

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func loadInput(ctx context.Context, cfg Config, baseDir string) (ParsedInput, string, error) {
	cfg.DiffFile = strings.TrimSpace(cfg.DiffFile)
	cfg.RepoPath = strings.TrimSpace(cfg.RepoPath)
	cfg.FileList = strings.TrimSpace(cfg.FileList)
	cfg.Fixture = strings.TrimSpace(cfg.Fixture)
	modes := 0
	primaryRepo := cfg.RepoPath
	if cfg.FileList != "" {
		primaryRepo = ""
	}
	for _, value := range []string{cfg.DiffFile, primaryRepo, cfg.FileList, cfg.Fixture} {
		if value != "" {
			modes++
		}
	}
	if modes != 1 {
		return ParsedInput{}, "", errors.New("choose exactly one of --diff-file, --repo-path, --file-list, or --fixture")
	}
	var raw, mode string
	var err error
	switch {
	case cfg.DiffFile != "":
		raw, err = readBounded(cfg.DiffFile)
		mode = "diff_file"
	case cfg.Fixture != "":
		name := filepath.Base(cfg.Fixture)
		if name != cfg.Fixture || strings.Contains(name, "..") {
			return ParsedInput{}, "", errors.New("fixture name must not contain a path")
		}
		raw, err = readBounded(filepath.Join(baseDir, "fixtures", name+".diff"))
		mode = "fixture:" + name
	case cfg.FileList != "":
		raw, err = diffFromFileList(cfg.RepoPath, cfg.FileList)
		mode = "file_list"
	default:
		raw, err = gitWorkingDiff(ctx, cfg.RepoPath)
		mode = "repo_path"
	}
	if err != nil {
		return ParsedInput{}, "", err
	}
	parsed, err := ParseUnifiedDiff(raw)
	return parsed, mode, err
}

func readBounded(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return readBoundedReader(file, "input")
}

func readBoundedReader(reader io.Reader, label string) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxInputBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxInputBytes {
		return "", fmt.Errorf("%s exceeds %d bytes", label, maxInputBytes)
	}
	return string(data), nil
}

func commandOutputBounded(cmd *exec.Cmd, label string) (string, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	output, readErr := readBoundedReader(stdout, label)
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", readErr
	}
	if err := cmd.Wait(); err != nil {
		return "", err
	}
	return output, nil
}

func gitWorkingDiff(ctx context.Context, repo string) (string, error) {
	abs, err := filepath.Abs(repo)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", "-c", "core.quotepath=false", "diff", "--no-ext-diff", "--unified=3", "HEAD", "--")
	cmd.Dir = abs
	out, err := commandOutputBounded(cmd, "git diff")
	if err != nil {
		return "", fmt.Errorf("read git diff: %w", err)
	}
	untracked := exec.CommandContext(ctx, "git", "-c", "core.quotepath=false", "ls-files", "--others", "--exclude-standard")
	untracked.Dir = abs
	listed, err := commandOutputBounded(untracked, "untracked file list")
	if err != nil {
		return "", fmt.Errorf("list untracked files: %w", err)
	}
	paths := nonEmptyLines(listed)
	extra, err := synthesizeFiles(abs, paths)
	if err != nil {
		return "", err
	}
	if len(out)+len(extra) > maxInputBytes {
		return "", fmt.Errorf("git input exceeds %d bytes", maxInputBytes)
	}
	return out + extra, nil
}

func diffFromFileList(repo, listPath string) (string, error) {
	if strings.TrimSpace(repo) == "" {
		return "", errors.New("--file-list requires --repo-path as its base")
	}
	root, err := filepath.Abs(repo)
	if err != nil {
		return "", err
	}
	data, err := readBounded(listPath)
	if err != nil {
		return "", err
	}
	var paths []string
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		paths = append(paths, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return synthesizeFiles(root, paths)
}

func synthesizeFiles(root string, paths []string) (string, error) {
	var b strings.Builder
	for _, value := range paths {
		rel := filepath.Clean(strings.TrimSpace(value))
		if rel == "." || rel == "" {
			continue
		}
		if filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("unsafe file-list entry %q", rel)
		}
		full := filepath.Join(root, rel)
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil {
			return "", err
		}
		if !withinRoot(root, resolved) {
			return "", fmt.Errorf("file-list entry escapes repository: %q", rel)
		}
		contents, err := readBounded(resolved)
		if err != nil {
			return "", err
		}
		slash := filepath.ToSlash(rel)
		lines := fileLines(contents)
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", slash, slash, slash, len(lines))
		for _, line := range lines {
			b.WriteString("+")
			b.WriteString(line)
			b.WriteString("\n")
		}
		if b.Len() > maxInputBytes {
			return "", fmt.Errorf("file-list input exceeds %d bytes", maxInputBytes)
		}
	}
	return b.String(), nil
}

func fileLines(contents string) []string {
	if contents == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(contents, "\n"), "\n")
}

func nonEmptyLines(value string) []string {
	var result []string
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}

func withinRoot(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ParseUnifiedDiff parses a bounded unified diff into changed files and added lines.
func ParseUnifiedDiff(raw string) (ParsedInput, error) {
	if len(raw) > maxInputBytes {
		return ParsedInput{}, fmt.Errorf("input exceeds %d bytes", maxInputBytes)
	}
	parsed := ParsedInput{Raw: raw, Context: map[string]string{}, Statuses: map[string]FileStatus{}}
	files := map[string]bool{}
	currentFile := ""
	oldFile := ""
	headerFile := ""
	newLine := 0
	inHunk := false
	for _, line := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			oldFile, currentFile = "", ""
			headerFile = diffHeaderTarget(line)
			inHunk = false
		case strings.HasPrefix(line, "rename to "):
			currentFile = cleanDiffPath(strings.TrimPrefix(line, "rename to "))
			if currentFile != "" {
				files[currentFile] = true
				parsed.Statuses[currentFile] = fileModified
			}
		case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
			if headerFile != "" {
				files[headerFile] = true
				parsed.Statuses[headerFile] = fileModified
			}
		case strings.HasPrefix(line, "--- "):
			oldFile = cleanDiffPath(strings.TrimPrefix(line, "--- "))
			inHunk = false
		case strings.HasPrefix(line, "+++ "):
			newFile := cleanDiffPath(strings.TrimPrefix(line, "+++ "))
			currentFile = newFile
			status := fileModified
			if oldFile == "" {
				status = fileAdded
			}
			if newFile == "" {
				currentFile, status = oldFile, fileDeleted
			}
			if currentFile != "" {
				files[currentFile] = true
				parsed.Statuses[currentFile] = status
			}
			inHunk = false
		case hunkHeader.MatchString(line):
			parts := hunkHeader.FindStringSubmatch(line)
			newLine, _ = strconv.Atoi(parts[1])
			inHunk = true
		case inHunk && strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			parsed.Lines = append(parsed.Lines, ChangedLine{File: currentFile, Line: newLine, Text: strings.TrimPrefix(line, "+"), Package: packageFor(currentFile)})
			parsed.Context[currentFile] += strings.TrimPrefix(line, "+") + "\n"
			parsed.Summary.AddedLines++
			newLine++
		case inHunk && strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			parsed.Summary.DeletedLines++
		case inHunk && strings.HasPrefix(line, " "):
			parsed.Context[currentFile] += strings.TrimPrefix(line, " ") + "\n"
			newLine++
		}
	}
	for file := range files {
		parsed.Files = append(parsed.Files, file)
		if strings.HasSuffix(file, ".go") {
			parsed.Summary.GoFiles++
		}
	}
	sort.Strings(parsed.Files)
	parsed.Summary.FilesChanged = len(parsed.Files)
	sum := sha256.Sum256([]byte(raw))
	parsed.Summary.Digest = hex.EncodeToString(sum[:])
	if raw != "" && len(parsed.Files) == 0 {
		return ParsedInput{}, errors.New("input is not a supported unified diff")
	}
	return parsed, nil
}

func diffHeaderTarget(line string) string {
	value := strings.TrimSpace(strings.TrimPrefix(line, "diff --git "))
	for token := 0; token < 2; token++ {
		var current string
		if strings.HasPrefix(value, `"`) {
			end := 1
			for end < len(value) {
				if value[end] == '"' && value[end-1] != '\\' {
					end++
					break
				}
				end++
			}
			if end > len(value) {
				return ""
			}
			current = value[:end]
			value = strings.TrimSpace(value[end:])
		} else {
			end := strings.IndexByte(value, ' ')
			if end < 0 {
				current, value = value, ""
			} else {
				current, value = value[:end], strings.TrimSpace(value[end+1:])
			}
		}
		if token == 1 {
			return cleanDiffPath(current)
		}
	}
	return ""
}

func cleanDiffPath(value string) string {
	value = strings.TrimSpace(value)
	if tab := strings.IndexByte(value, '\t'); tab >= 0 {
		value = value[:tab]
	}
	if strings.HasPrefix(value, `"`) {
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
	}
	if value == "/dev/null" {
		return ""
	}
	value = strings.TrimPrefix(strings.TrimPrefix(value, "a/"), "b/")
	value = filepath.ToSlash(filepath.Clean(value))
	if value == "." || value == ".." || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "/") {
		return ""
	}
	return value
}

func packageFor(file string) string {
	dir := filepath.ToSlash(filepath.Dir(file))
	if dir == "." {
		return "root"
	}
	return dir
}
