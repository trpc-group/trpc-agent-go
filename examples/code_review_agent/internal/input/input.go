//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package input validates CLI options and resolves bounded review inputs.
package input

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
)

const (
	defaultMaxDiffBytes  = 8 << 20
	defaultMaxFileBytes  = 1 << 20
	defaultMaxFiles      = 500
	defaultMaxHunks      = 5000
	defaultMaxAdded      = 100000
	defaultReviewTimeout = 2 * time.Minute
)

// Limits bounds all caller-controlled input before analysis.
type Limits struct {
	MaxDiffBytes int64
	MaxFileBytes int64
	MaxFiles     int
	MaxHunks     int
	MaxAdded     int
}

// Config contains validated review input and execution options.
type Config struct {
	DiffFile   string
	RepoPath   string
	FilesFile  string
	Fixture    string
	Runtime    string
	AllowLocal bool
	RuleOnly   bool
	FakeModel  bool
	DryRun     bool
	DBPath     string
	OutputDir  string
	SkillsRoot string
	Timeout    time.Duration
	Limits     Limits
}

// ParseConfig parses and validates CLI arguments without global flag state.
func ParseConfig(args []string) (Config, error) {
	var c Config
	set := flag.NewFlagSet("code-review-agent", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.StringVar(&c.DiffFile, "diff-file", "", "unified diff or PR patch")
	set.StringVar(&c.RepoPath, "repo-path", "", "local git worktree")
	set.StringVar(&c.FilesFile, "files-file", "", "newline-delimited repo-relative files")
	set.StringVar(&c.Fixture, "fixture", "", "built-in fixture name")
	set.StringVar(&c.Runtime, "runtime", "container", "container, local, or fake")
	set.BoolVar(&c.AllowLocal, "allow-local", false, "allow unsafe development fallback")
	set.BoolVar(&c.RuleOnly, "rule-only", false, "run deterministic rules only")
	set.BoolVar(&c.FakeModel, "fake-model", false, "use deterministic scripted model")
	set.BoolVar(&c.DryRun, "dry-run", false, "use fake sandbox with full persistence path")
	set.StringVar(&c.DBPath, "db", "review.db", "SQLite database path")
	set.StringVar(&c.OutputDir, "output-dir", ".", "report output directory")
	set.StringVar(&c.SkillsRoot, "skills-root", filepath.FromSlash("skills"), "skills repository root")
	set.DurationVar(&c.Timeout, "timeout", defaultReviewTimeout, "total review deadline")
	if err := set.Parse(args); err != nil {
		return Config{}, err
	}
	c.Limits = Limits{defaultMaxDiffBytes, defaultMaxFileBytes, defaultMaxFiles, defaultMaxHunks, defaultMaxAdded}
	return c, c.validate(set.Args())
}
func (c Config) validate(extra []string) error {
	if len(extra) != 0 {
		return fmt.Errorf("unexpected arguments: %v", extra)
	}
	inputs := boolInt(c.DiffFile != "") + boolInt(c.RepoPath != "") + boolInt(c.Fixture != "")
	if inputs != 1 {
		return errors.New("exactly one of --diff-file, --repo-path, or --fixture is required")
	}
	if c.FilesFile != "" && c.RepoPath == "" {
		return errors.New("--files-file requires --repo-path")
	}
	if c.Runtime != "container" && c.Runtime != "local" && c.Runtime != "fake" {
		return fmt.Errorf("unsupported runtime %q", c.Runtime)
	}
	if c.Runtime == "local" && !c.AllowLocal {
		return errors.New("--runtime=local requires --allow-local")
	}
	if c.Timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	return nil
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

const (
	initialFileListBufferBytes = 4096
	maxFileListLineBytes       = 64 << 10
)

func readFileList(root, listPath string, limits Limits) (paths []string, resultErr error) {
	file, err := os.Open(listPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
	}()
	paths = make([]string, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, initialFileListBufferBytes), maxFileListLineBytes)
	for scanner.Scan() {
		item := strings.TrimSpace(scanner.Text())
		if item == "" {
			continue
		}
		clean, cleanErr := cleanRelativePath(root, item)
		if cleanErr != nil {
			return nil, cleanErr
		}
		paths = append(paths, clean)
		if len(paths) > limits.MaxFiles {
			return nil, fmt.Errorf("file list exceeds %d entries", limits.MaxFiles)
		}
	}
	return paths, scanner.Err()
}
func newFilesDiff(root string, paths []string, limits Limits) ([]byte, error) {
	var result bytes.Buffer
	for _, path := range paths {
		data, err := readBoundedFile(filepath.Join(root, filepath.FromSlash(path)), limits.MaxFileBytes)
		if err != nil {
			return nil, err
		}
		oldPath, newPath := gitDiffPath("a/", path), gitDiffPath("b/", path)
		if bytes.IndexByte(data, 0) >= 0 {
			if _, err := fmt.Fprintf(&result, "diff --git %s %s\nnew file mode 100644\nBinary files /dev/null and %s differ\n", oldPath, newPath, newPath); err != nil {
				return nil, err
			}
			if int64(result.Len()) > limits.MaxDiffBytes {
				return nil, fmt.Errorf("generated diff exceeds %d bytes", limits.MaxDiffBytes)
			}
			continue
		}
		lines := splitFileLines(data)
		if _, err := fmt.Fprintf(&result, "diff --git %s %s\n--- /dev/null\n+++ %s\n@@ -0,0 +1,%d @@\n", oldPath, newPath, newPath, len(lines)); err != nil {
			return nil, err
		}
		for _, line := range lines {
			result.WriteByte('+')
			result.Write(line)
			result.WriteByte('\n')
		}
		if int64(result.Len()) > limits.MaxDiffBytes {
			return nil, fmt.Errorf("generated diff exceeds %d bytes", limits.MaxDiffBytes)
		}
	}
	return result.Bytes(), nil
}
func gitDiffPath(prefix, path string) string {
	value := []byte(prefix + path)
	if bytes.IndexFunc(value, func(r rune) bool { return r < 0x21 || r > 0x7e || r == '"' || r == '\\' }) < 0 {
		return string(value)
	}
	var quoted strings.Builder
	quoted.WriteByte('"')
	for _, char := range value {
		switch char {
		case '"', '\\':
			quoted.WriteByte('\\')
			quoted.WriteByte(char)
		case '\n':
			quoted.WriteString(`\n`)
		case '\r':
			quoted.WriteString(`\r`)
		case '\t':
			quoted.WriteString(`\t`)
		default:
			if char < 0x20 || char >= 0x7f {
				fmt.Fprintf(&quoted, `\%03o`, char)
			} else {
				quoted.WriteByte(char)
			}
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}
func splitFileLines(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	data = bytes.TrimSuffix(data, []byte{'\n'})
	if len(data) == 0 {
		return [][]byte{{}}
	}
	return bytes.Split(data, []byte{'\n'})
}

const maxGitErrorBytes = 64 << 10
const gitProbeBytes = 1024
const maxGitInventoryBytes = 16 << 20

func collectGitDiff(ctx context.Context, root string, limits Limits) ([]byte, error) {
	hasHead, err := gitHasHead(ctx, root)
	if err != nil {
		return nil, err
	}
	if !hasHead {
		return collectUnbornDiff(ctx, root, limits)
	}
	data, err := runGit(ctx, root, limits.MaxDiffBytes, "diff", "HEAD", "--binary", "--no-ext-diff", "--no-textconv", "--ignore-submodules=all", "--")
	if err != nil {
		return nil, err
	}
	all := bytes.NewBuffer(data)
	return appendUntracked(ctx, root, limits, all)
}
func gitHasHead(ctx context.Context, root string) (bool, error) {
	_, err := runGit(ctx, root, gitProbeBytes, "rev-parse", "--verify", "--quiet", "HEAD")
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}
func collectUnbornDiff(ctx context.Context, root string, limits Limits) ([]byte, error) {
	inventory, err := repositoryInventory(ctx, root)
	if err != nil {
		return nil, err
	}
	var listed bytes.Buffer
	for _, entry := range inventory {
		listed.WriteString(entry.Path)
		listed.WriteByte(0)
	}
	all := &bytes.Buffer{}
	if err := appendListedFiles(root, limits, all, listed.Bytes()); err != nil {
		return nil, err
	}
	return all.Bytes(), nil
}
func appendUntracked(ctx context.Context, root string, limits Limits, all *bytes.Buffer) ([]byte, error) {
	untracked, err := runGit(ctx, root, limits.MaxDiffBytes-int64(all.Len()), "ls-files", "--others", "--exclude-standard", "-z", "--")
	if err != nil {
		return nil, err
	}
	if err := appendListedFiles(root, limits, all, untracked); err != nil {
		return nil, err
	}
	return all.Bytes(), nil
}
func appendListedFiles(root string, limits Limits, all *bytes.Buffer, listed []byte) error {
	untrackedCount := 0
	for _, rawPath := range bytes.Split(listed, []byte{0}) {
		if len(rawPath) == 0 {
			continue
		}
		untrackedCount++
		if untrackedCount > limits.MaxFiles {
			return fmt.Errorf("listed files exceed %d entries", limits.MaxFiles)
		}
		path, cleanErr := cleanRelativePath(root, string(rawPath))
		if cleanErr != nil {
			return cleanErr
		}
		remaining := limits
		remaining.MaxDiffBytes -= int64(all.Len())
		remaining.MaxFiles = 1
		patch, patchErr := newFilesDiff(root, []string{path}, remaining)
		if patchErr != nil {
			return patchErr
		}
		all.Write(patch)
	}
	return nil
}
func runGit(ctx context.Context, root string, limit int64, args ...string) ([]byte, error) {
	if limit <= 0 {
		return nil, errors.New("git output exceeds diff limit")
	}
	// args are selected only by the fixed callers in this package.
	//nolint:gosec
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-pager", "-c", "safe.directory=*", "-C", root}, args...)...)
	cmd.Env = gitEnvironment()
	var out, errOut boundedBuffer
	out.limit, errOut.limit = limit, maxGitErrorBytes
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errOut.String()))
	}
	if out.truncated {
		return nil, errors.New("git output exceeds diff limit")
	}
	return out.Bytes(), nil
}

func repositoryInventory(ctx context.Context, root string) ([]snapshotEntry, error) {
	tracked, err := runGit(ctx, root, maxGitInventoryBytes, "ls-files", "--stage", "--cached", "-z", "--")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]snapshotEntry)
	for _, raw := range bytes.Split(tracked, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		tab := bytes.IndexByte(raw, '\t')
		if tab < 0 {
			return nil, errors.New("invalid git index entry")
		}
		fields := bytes.Fields(raw[:tab])
		if len(fields) != 3 {
			return nil, errors.New("invalid git index metadata")
		}
		if string(fields[2]) != "0" {
			return nil, errors.New(
				"repository index contains unresolved merge entries",
			)
		}
		mode := string(fields[0])
		if mode == "160000" {
			continue
		}
		path, cleanErr := cleanSnapshotPath(string(raw[tab+1:]))
		if cleanErr != nil {
			return nil, cleanErr
		}
		_, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return nil, statErr
		}
		seen[path] = snapshotEntry{Path: path, Executable: mode == "100755"}
		if len(seen) > maxSnapshotFiles {
			return nil, fmt.Errorf("repository exceeds %d files", maxSnapshotFiles)
		}
	}
	untracked, err := runGit(ctx, root, maxGitInventoryBytes, "ls-files", "--others", "--exclude-standard", "-z", "--")
	if err != nil {
		return nil, err
	}
	for _, raw := range bytes.Split(untracked, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		path, cleanErr := cleanSnapshotPath(string(raw))
		if cleanErr != nil {
			return nil, cleanErr
		}
		info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		if statErr != nil {
			return nil, statErr
		}
		seen[path] = snapshotEntry{Path: path, Executable: info.Mode().Perm()&0o111 != 0}
		if len(seen) > maxSnapshotFiles {
			return nil, fmt.Errorf("repository exceeds %d files", maxSnapshotFiles)
		}
	}
	result := make([]snapshotEntry, 0, len(seen))
	for _, entry := range seen {
		result = append(result, entry)
	}
	slices.SortFunc(result, func(left, right snapshotEntry) int {
		return strings.Compare(left.Path, right.Path)
	})
	return result, nil
}

func mergeInventory(root string, inventory []snapshotEntry, explicit []string) ([]snapshotEntry, error) {
	seen := make(map[string]snapshotEntry, len(inventory)+len(explicit))
	for _, entry := range inventory {
		seen[entry.Path] = entry
	}
	for _, value := range explicit {
		path, err := cleanSnapshotPath(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[path]; ok {
			continue
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, err
		}
		seen[path] = snapshotEntry{Path: path, Executable: info.Mode().Perm()&0o111 != 0}
	}
	result := make([]snapshotEntry, 0, len(seen))
	for _, entry := range seen {
		result = append(result, entry)
	}
	slices.SortFunc(result, func(left, right snapshotEntry) int {
		return strings.Compare(left.Path, right.Path)
	})
	return result, nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit     int64
	written   int64
	truncated bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := b.limit - b.written
	if remaining > 0 {
		keep := int64(len(data))
		if keep > remaining {
			keep = remaining
		}
		if _, err := b.Buffer.Write(data[:keep]); err != nil {
			return 0, err
		}
		b.written += keep
	}
	if int64(original) > remaining {
		b.truncated = true
	}
	return original, nil
}
func gitEnvironment() []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat"}
	if runtime.GOOS == "windows" {
		return append(env, "GIT_CONFIG_GLOBAL=NUL")
	}
	return append(env, "GIT_CONFIG_GLOBAL=/dev/null")
}

// Summary contains ephemeral raw input and persistable metadata.
type Summary struct {
	Kind             string
	Digest           string
	RepoRoot         string
	RepositoryDigest string
	// Cleanup releases the immutable repository snapshot, when present.
	Cleanup  func() error
	Files    []diffparse.ChangedFile
	Packages []string
	// ModuleHints includes changed Go packages and module metadata directories.
	// It is ephemeral execution input and is not persisted as package metadata.
	ModuleHints []string
	// ExecutablePaths preserves Git index execution semantics for staging.
	ExecutablePaths []string
	Sources         map[string][]byte
	RawDiff         []byte
	Hunks           int
	Added           int
}

// PersistableMetadata excludes raw diff and source content.
type PersistableMetadata struct {
	Kind       string   `json:"kind"`
	Digest     string   `json:"sha256"`
	FileCount  int      `json:"file_count"`
	HunkCount  int      `json:"hunk_count"`
	AddedLines int      `json:"added_lines"`
	Packages   []string `json:"packages"`
}

// Metadata returns the only input fields safe for persistence.
func (s Summary) Metadata() PersistableMetadata {
	return PersistableMetadata{s.Kind, s.Digest, len(s.Files), s.Hunks, s.Added, append([]string(nil), s.Packages...)}
}

// Load resolves the selected input and enforces aggregate limits.
func Load(ctx context.Context, config Config) (Summary, error) {
	var sourceRoot, sourceDigest string
	var inventory []snapshotEntry
	var explicitPaths []string
	var selectedInventory, includeGitInventory bool
	if config.RepoPath != "" {
		var err error
		sourceRoot, err = secureRepoRoot(config.RepoPath)
		if err != nil {
			return Summary{}, err
		}
		if config.FilesFile != "" {
			explicitPaths, err = readFileList(sourceRoot, config.FilesFile, config.Limits)
			if err != nil {
				return Summary{}, err
			}
		}
		inventory, err = repositoryInventory(ctx, sourceRoot)
		if err != nil && config.FilesFile == "" {
			return Summary{}, fmt.Errorf("list repository snapshot: %w", err)
		}
		if err == nil {
			includeGitInventory = true
		} else {
			inventory = nil
		}
		inventory, err = mergeInventory(sourceRoot, inventory, explicitPaths)
		if err != nil {
			return Summary{}, err
		}
		selectedInventory = true
		sourceDigest, err = digestPaths(sourceRoot, inventory, config.Limits.MaxFileBytes)
		if err != nil {
			return Summary{}, fmt.Errorf("digest repository before diff: %w", err)
		}
	} else if config.Fixture != "" {
		sourceRoot = filepath.Join("fixtures", config.Fixture, "repo")
		var err error
		sourceDigest, err = digestTree(sourceRoot, config.Limits.MaxFileBytes)
		if err != nil {
			return Summary{}, fmt.Errorf("digest fixture before diff: %w", err)
		}
	}
	kind, repoRoot, raw, err := loadRaw(ctx, config, explicitPaths)
	if err != nil {
		return Summary{}, err
	}
	if int64(len(raw)) > config.Limits.MaxDiffBytes {
		return Summary{}, fmt.Errorf("diff exceeds %d bytes", config.Limits.MaxDiffBytes)
	}
	var cleanup func() error
	var snapshotDigest string
	var executablePaths []string
	if repoRoot != "" {
		originalRoot := repoRoot
		var snapshot string
		var digest string
		var snapshotCleanup func() error
		var snapshotErr error
		if selectedInventory {
			snapshot, digest, snapshotCleanup, snapshotErr = snapshotRepositoryPaths(originalRoot, inventory, config.Limits)
		} else {
			snapshot, digest, snapshotCleanup, snapshotErr = snapshotRepository(originalRoot, config.Limits)
		}
		if snapshotErr != nil {
			return Summary{}, fmt.Errorf("snapshot repository: %w", snapshotErr)
		}
		snapshotEntries := inventory
		if !selectedInventory {
			snapshotEntries, snapshotErr = snapshotTreePaths(snapshot)
			if snapshotErr != nil {
				return Summary{}, errors.Join(snapshotErr, snapshotCleanup())
			}
		}
		for _, entry := range snapshotEntries {
			if entry.Executable {
				executablePaths = append(executablePaths, entry.Path)
			}
		}
		if sourceDigest != "" {
			afterInventory := inventory
			var inventoryErr error
			if selectedInventory && includeGitInventory {
				afterInventory, inventoryErr = repositoryInventory(ctx, originalRoot)
				if inventoryErr == nil {
					afterInventory, inventoryErr = mergeInventory(originalRoot, afterInventory, explicitPaths)
				}
			}
			var afterDigest string
			var digestErr error
			if selectedInventory {
				afterDigest, digestErr = digestPaths(originalRoot, afterInventory, config.Limits.MaxFileBytes)
			} else {
				afterDigest, digestErr = digestTree(originalRoot, config.Limits.MaxFileBytes)
			}
			if inventoryErr != nil || digestErr != nil || !slices.Equal(afterInventory, inventory) || afterDigest != sourceDigest || afterDigest != digest {
				digestErr = errors.Join(inventoryErr, digestErr)
				return Summary{}, errors.Join(errors.New("repository changed while loading review input"), digestErr, snapshotCleanup())
			}
		}
		if config.RepoPath != "" {
			if rawErr := verifyRawUnchanged(
				ctx,
				config,
				explicitPaths,
				kind,
				originalRoot,
				raw,
			); rawErr != nil {
				return Summary{}, errors.Join(
					rawErr,
					snapshotCleanup(),
				)
			}
		}
		repoRoot, cleanup = snapshot, snapshotCleanup
		snapshotDigest = digest
	}
	files, err := diffparse.Parse(raw)
	if err != nil {
		return Summary{}, cleanupLoadSnapshot(cleanup, err)
	}
	hunks, added := diffparse.Stats(files)
	if len(files) > config.Limits.MaxFiles || hunks > config.Limits.MaxHunks || added > config.Limits.MaxAdded {
		return Summary{}, cleanupLoadSnapshot(cleanup, errors.New("input exceeds file, hunk, or added-line limit"))
	}
	sources, err := loadChangedSources(repoRoot, files, config.Limits)
	if err != nil {
		return Summary{}, cleanupLoadSnapshot(cleanup, err)
	}
	digest := sha256.Sum256(raw)
	packages := ResolvePackages(repoRoot, files)
	moduleHints := ResolveModuleHints(files)
	return Summary{Kind: kind, Digest: hex.EncodeToString(digest[:]), RepoRoot: repoRoot, RepositoryDigest: snapshotDigest, Cleanup: cleanup, Files: files, Packages: packages, ModuleHints: moduleHints, ExecutablePaths: executablePaths, Sources: sources, RawDiff: raw, Hunks: hunks, Added: added}, nil
}

func verifyRawUnchanged(
	ctx context.Context,
	config Config,
	explicitPaths []string,
	kind string,
	root string,
	raw []byte,
) error {
	afterKind, afterRoot, afterRaw, err := loadRaw(
		ctx,
		config,
		explicitPaths,
	)
	if err != nil || afterKind != kind || afterRoot != root ||
		!bytes.Equal(afterRaw, raw) {
		return errors.Join(
			errors.New("review diff changed while loading input"),
			err,
		)
	}
	return nil
}

func cleanupLoadSnapshot(cleanup func() error, cause error) error {
	if cleanup == nil {
		return cause
	}
	return errors.Join(cause, cleanup())
}
func loadChangedSources(root string, files []diffparse.ChangedFile, limits Limits) (map[string][]byte, error) {
	if root == "" {
		return nil, nil
	}
	result := make(map[string][]byte)
	var total int64
	for _, file := range files {
		if file.Deleted || file.Binary || filepath.Ext(file.NewPath) != ".go" {
			continue
		}
		path, err := cleanRelativePath(root, file.NewPath)
		if err != nil {
			return nil, fmt.Errorf("resolve changed source %q: %w", file.NewPath, err)
		}
		data, err := readBoundedFile(filepath.Join(root, filepath.FromSlash(path)), limits.MaxFileBytes)
		if err != nil {
			return nil, fmt.Errorf("read changed source %q: %w", path, err)
		}
		total += int64(len(data))
		if total > limits.MaxDiffBytes {
			return nil, fmt.Errorf("changed source exceeds %d bytes", limits.MaxDiffBytes)
		}
		result[path] = data
	}
	return result, nil
}
func loadRaw(ctx context.Context, config Config, explicitPaths []string) (string, string, []byte, error) {
	switch {
	case config.DiffFile != "":
		data, err := readBoundedFile(config.DiffFile, config.Limits.MaxDiffBytes)
		return "diff", "", data, err
	case config.Fixture != "":
		if !validFixtureName(config.Fixture) {
			return "", "", nil, errors.New("fixture name must contain only lowercase letters, digits, and underscores")
		}
		path := filepath.Join("fixtures", config.Fixture, "input.diff")
		data, err := readBoundedFile(path, config.Limits.MaxDiffBytes)
		return "fixture", filepath.Join("fixtures", config.Fixture, "repo"), data, err
	default:
		root, err := secureRepoRoot(config.RepoPath)
		if err != nil {
			return "", "", nil, err
		}
		if config.FilesFile != "" {
			data, diffErr := newFilesDiff(root, explicitPaths, config.Limits)
			return "files", root, data, diffErr
		}
		data, gitErr := collectGitDiff(ctx, root, config.Limits)
		return "repo", root, data, gitErr
	}
}
func readBoundedFile(path string, limit int64) (data []byte, resultErr error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
	}()
	data, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file %q exceeds %d bytes", path, limit)
	}
	return data, nil
}
func validFixtureName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// ResolvePackages derives package directories without executing project code.
func ResolvePackages(root string, files []diffparse.ChangedFile) []string {
	if root == "" {
		return nil
	}
	seen := make(map[string]struct{})
	for _, file := range files {
		path := file.NewPath
		if path == "" {
			path = file.OldPath
		}
		if filepath.Ext(path) != ".go" {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		seen[dir] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for dir := range seen {
		result = append(result, dir)
	}
	sort.Strings(result)
	return result
}

// ResolveModuleHints derives directories whose selected Go module must be checked.
func ResolveModuleHints(files []diffparse.ChangedFile) []string {
	seen := make(map[string]struct{})
	for _, file := range files {
		for _, filePath := range []string{file.OldPath, file.NewPath} {
			if filePath == "" {
				continue
			}
			seen[filepath.ToSlash(filepath.Dir(filePath))] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for directory := range seen {
		result = append(result, directory)
	}
	sort.Strings(result)
	return result
}

func secureRepoRoot(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("repo path is not a directory: %q", path)
	}
	return resolved, nil
}
func cleanRelativePath(root, path string) (string, error) {
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("invalid relative path %q", path)
	}
	joined := filepath.Join(root, filepath.Clean(path))
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository: %q", path)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("path is not a regular file: %q", path)
	}
	return filepath.ToSlash(rel), nil
}
