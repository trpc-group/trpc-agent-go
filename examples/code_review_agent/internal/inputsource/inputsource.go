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
// git repository (committed + working-tree changes vs the default branch).
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

// MaxDiffBytes is the maximum total size of diff input accepted by Load.
// Inputs exceeding this are rejected before parsing to prevent unbounded
// memory allocation. 10 MiB is generous for typical PRs while still
// bounding memory usage.
const MaxDiffBytes int64 = 10 * 1024 * 1024

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
	// and working-tree changes vs the default branch should be reviewed.
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
// chosen Source. For SourceFileList, a second path (the repo root) is
// required — file-list entries are resolved under it and validated against
// directory traversal, symlinks and non-regular files.
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
		if len(paths) < 2 || paths[1] == "" {
			return nil, fmt.Errorf("inputsource: file-list mode requires a repo root")
		}
		return loadFileList(paths[0], paths[1])
	case SourceRepoPath:
		return loadRepoPath(ctx, paths[0])
	default:
		return nil, fmt.Errorf("inputsource: unknown source %q", source)
	}
}

// loadFixtureDir walks the directory with WalkDir, reads each .diff file
// (skipping symlinks and other non-regular files via shouldUploadFile),
// concatenates them with "\n" separators and parses the result. The total
// size of all fixtures is capped at MaxDiffBytes to prevent unbounded
// memory allocation.
func loadFixtureDir(ctx context.Context, dir string) (*Input, error) {
	var parts []string
	var totalSize int64
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
		f, oerr := os.Open(path)
		if oerr != nil {
			return oerr
		}
		defer f.Close()
		remaining := MaxDiffBytes - totalSize
		if remaining <= 0 {
			return fmt.Errorf("inputsource: fixture dir exceeds max total size %d bytes", MaxDiffBytes)
		}
		data, rerr := io.ReadAll(io.LimitReader(f, remaining+1))
		if rerr != nil {
			return rerr
		}
		if int64(len(data)) > remaining {
			return fmt.Errorf("inputsource: fixture dir exceeds max total size %d bytes", MaxDiffBytes)
		}
		totalSize += int64(len(data))
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

// loadDiffFile reads a single .diff file and parses it. The file size is
// capped at MaxDiffBytes to prevent unbounded memory allocation.
func loadDiffFile(path string) (*Input, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("inputsource: read diff file %q: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxDiffBytes+1))
	if err != nil {
		return nil, fmt.Errorf("inputsource: read diff file %q: %w", path, err)
	}
	if int64(len(data)) > MaxDiffBytes {
		return nil, fmt.Errorf("inputsource: diff file %q exceeds max size %d bytes", path, MaxDiffBytes)
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
// Each entry is resolved under repoRoot and validated against directory
// traversal, symlinks and non-regular files to prevent reading files
// outside the reviewed repository.
func loadFileList(listPath, repoRoot string) (*Input, error) {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("inputsource: resolve repo root %q: %w", repoRoot, err)
	}
	f, err := os.Open(listPath)
	if err != nil {
		return nil, fmt.Errorf("inputsource: read file list %q: %w", listPath, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxDiffBytes+1))
	if err != nil {
		return nil, fmt.Errorf("inputsource: read file list %q: %w", listPath, err)
	}
	if int64(len(data)) > MaxDiffBytes {
		return nil, fmt.Errorf("inputsource: file list %q exceeds max size %d bytes", listPath, MaxDiffBytes)
	}
	var diffs []string
	var totalSize int64
	for _, line := range strings.Split(string(data), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		synth, serr := syntheticDiffForFile(name, absRoot, MaxDiffBytes-totalSize)
		if serr != nil {
			return nil, serr
		}
		totalSize += int64(len(synth))
		if totalSize > MaxDiffBytes {
			return nil, fmt.Errorf("inputsource: file-list synthetic diff exceeds max total size %d bytes", MaxDiffBytes)
		}
		diffs = append(diffs, synth)
	}
	diffText := strings.Join(diffs, "\n")
	parsed, err := diffparse.Parse(strings.NewReader(diffText))
	if err != nil {
		return nil, fmt.Errorf("inputsource: parse file-list diff: %w", err)
	}
	return &Input{Source: SourceFileList, DiffText: diffText, Files: parsed.Files, RepoPath: absRoot}, nil
}

// syntheticDiffForFile reads a source file and builds a synthetic "new
// file" diff treating its full contents as added lines. The path is
// resolved under repoRoot and validated: absolute paths, traversal
// (../), symlinks and non-regular files are rejected before reading.
// maxBytes caps the read size to prevent unbounded memory allocation.
func syntheticDiffForFile(name, repoRoot string, maxBytes int64) (string, error) {
	// Reject absolute paths — file-list entries must be repo-relative.
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("inputsource: reject absolute path %q in file-list", name)
	}
	// Resolve the entry under repoRoot and verify it stays under root.
	full := filepath.Join(repoRoot, name)
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("inputsource: resolve %q: %w", name, err)
	}
	if _, err := pathUnder(repoRoot, abs); err != nil {
		return "", fmt.Errorf("inputsource: reject path traversal %q (resolves outside repo root)", name)
	}
	// Lstat to reject symlinks, devices, sockets, etc.
	info, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("inputsource: stat listed file %q: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("inputsource: reject symlink %q in file-list", name)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("inputsource: reject non-regular file %q in file-list", name)
	}
	if maxBytes <= 0 {
		return "", fmt.Errorf("inputsource: file-list size budget exhausted")
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", fmt.Errorf("inputsource: read listed file %q: %w", name, err)
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("inputsource: read listed file %q: %w", name, err)
	}
	if int64(len(content)) > maxBytes {
		return "", fmt.Errorf("inputsource: file %q exceeds remaining size budget %d bytes", name, maxBytes)
	}
	text := string(content)
	var lines []string
	if text != "" {
		lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	// Use the repo-relative name in the diff header so it matches the
	// reviewed repository's layout.
	rel := filepath.ToSlash(name)
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", rel, rel)
	b.WriteString("new file mode 100644\n")
	b.WriteString("--- /dev/null\n")
	fmt.Fprintf(&b, "+++ b/%s\n", rel)
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, l := range lines {
		b.WriteString("+")
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// loadRepoPath extracts the committed-vs-default-branch diff and the
// working-tree diff from the repository, concatenates them and parses the
// result. An empty repo (unborn HEAD, no changes) yields an empty diff
// with no files.
func loadRepoPath(ctx context.Context, repo string) (*Input, error) {
	baseBranch, err := gitDefaultBranch(ctx, repo)
	if err != nil {
		return nil, err
	}
	committed, err := gitCommittedDiff(ctx, repo, baseBranch)
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

// gitDefaultBranch resolves the repository's default branch name via
// `git symbolic-ref refs/remotes/origin/HEAD`. This returns a remote-tracking
// ref name (e.g. "origin/main") that merge-base and diff can resolve directly,
// even when no local branch exists (common in CI checkouts). For repos without
// remotes it falls back to verifying "main"/"master" as local or remote refs.
// For repos with an unborn HEAD (no commits), it returns "" so the caller
// treats the repo as having no committed diff.
func gitDefaultBranch(ctx context.Context, repo string) (string, error) {
	out, err := gitOutput(ctx, repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		name := strings.TrimSpace(out)
		// symbolic-ref --short returns e.g. "origin/main" — keep the prefix
		// so merge-base/diff resolve against the remote-tracking ref, which
		// exists even when the local branch is absent.
		if name != "" {
			return name, nil
		}
	}
	// No remote HEAD: try common default branch names (local + remote-tracking).
	for _, candidate := range []string{"main", "master", "origin/main", "origin/master"} {
		if _, err := gitOutput(ctx, repo, "rev-parse", "--verify", candidate); err == nil {
			return candidate, nil
		}
	}
	// Unborn HEAD or no branches: return empty so gitCommittedDiff returns "".
	return "", nil
}

// gitCommittedDiff returns the diff of HEAD against its merge-base with
// the given base branch. If baseBranch is empty (unborn HEAD, no commits)
// or the merge-base fails (unrelated histories), it returns an empty string
// so the caller only sees the working-tree diff — never a duplicate of it.
func gitCommittedDiff(ctx context.Context, repo, baseBranch string) (string, error) {
	if baseBranch == "" {
		return "", nil
	}
	base, err := gitOutput(ctx, repo, "merge-base", "HEAD", baseBranch)
	if err != nil {
		// No common ancestor (unrelated histories): no committed diff.
		return "", nil
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return "", nil
	}
	diff, err := gitOutput(ctx, repo, "diff", base+"...HEAD")
	if err != nil {
		return "", fmt.Errorf("inputsource: git diff committed: %w", err)
	}
	return diff, nil
}

// gitWorkingTreeDiff returns the working-tree diff against HEAD, capturing
// both staged and unstaged changes so staged-but-uncommitted security or
// correctness changes are reviewed rather than silently skipped. For repos
// with an unborn HEAD (no commits), it falls back to `git diff` (unstaged
// only) since there are no staged changes to miss in a fresh repo.
//
// Untracked files (listed by `git ls-files --others --exclude-standard`) are
// synthesised into "new file" diffs and appended so newly-added files —
// which may contain hardcoded secrets or other reviewable issues — are not
// silently skipped. This matters for security: a developer who drops a
// config file with an API key into the repo without `git add` would
// otherwise bypass the rule engine entirely.
func gitWorkingTreeDiff(ctx context.Context, repo string) (string, error) {
	diff, err := gitOutput(ctx, repo, "diff", "HEAD")
	if err != nil {
		// unborn HEAD (no commits): fall back to unstaged-only diff.
		diff, err = gitOutput(ctx, repo, "diff")
		if err != nil {
			return "", fmt.Errorf("inputsource: git diff working tree: %w", err)
		}
	}
	untracked, err := gitUntrackedDiff(ctx, repo, int64(len(diff)))
	if err != nil {
		return "", fmt.Errorf("inputsource: git untracked diff: %w", err)
	}
	if diff != "" && untracked != "" {
		diff += "\n"
	}
	diff += untracked
	return diff, nil
}

// gitUntrackedDiff synthesises "new file" diffs for every untracked file
// reported by `git ls-files --others --exclude-standard`. The output is
// capped at MaxDiffBytes minus the bytes already consumed by the tracked
// diff so the combined working-tree diff never exceeds MaxDiffBytes.
//
// Each entry is resolved under repo and validated via syntheticDiffForFile,
// which rejects absolute paths, traversal, symlinks and non-regular files
// — the same safety net applied to --file-list mode.
func gitUntrackedDiff(ctx context.Context, repo string, alreadyUsed int64) (string, error) {
	if alreadyUsed >= MaxDiffBytes {
		return "", nil
	}
	out, err := gitOutput(ctx, repo, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", fmt.Errorf("ls-files: %w", err)
	}
	if out == "" {
		return "", nil
	}
	// Resolve repo to an absolute root once so syntheticDiffForFile's
	// pathUnder check is consistent across entries.
	absRepo, err := filepath.Abs(repo)
	if err != nil {
		return "", fmt.Errorf("resolve repo root: %w", err)
	}
	var diffs []string
	var totalSize int64
	for _, name := range strings.Split(strings.TrimRight(out, "\x00"), "\x00") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		// Reject absolute untracked paths defensively; git normally
		// emits repo-relative names, but a hostile or unusual setup
		// (e.g. core.worktree pointing elsewhere) could change that.
		if filepath.IsAbs(name) {
			return "", fmt.Errorf("reject absolute untracked path %q", name)
		}
		synth, serr := syntheticDiffForFile(name, absRepo, MaxDiffBytes-alreadyUsed-totalSize)
		if serr != nil {
			return "", serr
		}
		totalSize += int64(len(synth))
		diffs = append(diffs, synth)
	}
	return strings.Join(diffs, "\n"), nil
}

// gitOutput runs "git -C repo args..." capturing stdout via a pipe so the
// output is stream-limited to MaxDiffBytes and never fully buffered before
// the size check. stderr is discarded so unborn-HEAD fallbacks do not
// pollute test output.
func gitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	full := make([]string, 0, len(args)+2)
	full = append(full, "-C", repo)
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, MaxDiffBytes+1))
	waitErr := cmd.Wait()
	if readErr != nil {
		return "", readErr
	}
	if waitErr != nil {
		return "", waitErr
	}
	if int64(len(data)) > MaxDiffBytes {
		return "", fmt.Errorf("inputsource: git output exceeds max size %d bytes", MaxDiffBytes)
	}
	return string(data), nil
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
