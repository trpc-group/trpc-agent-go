//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inputsource loads review inputs from one of four sources: a
// directory of .diff fixtures, a single .diff file, a text file listing
// source files (synthesized into a synthetic "new file" diff), or a live
// git repository (committed + working-tree changes vs master).
//
// Directory traversal is symlink-safe: WalkDir combined with Lstat skips
// symlinks, devices, sockets and named pipes so a hostile fixture tree
// cannot redirect review input at arbitrary host files. pathUnder is
// provided for the sandbox layer to validate that host paths supplied as
// inputs stay under an allowed parent directory.
package inputsource

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
)

// Source identifies the input mode requested by the caller of Load.
type Source string

const (
	// SourceFixtureDir treats the path as a directory of .diff files.
	SourceFixtureDir Source = "fixture-dir"
	// SourceDiffFile treats the path as a single .diff file.
	SourceDiffFile Source = "diff-file"
	// SourceFileList treats the path as a text file listing source files
	// to be synthesized into a synthetic "new file" diff.
	SourceFileList Source = "file-list"
	// SourceRepoPath treats the path as a git repository whose committed
	// and working-tree changes vs master should be reviewed.
	SourceRepoPath Source = "repo-path"
)

// Input is the result of Load: the raw diff text, the parsed files, and
// (for repo-path mode) the repository path the diff was extracted from.
type Input struct {
	Source   Source
	DiffText string
	Files    []diffparse.DiffFile
	RepoPath string
}

// Load reads inputs based on the mode and returns parsed diff files. At
// least one path must be supplied; the first path is interpreted per the
// chosen Source.
func Load(ctx context.Context, source Source, paths ...string) (*Input, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("inputsource: at least one path required for source %q", source)
	}
	switch source {
	case SourceFixtureDir:
		return loadFixtureDir(ctx, paths[0])
	case SourceDiffFile:
		return loadDiffFile(paths[0])
	case SourceFileList:
		return loadFileList(paths[0])
	case SourceRepoPath:
		return loadRepoPath(ctx, paths[0])
	default:
		return nil, fmt.Errorf("inputsource: unknown source %q", source)
	}
}

// loadFixtureDir walks the directory with WalkDir, reads each .diff file
// (skipping symlinks and other non-regular files via shouldUploadFile),
// concatenates them with "\n" separators and parses the result.
func loadFixtureDir(ctx context.Context, dir string) (*Input, error) {
	var parts []string
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, lerr := os.Lstat(path)
		if lerr != nil {
			return lerr
		}
		if !shouldUploadFile(info) {
			return nil
		}
		if !strings.HasSuffix(path, ".diff") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		parts = append(parts, string(data))
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("inputsource: walk fixture dir %q: %w", dir, walkErr)
	}
	diffText := strings.Join(parts, "\n")
	parsed, err := diffparse.Parse(strings.NewReader(diffText))
	if err != nil {
		return nil, fmt.Errorf("inputsource: parse fixture diff: %w", err)
	}
	return &Input{Source: SourceFixtureDir, DiffText: diffText, Files: parsed.Files}, nil
}

// loadDiffFile reads a single .diff file and parses it.
func loadDiffFile(path string) (*Input, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("inputsource: read diff file %q: %w", path, err)
	}
	diffText := string(data)
	parsed, err := diffparse.Parse(strings.NewReader(diffText))
	if err != nil {
		return nil, fmt.Errorf("inputsource: parse diff file %q: %w", path, err)
	}
	return &Input{Source: SourceDiffFile, DiffText: diffText, Files: parsed.Files}, nil
}

// loadFileList reads a text file listing source paths (one per line),
// synthesizes a "new file" diff for each, concatenates and parses them.
func loadFileList(listPath string) (*Input, error) {
	data, err := os.ReadFile(listPath)
	if err != nil {
		return nil, fmt.Errorf("inputsource: read file list %q: %w", listPath, err)
	}
	var diffs []string
	for _, line := range strings.Split(string(data), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		synth, serr := syntheticDiffForFile(name)
		if serr != nil {
			return nil, serr
		}
		diffs = append(diffs, synth)
	}
	diffText := strings.Join(diffs, "\n")
	parsed, err := diffparse.Parse(strings.NewReader(diffText))
	if err != nil {
		return nil, fmt.Errorf("inputsource: parse file-list diff: %w", err)
	}
	return &Input{Source: SourceFileList, DiffText: diffText, Files: parsed.Files}, nil
}

// syntheticDiffForFile reads a source file and builds a synthetic "new
// file" diff treating its full contents as added lines.
func syntheticDiffForFile(name string) (string, error) {
	content, err := os.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("inputsource: read listed file %q: %w", name, err)
	}
	text := string(content)
	var lines []string
	if text != "" {
		lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", name, name)
	b.WriteString("new file mode 100644\n")
	b.WriteString("--- /dev/null\n")
	fmt.Fprintf(&b, "+++ b/%s\n", name)
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, l := range lines {
		b.WriteString("+")
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// loadRepoPath extracts the committed-vs-master diff and the working-tree
// diff from the repository, concatenates them and parses the result. An
// empty repo (unborn HEAD, no changes) yields an empty diff with no files.
func loadRepoPath(ctx context.Context, repo string) (*Input, error) {
	committed, err := gitCommittedDiff(ctx, repo)
	if err != nil {
		return nil, err
	}
	working, err := gitWorkingTreeDiff(ctx, repo)
	if err != nil {
		return nil, err
	}
	diffText := committed
	if diffText != "" && working != "" {
		diffText += "\n"
	}
	diffText += working
	parsed, err := diffparse.Parse(strings.NewReader(diffText))
	if err != nil {
		return nil, fmt.Errorf("inputsource: parse repo diff: %w", err)
	}
	return &Input{Source: SourceRepoPath, DiffText: diffText, Files: parsed.Files, RepoPath: repo}, nil
}

// gitCommittedDiff returns the diff of HEAD against its merge-base with
// master. If the repo has an unborn HEAD or no master branch, it falls
// back to "git diff --root"; if that also fails the repo is treated as
// empty and an empty string is returned.
func gitCommittedDiff(ctx context.Context, repo string) (string, error) {
	base, err := gitOutput(ctx, repo, "merge-base", "HEAD", "master")
	if err == nil {
		base = strings.TrimSpace(base)
		if base != "" {
			diff, derr := gitOutput(ctx, repo, "diff", base+"...HEAD")
			if derr != nil {
				return "", fmt.Errorf("inputsource: git diff committed: %w", derr)
			}
			return diff, nil
		}
	}
	// Unborn HEAD or no master branch: try --root, else treat as empty.
	if diff, derr := gitOutput(ctx, repo, "diff", "--root"); derr == nil {
		return diff, nil
	}
	return "", nil
}

// gitWorkingTreeDiff returns the unstaged working-tree diff.
func gitWorkingTreeDiff(ctx context.Context, repo string) (string, error) {
	diff, err := gitOutput(ctx, repo, "diff")
	if err != nil {
		return "", fmt.Errorf("inputsource: git diff working tree: %w", err)
	}
	return diff, nil
}

// gitOutput runs "git -C repo args..." capturing stdout. stderr is
// discarded so unborn-HEAD fallbacks do not pollute test output.
func gitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	full := make([]string, 0, len(args)+2)
	full = append(full, "-C", repo)
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// shouldUploadFile reports whether the entry is a regular file safe to
// read and upload. Symlinks, devices, sockets and named pipes are rejected
// so a fixture tree cannot redirect input at arbitrary host files.
func shouldUploadFile(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 ||
		mode&os.ModeDevice != 0 ||
		mode&os.ModeSocket != 0 ||
		mode&os.ModeNamedPipe != 0 {
		return false
	}
	return mode.IsRegular()
}

// pathUnder ensures child is under parent, preventing directory traversal.
// Both paths are cleaned with filepath.Clean; child is accepted only when
// it equals parent or has parent as a path prefix (with a separator). The
// cleaned child path is returned on success.
func pathUnder(parent, child string) (string, error) {
	pc := filepath.Clean(parent)
	cc := filepath.Clean(child)
	if cc == pc {
		return cc, nil
	}
	prefix := pc + string(filepath.Separator)
	if !strings.HasPrefix(cc, prefix) {
		return "", fmt.Errorf("inputsource: path %q is not under %q", child, parent)
	}
	return cc, nil
}
