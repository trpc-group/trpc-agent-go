//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package input

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

var errSourceLimit = errors.New("review input byte limit exceeded")

const (
	maxSnapshotFileBytes  = 1 << 20
	maxSnapshotTotalBytes = 8 << 20
)

// Selection identifies exactly one review input source.
type Selection struct {
	// DiffFile is a local Git-style unified diff file.
	DiffFile string
	// RepoPath is a local Git working tree whose changes are reviewed.
	RepoPath string
	// Fixture is a relative name in the configured fixture filesystem.
	Fixture string
}

// Validate checks that exactly one input source is selected.
func (s Selection) Validate() error {
	selected := 0
	for _, value := range []string{s.DiffFile, s.RepoPath, s.Fixture} {
		if value != "" {
			selected++
		}
	}
	if selected != 1 {
		return errors.New("validate review source: exactly one source is required")
	}
	return nil
}

// Loaded contains bounded raw input and its normalized parsed representation.
type Loaded struct {
	// Source identifies how the input was obtained.
	Source review.InputSource
	// Reference is the source path or fixture name selected by the caller.
	Reference string
	// RepositoryRoot is the resolved repository path for repository input.
	RepositoryRoot string
	// Digest is the lowercase SHA-256 digest of a versioned, layer-framed
	// representation of the raw source parts.
	Digest string
	// Raw is the bounded source diff.
	Raw []byte
	// Diff is the normalized parsed diff.
	Diff Diff
	// Snapshots contains complete changed Go files when the source provides them.
	Snapshots []Snapshot

	parts []rawPart
}

// Snapshot is a complete source file at one repository change layer.
type Snapshot struct {
	Layer   review.ChangeLayer
	Path    string
	Content []byte
}

type snapshotKey struct {
	layer review.ChangeLayer
	file  string
}

type rawPart struct {
	layer DiffLayer
	raw   []byte
}

type sourceOptions struct {
	maxBytes   int
	runner     CommandRunner
	fixtureFS  fs.FS
	fixtureDir string
	parse      []Option
}

// SourceOption configures Load.
type SourceOption func(*sourceOptions)

// WithSourceMaxBytes sets the maximum raw source size. It must be positive.
func WithSourceMaxBytes(maxBytes int) SourceOption {
	return func(options *sourceOptions) {
		options.maxBytes = maxBytes
	}
}

// WithCommandRunner replaces the direct process runner used for repository input.
func WithCommandRunner(runner CommandRunner) SourceOption {
	return func(options *sourceOptions) {
		options.runner = runner
	}
}

// WithFixtureFS configures the filesystem and directory used for fixture input.
func WithFixtureFS(filesystem fs.FS, directory string) SourceOption {
	return func(options *sourceOptions) {
		options.fixtureFS = filesystem
		options.fixtureDir = directory
	}
}

// WithParseOptions passes bounded parser options to Parse.
func WithParseOptions(options ...Option) SourceOption {
	return func(configuration *sourceOptions) {
		configuration.parse = append([]Option(nil), options...)
	}
}

// Load obtains, bounds, digests, and parses one selected review source.
func Load(ctx context.Context, selection Selection, opts ...SourceOption) (Loaded, error) {
	if ctx == nil {
		return Loaded{}, errors.New("load review source: nil context")
	}
	if err := selection.Validate(); err != nil {
		return Loaded{}, err
	}
	configuration := sourceOptions{
		maxBytes: defaultMaxInputBytes,
		runner:   ExecCommandRunner{},
	}
	for _, option := range opts {
		if option == nil {
			return Loaded{}, errors.New("load review source: nil option")
		}
		option(&configuration)
	}
	if configuration.maxBytes <= 0 {
		return Loaded{}, errors.New("load review source: maximum bytes must be positive")
	}
	if configuration.maxBytes == int(^uint(0)>>1) {
		return Loaded{}, errors.New("load review source: maximum bytes too large")
	}
	parserLimits, err := effectiveParserLimits(configuration)
	if err != nil {
		return Loaded{}, err
	}
	if parserLimits.MaxInputBytes < configuration.maxBytes {
		configuration.maxBytes = parserLimits.MaxInputBytes
	}
	if err := ctx.Err(); err != nil {
		return Loaded{}, err
	}

	loaded, err := loadRaw(ctx, selection, configuration)
	if err != nil {
		return Loaded{}, err
	}
	parts := loaded.parts
	if len(parts) == 0 {
		parts = []rawPart{{layer: DiffLayerUnified, raw: loaded.Raw}}
	}
	var parsed Diff
	remaining := parserLimits
	for _, part := range parts {
		if len(part.raw) == 0 {
			continue
		}
		partLines := physicalLineCount(part.raw)
		if remaining.MaxLines <= 0 || partLines > remaining.MaxLines {
			return Loaded{}, errors.New("load review source: parse diff: line limit exceeded")
		}
		if remaining.MaxFiles <= 0 {
			return Loaded{}, errors.New("load review source: parse diff: file limit exceeded")
		}
		partLimits := remaining
		if partLimits.MaxHunks <= 0 {
			partLimits.MaxHunks = 1
		}
		if partLimits.MaxChangedLines <= 0 {
			partLimits.MaxChangedLines = 1
		}
		partDiff, err := Parse(
			&contextReader{ctx: ctx, reader: bytes.NewReader(part.raw)},
			WithLimits(partLimits),
		)
		if err != nil {
			return Loaded{}, fmt.Errorf("load review source %s layer: %w", part.layer, err)
		}
		partHunks := 0
		partChangedLines := 0
		for index := range partDiff.Files {
			partDiff.Files[index].Layer = part.layer
			for _, hunk := range partDiff.Files[index].Hunks {
				partHunks++
				for _, line := range hunk.Lines {
					if line.Kind == LineAdded || line.Kind == LineDeleted {
						partChangedLines++
					}
				}
			}
		}
		if partHunks > remaining.MaxHunks {
			return Loaded{}, errors.New("load review source: parse diff: hunk limit exceeded")
		}
		if partChangedLines > remaining.MaxChangedLines {
			return Loaded{}, errors.New("load review source: parse diff: changed line limit exceeded")
		}
		parsed.Files = append(parsed.Files, partDiff.Files...)
		remaining.MaxLines -= partLines
		remaining.MaxFiles -= len(partDiff.Files)
		remaining.MaxHunks -= partHunks
		remaining.MaxChangedLines -= partChangedLines
	}
	if len(parsed.Files) == 0 {
		return Loaded{}, errors.New("load review source: parse diff: no files")
	}
	loaded.Digest = digestRawParts(parts)
	loaded.Diff = parsed
	if loaded.Source == review.InputSourceRepository {
		loaded.Snapshots, err = loadRepositorySnapshots(
			ctx,
			configuration.runner,
			loaded.RepositoryRoot,
			parsed,
		)
		if err != nil {
			return Loaded{}, err
		}
	}
	loaded.parts = nil
	if err := ctx.Err(); err != nil {
		return Loaded{}, err
	}
	return loaded, nil
}

func loadRepositorySnapshots(
	ctx context.Context,
	runner CommandRunner,
	root string,
	diff Diff,
) ([]Snapshot, error) {
	seen := make(map[snapshotKey]struct{})
	var snapshots []Snapshot
	total := 0
	for _, file := range diff.Files {
		if file.NewPath == "" || file.Binary || !strings.HasSuffix(file.NewPath, ".go") {
			continue
		}
		key := snapshotKey{layer: file.Layer, file: file.NewPath}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		remaining := maxSnapshotTotalBytes - total
		if remaining <= 0 {
			return nil, errors.New("load repository snapshots: total byte limit exceeded")
		}
		maximum := maxSnapshotFileBytes
		if remaining < maximum {
			maximum = remaining
		}
		var content []byte
		var err error
		switch file.Layer {
		case review.ChangeLayerStaged:
			content, err = loadIndexFile(ctx, runner, root, file.NewPath, maximum)
		case review.ChangeLayerWorktree:
			content, err = loadWorktreeFile(ctx, root, file.NewPath, maximum)
		default:
			return nil, errors.New("load repository snapshots: invalid change layer")
		}
		if err != nil {
			return nil, err
		}
		total += len(content)
		snapshots = append(snapshots, Snapshot{
			Layer:   file.Layer,
			Path:    file.NewPath,
			Content: content,
		})
	}
	return snapshots, nil
}

func loadIndexFile(
	ctx context.Context,
	runner CommandRunner,
	root string,
	filePath string,
	maximum int,
) ([]byte, error) {
	output := newBoundedWriter(maximum)
	err := runner.Run(
		ctx,
		"git",
		[]string{"-C", root, "cat-file", "blob", ":" + filePath},
		nil,
		output,
	)
	if output.exceeded {
		return nil, errors.New("load index snapshot: file byte limit exceeded")
	}
	if err != nil {
		return nil, fmt.Errorf("load index snapshot: %w", err)
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func loadWorktreeFile(
	ctx context.Context,
	root string,
	filePath string,
	maximum int,
) ([]byte, error) {
	candidate := filepath.Join(root, filepath.FromSlash(filePath))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return nil, fmt.Errorf("resolve worktree snapshot: %w", err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return nil, errors.New("resolve worktree snapshot: path escapes repository")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("open worktree snapshot: %w", err)
	}
	defer file.Close()
	content, err := readBounded(&contextReader{ctx: ctx, reader: file}, maximum)
	if err != nil {
		return nil, fmt.Errorf("read worktree snapshot: %w", err)
	}
	return content, nil
}

func digestRawParts(parts []rawPart) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("review-input/v1\x00"))
	var length [8]byte
	for _, part := range parts {
		_, _ = hash.Write([]byte(part.layer))
		_, _ = hash.Write([]byte{0})
		binary.BigEndian.PutUint64(length[:], uint64(len(part.raw)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write(part.raw)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func physicalLineCount(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	lines := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		lines++
	}
	return lines
}

func effectiveParserLimits(configuration sourceOptions) (Limits, error) {
	parserConfiguration := options{limits: defaultLimits()}
	for _, option := range configuration.parse {
		if option == nil {
			return Limits{}, errors.New("load review source: nil parse option")
		}
		option(&parserConfiguration)
	}
	if err := validateLimits(parserConfiguration.limits); err != nil {
		return Limits{}, fmt.Errorf("load review source: %w", err)
	}
	return parserConfiguration.limits, nil
}

func loadRaw(
	ctx context.Context,
	selection Selection,
	configuration sourceOptions,
) (Loaded, error) {
	switch {
	case selection.DiffFile != "":
		file, err := os.Open(selection.DiffFile)
		if err != nil {
			return Loaded{}, fmt.Errorf("load diff file: %w", err)
		}
		defer file.Close()
		raw, err := readBounded(&contextReader{ctx: ctx, reader: file}, configuration.maxBytes)
		if err != nil {
			return Loaded{}, fmt.Errorf("load diff file: %w", err)
		}
		return Loaded{
			Source:    review.InputSourceDiffFile,
			Reference: selection.DiffFile,
			Raw:       raw,
			parts:     []rawPart{{layer: DiffLayerUnified, raw: raw}},
		}, nil
	case selection.RepoPath != "":
		return loadRepository(ctx, selection.RepoPath, configuration)
	default:
		return loadFixture(ctx, selection.Fixture, configuration)
	}
}

func loadFixture(
	ctx context.Context,
	name string,
	configuration sourceOptions,
) (Loaded, error) {
	if configuration.fixtureFS == nil {
		return Loaded{}, errors.New("load fixture: nil fixture filesystem")
	}
	if !fs.ValidPath(name) || name == "." {
		return Loaded{}, fmt.Errorf("load fixture: invalid fixture name %q", name)
	}
	directory := configuration.fixtureDir
	if directory == "" {
		directory = "."
	}
	if !fs.ValidPath(directory) {
		return Loaded{}, fmt.Errorf("load fixture: invalid fixture directory %q", directory)
	}
	fixturePath := path.Join(directory, name)
	file, err := configuration.fixtureFS.Open(fixturePath)
	if err != nil {
		return Loaded{}, fmt.Errorf("load fixture %q: %w", name, err)
	}
	defer file.Close()
	raw, err := readBounded(&contextReader{ctx: ctx, reader: file}, configuration.maxBytes)
	if err != nil {
		return Loaded{}, fmt.Errorf("load fixture %q: %w", name, err)
	}
	return Loaded{
		Source:    review.InputSourceFixture,
		Reference: name,
		Raw:       raw,
		parts:     []rawPart{{layer: DiffLayerUnified, raw: raw}},
	}, nil
}

func loadRepository(
	ctx context.Context,
	repository string,
	configuration sourceOptions,
) (Loaded, error) {
	if configuration.runner == nil {
		return Loaded{}, errors.New("load repository: nil command runner")
	}
	root, err := filepath.Abs(repository)
	if err != nil {
		return Loaded{}, fmt.Errorf("resolve repository path: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Loaded{}, fmt.Errorf("resolve repository path: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return Loaded{}, fmt.Errorf("stat repository path: %w", err)
	}
	if !info.IsDir() {
		return Loaded{}, errors.New("load repository: repository path is not a directory")
	}
	root, err = gitRepositoryRoot(ctx, configuration.runner, root)
	if err != nil {
		return Loaded{}, err
	}
	base, err := gitDiffBase(ctx, configuration.runner, root)
	if err != nil {
		return Loaded{}, err
	}

	staged, err := runGitDiff(
		ctx,
		configuration.runner,
		root,
		base,
		true,
		configuration.maxBytes,
	)
	if err != nil {
		return Loaded{}, fmt.Errorf("load staged repository diff: %w", err)
	}
	worktreeLimit := configuration.maxBytes - len(staged)
	if len(staged) > 0 && staged[len(staged)-1] != '\n' {
		worktreeLimit--
	}
	if worktreeLimit < 0 {
		worktreeLimit = 0
	}
	worktree, err := runGitDiff(
		ctx,
		configuration.runner,
		root,
		"",
		false,
		worktreeLimit,
	)
	if err != nil {
		return Loaded{}, fmt.Errorf("load worktree repository diff: %w", err)
	}
	raw := joinRawParts(staged, worktree)
	if len(raw) > configuration.maxBytes {
		return Loaded{}, errSourceLimit
	}
	return Loaded{
		Source:         review.InputSourceRepository,
		Reference:      repository,
		RepositoryRoot: root,
		Raw:            raw,
		parts: []rawPart{
			{layer: DiffLayerStaged, raw: staged},
			{layer: DiffLayerWorktree, raw: worktree},
		},
	}, nil
}

func runGitDiff(
	ctx context.Context,
	runner CommandRunner,
	root string,
	base string,
	cached bool,
	maximum int,
) ([]byte, error) {
	output := newBoundedWriter(maximum)
	args := []string{
		"-c", "color.ui=false",
		"-c", "core.quotePath=true",
		"-c", "diff.suppressBlankEmpty=false",
		"-c", "diff.submodule=short",
		"-c", "diff.context=3",
		"-c", "diff.interHunkContext=0",
		"-c", "diff.algorithm=myers",
		"-c", "diff.indentHeuristic=false",
		"-c", "diff.renameLimit=10000",
		"-C", root,
		"diff",
		"--binary",
		"--no-ext-diff",
		"--no-textconv",
		"--no-color",
		"--unified=3",
		"--inter-hunk-context=0",
		"--submodule=short",
		"--src-prefix=a/",
		"--dst-prefix=b/",
		"--find-renames=50%",
		"--find-copies=50%",
		"-O/dev/null",
	}
	if cached {
		args = append(args, "--cached")
	}
	if base != "" {
		args = append(args, base)
	}
	args = append(args, "--")
	err := runner.Run(ctx, "git", args, nil, output)
	if output.exceeded {
		return nil, errSourceLimit
	}
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func joinRawParts(parts ...[]byte) []byte {
	var joined []byte
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		if len(joined) > 0 && joined[len(joined)-1] != '\n' {
			joined = append(joined, '\n')
		}
		joined = append(joined, part...)
	}
	return joined
}

func gitRepositoryRoot(
	ctx context.Context,
	runner CommandRunner,
	directory string,
) (string, error) {
	output := newBoundedWriter(16 << 10)
	err := runner.Run(
		ctx,
		"git",
		[]string{"-C", directory, "rev-parse", "--show-toplevel"},
		nil,
		output,
	)
	if output.exceeded {
		return "", errors.New("resolve repository root: output limit exceeded")
	}
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	value, ok := trimGitOutputLine(output.String())
	if !ok || value == "" || strings.ContainsRune(value, '\x00') || !filepath.IsAbs(value) {
		return "", errors.New("resolve repository root: invalid git output")
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(value))
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("stat repository root: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("resolve repository root: git output is not a directory")
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return "", errors.New("resolve repository root: selected path is outside git root")
	}
	return root, nil
}

func trimGitOutputLine(value string) (string, bool) {
	switch {
	case strings.HasSuffix(value, "\r\n"):
		value = strings.TrimSuffix(value, "\r\n")
	case strings.HasSuffix(value, "\n"):
		value = strings.TrimSuffix(value, "\n")
	default:
		return "", false
	}
	return value, !strings.ContainsAny(value, "\r\n")
}

func gitDiffBase(
	ctx context.Context,
	runner CommandRunner,
	root string,
) (string, error) {
	head := newBoundedWriter(256)
	err := runner.Run(
		ctx,
		"git",
		[]string{"-C", root, "rev-parse", "--verify", "HEAD"},
		nil,
		head,
	)
	if head.exceeded {
		return "", errors.New("resolve diff base: output limit exceeded")
	}
	if err == nil {
		value, ok := trimGitOutputLine(head.String())
		if !ok {
			return "", errors.New("resolve diff base: invalid HEAD output")
		}
		if isHexObjectID(value) {
			return value, nil
		}
		return "", errors.New("resolve diff base: invalid HEAD object id")
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if !isCommandExitCode(err, 1, 128) {
		return "", fmt.Errorf("resolve diff base: %w", err)
	}
	ref, unborn, unbornErr := gitUnbornHead(ctx, runner, root)
	if unbornErr != nil {
		return "", unbornErr
	}
	if !unborn {
		return "", fmt.Errorf("resolve diff base for existing HEAD %q: %w", ref, err)
	}
	emptyTree := newBoundedWriter(256)
	err = runner.Run(
		ctx,
		"git",
		[]string{"-C", root, "hash-object", "-t", "tree", "--stdin"},
		strings.NewReader(""),
		emptyTree,
	)
	if err != nil {
		return "", fmt.Errorf("resolve empty tree: %w", err)
	}
	value, ok := trimGitOutputLine(emptyTree.String())
	if emptyTree.exceeded || !ok || !isHexObjectID(value) {
		return "", errors.New("resolve empty tree: invalid object id")
	}
	return value, nil
}

func gitUnbornHead(
	ctx context.Context,
	runner CommandRunner,
	root string,
) (string, bool, error) {
	referenceOutput := newBoundedWriter(1024)
	err := runner.Run(
		ctx,
		"git",
		[]string{"-C", root, "symbolic-ref", "-q", "HEAD"},
		nil,
		referenceOutput,
	)
	if err != nil {
		return "", false, fmt.Errorf("resolve symbolic HEAD: %w", err)
	}
	reference, ok := trimGitOutputLine(referenceOutput.String())
	if referenceOutput.exceeded || !ok || !strings.HasPrefix(reference, "refs/heads/") {
		return "", false, errors.New("resolve symbolic HEAD: invalid reference")
	}
	verifyOutput := newBoundedWriter(1)
	err = runner.Run(
		ctx,
		"git",
		[]string{"-C", root, "show-ref", "--verify", "--quiet", reference},
		nil,
		verifyOutput,
	)
	if err == nil {
		return reference, false, nil
	}
	if isCommandExitCode(err, 1) {
		return reference, true, nil
	}
	return "", false, fmt.Errorf("verify symbolic HEAD: %w", err)
}

func isHexObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func readBounded(reader io.Reader, maximum int) ([]byte, error) {
	limited := io.LimitReader(reader, int64(maximum)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(data) > maximum {
		return nil, errSourceLimit
	}
	return data, nil
}

type boundedWriter struct {
	buffer    bytes.Buffer
	remaining int
	exceeded  bool
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	read, err := r.reader.Read(data)
	if contextErr := r.ctx.Err(); contextErr != nil {
		return read, contextErr
	}
	return read, err
}

func newBoundedWriter(maximum int) *boundedWriter {
	return &boundedWriter{remaining: maximum}
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	if len(data) > w.remaining {
		w.exceeded = true
		return 0, errSourceLimit
	}
	w.remaining -= len(data)
	return w.buffer.Write(data)
}

func (w *boundedWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

func (w *boundedWriter) Bytes() []byte {
	return w.buffer.Bytes()
}

func (w *boundedWriter) String() string {
	return w.buffer.String()
}
