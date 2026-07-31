//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxSandboxSnapshotBytes          = int64(128 * 1024 * 1024)
	maxSandboxSnapshotFiles          = 20_000
	maxSandboxSnapshotDirectories    = 5_000
	maxSandboxSnapshotPathBytes      = 4 * 1024
	maxSandboxSnapshotTotalPathBytes = int64(8 * 1024 * 1024)
	snapshotCopyBufferBytes          = 32 * 1024
)

const (
	reviewModuleManifestName    = ".trpc-agent-review-modules"
	reviewWorkspaceManifestName = ".trpc-agent-review-workspaces"
)

var (
	errSnapshotCopyLimit       = errors.New("snapshot copy exceeds remaining byte limit")
	errGitPathOutputTerminator = errors.New("git ls-files output is not NUL terminated")
)

type snapshotLimits struct {
	maxBytes          int64
	maxFiles          int
	maxDirectories    int
	maxPathBytes      int
	maxTotalPathBytes int64
}

func defaultSandboxSnapshotLimits() snapshotLimits {
	return snapshotLimits{
		maxBytes:          maxSandboxSnapshotBytes,
		maxFiles:          maxSandboxSnapshotFiles,
		maxDirectories:    maxSandboxSnapshotDirectories,
		maxPathBytes:      maxSandboxSnapshotPathBytes,
		maxTotalPathBytes: maxSandboxSnapshotTotalPathBytes,
	}
}

func (l snapshotLimits) validate() error {
	if l.maxBytes <= 0 || l.maxFiles <= 0 || l.maxDirectories <= 0 ||
		l.maxPathBytes <= 0 || l.maxTotalPathBytes <= 0 {
		return fmt.Errorf("repository snapshot limits must all be positive")
	}
	return nil
}

type repoSnapshot struct {
	Root  string
	Files int
	Bytes int64
}

type repositoryValidationTargetKind uint8

const (
	repositoryValidationTargetModule repositoryValidationTargetKind = iota + 1
	repositoryValidationTargetWorkspace
)

type repositoryValidationTarget struct {
	path               string
	requireRegularFile bool
	kind               repositoryValidationTargetKind
}

type snapshotBuilder struct {
	repoRoot     string
	snapshotRoot string
	limits       snapshotLimits
	entries      int
	files        int
	bytes        int64
	pathBytes    int64
	directories  map[string]bool
}

func prepareSandboxRepoSnapshot(
	ctx context.Context,
	repoRoot string,
	filesScope []string,
	limits snapshotLimits,
) (repoSnapshot, error) {
	if err := limits.validate(); err != nil {
		return repoSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return repoSnapshot{}, fmt.Errorf("prepare repository snapshot: %w", err)
	}
	root := repoRoot
	if root == "" {
		return repoSnapshot{}, fmt.Errorf("repository path is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return repoSnapshot{}, fmt.Errorf("resolve repository path: %w", err)
	}
	resolvedRoot, err := resolveExistingPath(absRoot)
	if err != nil {
		return repoSnapshot{}, fmt.Errorf("resolve repository symlinks: %w", err)
	}
	scope, err := cleanSnapshotScope(filesScope)
	if err != nil {
		return repoSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return repoSnapshot{}, fmt.Errorf("create repository snapshot: %w", err)
	}
	snapshotRoot, err := os.MkdirTemp("", "code-review-repo-snapshot-*")
	if err != nil {
		return repoSnapshot{}, fmt.Errorf("create repository snapshot: %w", err)
	}
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(snapshotRoot)
		}
	}()

	builder := snapshotBuilder{
		repoRoot:     resolvedRoot,
		snapshotRoot: snapshotRoot,
		limits:       limits,
		directories:  make(map[string]bool),
	}
	if err := walkGitTrackedFiles(ctx, resolvedRoot, scope, limits.maxPathBytes, builder.stageTrackedFile); err != nil {
		return repoSnapshot{}, err
	}
	cleanupOnError = false
	return repoSnapshot{Root: snapshotRoot, Files: builder.files, Bytes: builder.bytes}, nil
}

func (b *snapshotBuilder) stageTrackedFile(ctx context.Context, raw string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("stage tracked file: %w", err)
	}
	b.entries++
	if b.entries > b.limits.maxFiles {
		return fmt.Errorf("repository snapshot exceeds %d tracked entries", b.limits.maxFiles)
	}
	pathBytes := int64(len(raw))
	if len(raw) > b.limits.maxPathBytes {
		return fmt.Errorf("tracked path exceeds %d bytes", b.limits.maxPathBytes)
	}
	if pathBytes > b.limits.maxTotalPathBytes-b.pathBytes {
		return fmt.Errorf(
			"repository snapshot tracked paths exceed %d bytes",
			b.limits.maxTotalPathBytes,
		)
	}
	b.pathBytes += pathBytes

	rel, err := cleanTrackedPath(raw)
	if err != nil {
		return err
	}
	if err := b.accountDirectories(rel); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("stage tracked file %q: %w", rel, err)
	}
	src := filepath.Join(b.repoRoot, filepath.FromSlash(rel))
	if !pathStaysWithin(b.repoRoot, src) {
		return fmt.Errorf("tracked path %q escapes the repository", raw)
	}
	info, err := os.Lstat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat tracked file %q: %w", rel, err)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("resolve tracked file %q: %w", rel, err)
	}
	resolvedSrc, err := resolveExistingPath(src)
	if err != nil {
		return fmt.Errorf("resolve tracked file %q: %w", rel, err)
	}
	if !pathStaysWithin(b.repoRoot, resolvedSrc) {
		return fmt.Errorf("tracked path %q resolves outside the repository", raw)
	}
	resolvedInfo, err := os.Stat(resolvedSrc)
	if err != nil {
		return fmt.Errorf("stat resolved tracked file %q: %w", rel, err)
	}
	if !resolvedInfo.Mode().IsRegular() {
		return nil
	}
	remaining := b.limits.maxBytes - b.bytes
	if resolvedInfo.Size() > remaining {
		return fmt.Errorf(
			"repository snapshot exceeds %d bytes before %q",
			b.limits.maxBytes,
			rel,
		)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("copy tracked file %q: %w", rel, err)
	}
	dst := filepath.Join(b.snapshotRoot, filepath.FromSlash(rel))
	copied, err := copyRegularFile(ctx, resolvedSrc, dst, resolvedInfo.Mode().Perm(), remaining)
	if errors.Is(err, errSnapshotCopyLimit) {
		return fmt.Errorf(
			"repository snapshot exceeds %d bytes while copying %q",
			b.limits.maxBytes,
			rel,
		)
	}
	if err != nil {
		return err
	}
	b.bytes += copied
	b.files++
	return nil
}

func (b *snapshotBuilder) accountDirectories(rel string) error {
	dir := path.Dir(rel)
	if dir == "." {
		return nil
	}
	current := ""
	for _, part := range strings.Split(dir, "/") {
		if current == "" {
			current = part
		} else {
			current = path.Join(current, part)
		}
		if b.directories[current] {
			continue
		}
		b.directories[current] = true
		if len(b.directories) > b.limits.maxDirectories {
			return fmt.Errorf(
				"repository snapshot exceeds %d tracked directories",
				b.limits.maxDirectories,
			)
		}
	}
	return nil
}

func cleanSnapshotScope(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	cleaned := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, item := range raw {
		rel, err := cleanTrackedPath(item)
		if err != nil {
			return nil, fmt.Errorf("invalid snapshot scope: %w", err)
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		cleaned = append(cleaned, rel)
	}
	return cleaned, nil
}

func prepareAffectedModuleManifest(
	ctx context.Context,
	snapshotRoot string,
	parsed parsedDiff,
) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("prepare affected module manifest: %w", err)
	}
	root, err := filepath.Abs(snapshotRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve snapshot root: %w", err)
	}
	modules := make(map[string]bool)
	workspaces := make(map[string]bool)
	for _, target := range repositoryValidationTargets(parsed) {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("prepare affected module manifest: %w", err)
		}
		rel, err := cleanTrackedPath(target.path)
		if err != nil {
			return nil, fmt.Errorf("resolve repository validation target %q: %w", target.path, err)
		}
		switch target.kind {
		case repositoryValidationTargetModule:
			module, err := nearestGoModule(ctx, root, rel, target.requireRegularFile)
			if err != nil {
				return nil, fmt.Errorf("resolve affected module for %q: %w", rel, err)
			}
			modules[module] = true
		case repositoryValidationTargetWorkspace:
			workspace, err := repositoryWorkspaceDirectory(
				root,
				rel,
				target.requireRegularFile,
			)
			if err != nil {
				return nil, fmt.Errorf("resolve affected workspace for %q: %w", rel, err)
			}
			workspaces[workspace] = true
		default:
			return nil, fmt.Errorf("unknown repository validation target kind")
		}
	}
	if len(workspaces) > 0 {
		workspaceModules, err := allSnapshotGoModules(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace modules: %w", err)
		}
		for _, module := range workspaceModules {
			modules[module] = true
		}
	}
	if len(modules) == 0 {
		return nil, fmt.Errorf("no affected Go modules were found")
	}

	ordered := make([]string, 0, len(modules))
	for module := range modules {
		ordered = append(ordered, module)
	}
	sort.Strings(ordered)

	if err := writeReviewPathManifest(
		ctx,
		root,
		reviewModuleManifestName,
		"affected module",
		ordered,
	); err != nil {
		return nil, err
	}
	if len(workspaces) > 0 {
		orderedWorkspaces := make([]string, 0, len(workspaces))
		for workspace := range workspaces {
			orderedWorkspaces = append(orderedWorkspaces, workspace)
		}
		sort.Strings(orderedWorkspaces)
		if err := writeReviewPathManifest(
			ctx,
			root,
			reviewWorkspaceManifestName,
			"workspace",
			orderedWorkspaces,
		); err != nil {
			_ = os.Remove(filepath.Join(root, reviewModuleManifestName))
			return nil, err
		}
	}
	return ordered, nil
}

func repositoryValidationTargets(parsed parsedDiff) []repositoryValidationTarget {
	targets := make([]repositoryValidationTarget, 0, len(parsed.Files))
	targetIndexes := make(map[string]int, len(parsed.Files))
	add := func(candidate string, requireRegularFile bool) {
		kind := repositoryValidationKind(candidate)
		if kind == 0 {
			return
		}
		key := fmt.Sprintf("%d\x00%s", kind, candidate)
		if index, ok := targetIndexes[key]; ok {
			targets[index].requireRegularFile = targets[index].requireRegularFile || requireRegularFile
			return
		}
		targetIndexes[key] = len(targets)
		targets = append(targets, repositoryValidationTarget{
			path:               candidate,
			requireRegularFile: requireRegularFile,
			kind:               kind,
		})
	}

	for _, file := range parsed.Files {
		switch {
		case file.IsDeleted:
			add(file.OldPath, false)
		case file.IsRename:
			add(file.OldPath, false)
			add(file.NewPath, true)
		default:
			add(file.NewPath, true)
		}
	}
	return targets
}

func isGoSourcePath(candidate string) bool {
	return candidate != "" && strings.HasSuffix(candidate, ".go")
}

func isGoRepositoryMetadataPath(candidate string) bool {
	switch path.Base(candidate) {
	case "go.mod", "go.sum", "go.work", "go.work.sum":
		return candidate != ""
	default:
		return false
	}
}

func repositoryValidationKind(candidate string) repositoryValidationTargetKind {
	if isGoSourcePath(candidate) {
		return repositoryValidationTargetModule
	}
	switch path.Base(candidate) {
	case "go.mod", "go.sum":
		return repositoryValidationTargetModule
	case "go.work", "go.work.sum":
		return repositoryValidationTargetWorkspace
	default:
		return 0
	}
}

func repositoryWorkspaceDirectory(
	snapshotRoot string,
	metadataPath string,
	requireRegularFile bool,
) (string, error) {
	canonicalMetadataPath := metadataPath
	if requireRegularFile {
		fullPath := filepath.Join(snapshotRoot, filepath.FromSlash(metadataPath))
		info, err := os.Lstat(fullPath)
		if err != nil {
			return "", fmt.Errorf("stat workspace metadata: %w", err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("workspace metadata is not regular")
		}
		canonicalMetadataPath, err = canonicalSnapshotRelativePath(
			snapshotRoot,
			filepath.FromSlash(metadataPath),
		)
		if err != nil {
			return "", fmt.Errorf("canonicalize workspace metadata: %w", err)
		}
		if !isWorkspaceMetadataPath(canonicalMetadataPath) {
			return "", fmt.Errorf("workspace metadata has an unexpected name")
		}
	}

	workspace := path.Dir(canonicalMetadataPath)
	if workspace == "." {
		return ".", nil
	}
	canonicalWorkspace, err := canonicalSnapshotRelativePath(
		snapshotRoot,
		filepath.FromSlash(workspace),
	)
	if err != nil {
		return "", fmt.Errorf("canonicalize workspace directory: %w", err)
	}
	info, err := os.Lstat(filepath.Join(snapshotRoot, filepath.FromSlash(canonicalWorkspace)))
	if err != nil {
		return "", fmt.Errorf("stat workspace directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace path is not a directory")
	}
	return canonicalWorkspace, nil
}

func isWorkspaceMetadataPath(candidate string) bool {
	switch path.Base(candidate) {
	case "go.work", "go.work.sum":
		return true
	default:
		return false
	}
}

func allSnapshotGoModules(ctx context.Context, snapshotRoot string) ([]string, error) {
	modules := make(map[string]bool)
	err := filepath.WalkDir(snapshotRoot, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("snapshot path %q is a symlink", filepath.ToSlash(current))
		}
		if entry.IsDir() || entry.Name() != "go.mod" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat module file %q: %w", filepath.ToSlash(current), err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("module file %q is not regular", filepath.ToSlash(current))
		}
		relative, err := filepath.Rel(snapshotRoot, current)
		if err != nil {
			return fmt.Errorf("resolve module file path: %w", err)
		}
		canonical, err := canonicalSnapshotRelativePath(snapshotRoot, relative)
		if err != nil {
			return fmt.Errorf("canonicalize module file: %w", err)
		}
		if path.Base(canonical) != "go.mod" {
			return fmt.Errorf("module file %q is not named go.mod", canonical)
		}
		modules[path.Dir(canonical)] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	ordered := make([]string, 0, len(modules))
	for module := range modules {
		ordered = append(ordered, module)
	}
	sort.Strings(ordered)
	return ordered, nil
}

func writeReviewPathManifest(
	ctx context.Context,
	root string,
	name string,
	description string,
	entries []string,
) error {
	manifestPath := filepath.Join(root, name)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("prepare %s manifest: %w", description, err)
	}
	manifest, err := os.OpenFile(manifestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %s manifest: %w", description, err)
	}
	removeOnError := true
	defer func() {
		if removeOnError {
			_ = os.Remove(manifestPath)
		}
	}()
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			_ = manifest.Close()
			return fmt.Errorf("write %s manifest: %w", description, err)
		}
		if _, err := io.WriteString(manifest, entry); err != nil {
			_ = manifest.Close()
			return fmt.Errorf("write %s manifest: %w", description, err)
		}
		if _, err := manifest.Write([]byte{0}); err != nil {
			_ = manifest.Close()
			return fmt.Errorf("write %s manifest delimiter: %w", description, err)
		}
	}
	if err := manifest.Close(); err != nil {
		return fmt.Errorf("close %s manifest: %w", description, err)
	}
	removeOnError = false
	return nil
}

func nearestGoModule(
	ctx context.Context,
	snapshotRoot string,
	changedFile string,
	requireRegularFile bool,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	changedPath := filepath.Join(snapshotRoot, filepath.FromSlash(changedFile))
	if !pathStaysWithin(snapshotRoot, changedPath) {
		return "", fmt.Errorf("changed file escapes the snapshot")
	}
	if requireRegularFile {
		info, err := os.Lstat(changedPath)
		if err != nil {
			return "", fmt.Errorf("stat changed file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("changed file is not regular")
		}
	}

	dir := filepath.Dir(filepath.FromSlash(changedFile))
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		moduleFile := filepath.Join(snapshotRoot, dir, "go.mod")
		moduleInfo, err := os.Lstat(moduleFile)
		switch {
		case err == nil:
			if !moduleInfo.Mode().IsRegular() {
				return "", fmt.Errorf("module file %q is not regular", filepath.ToSlash(moduleFile))
			}
			canonicalModuleFile, err := canonicalSnapshotRelativePath(
				snapshotRoot,
				filepath.Join(dir, "go.mod"),
			)
			if err != nil {
				return "", fmt.Errorf("canonicalize module file: %w", err)
			}
			if path.Base(canonicalModuleFile) != "go.mod" {
				return "", fmt.Errorf("module file %q is not named go.mod", canonicalModuleFile)
			}
			return path.Dir(canonicalModuleFile), nil
		case !os.IsNotExist(err):
			return "", fmt.Errorf("stat module file %q: %w", filepath.ToSlash(moduleFile), err)
		}
		if dir == "." {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == ".." || strings.HasPrefix(parent, ".."+string(filepath.Separator)) {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no go.mod found in snapshot ancestors")
}

func canonicalSnapshotRelativePath(snapshotRoot string, relativePath string) (string, error) {
	clean := filepath.Clean(relativePath)
	if clean == "." || filepath.IsAbs(clean) {
		return "", fmt.Errorf("snapshot path %q must name a relative entry", relativePath)
	}
	components := strings.Split(clean, string(filepath.Separator))
	current := snapshotRoot
	canonical := make([]string, 0, len(components))
	for index, component := range components {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("snapshot path %q contains an invalid component", relativePath)
		}
		requested := filepath.Join(current, component)
		requestedInfo, err := os.Lstat(requested)
		if err != nil {
			return "", fmt.Errorf("stat snapshot path %q: %w", filepath.ToSlash(requested), err)
		}
		if requestedInfo.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("snapshot path %q is a symlink", filepath.ToSlash(requested))
		}
		if index < len(components)-1 && !requestedInfo.IsDir() {
			return "", fmt.Errorf("snapshot path %q is not a directory", filepath.ToSlash(requested))
		}

		entries, err := os.ReadDir(current)
		if err != nil {
			return "", fmt.Errorf("read snapshot directory %q: %w", filepath.ToSlash(current), err)
		}
		actual := ""
		for _, entry := range entries {
			if entry.Name() == component {
				actual = entry.Name()
				break
			}
		}
		if actual == "" {
			for _, entry := range entries {
				entryInfo, err := entry.Info()
				if err != nil {
					return "", fmt.Errorf(
						"stat snapshot directory entry %q: %w",
						filepath.ToSlash(filepath.Join(current, entry.Name())),
						err,
					)
				}
				if os.SameFile(requestedInfo, entryInfo) {
					actual = entry.Name()
					break
				}
			}
		}
		if actual == "" {
			return "", fmt.Errorf("snapshot path component %q could not be canonicalized", component)
		}
		canonical = append(canonical, actual)
		current = filepath.Join(current, actual)
	}
	return filepath.ToSlash(filepath.Join(canonical...)), nil
}

func walkGitTrackedFiles(
	ctx context.Context,
	repoRoot string,
	filesScope []string,
	maxPathBytes int,
	visit func(context.Context, string) error,
) error {
	if maxPathBytes <= 0 {
		return fmt.Errorf("tracked path byte limit must be positive")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := []string{"ls-files", "-z", "--"}
	args = append(args, filesScope...)
	cmd, err := newHardenedGitCommand(runCtx, repoRoot, args...)
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open git ls-files stdout: %w", err)
	}
	var stderr limitBuffer
	stderr.limit = int(maxStderrBytes)
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start git ls-files: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Split(splitNULTerminatedPath)
	scanner.Buffer(make([]byte, maxPathBytes+1), maxPathBytes+1)
	var scanErr error
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		if err := visit(runCtx, scanner.Text()); err != nil {
			scanErr = err
			break
		}
	}
	if scanErr == nil {
		if err := scanner.Err(); err != nil {
			scanErr = fmt.Errorf(
				"scan git ls-files output with %d-byte path limit: %w",
				maxPathBytes,
				err,
			)
		}
	}
	if scanErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("git ls-files canceled: %w", err)
	}
	if scanErr != nil {
		return scanErr
	}
	if waitErr != nil {
		reason := strings.TrimSpace(string(stderr.Bytes()))
		if reason == "" {
			reason = waitErr.Error()
		}
		return fmt.Errorf("git ls-files failed: %s", reason)
	}
	return nil
}

func splitNULTerminatedPath(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) != 0 {
		return 0, nil, errGitPathOutputTerminator
	}
	return 0, nil, nil
}

func cleanTrackedPath(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("tracked path is empty")
	}
	if strings.ContainsRune(raw, '\x00') {
		return "", fmt.Errorf("tracked path contains a NUL byte")
	}
	normalized := strings.ReplaceAll(raw, "\\", "/")
	if path.IsAbs(normalized) || hasWindowsDrive(normalized) {
		return "", fmt.Errorf("tracked path %q must be relative", raw)
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("tracked path %q escapes the repository", raw)
	}
	if clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return "", fmt.Errorf("tracked path %q targets Git metadata", raw)
	}
	return clean, nil
}

func pathStaysWithin(root string, candidate string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absCandidate)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolveExistingPath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err == nil {
		return filepath.Abs(resolved)
	}
	hasSymlink, inspectErr := pathHasSymlinkComponent(absPath)
	if inspectErr != nil || hasSymlink {
		return "", err
	}
	// A component-by-component check can still prove that an existing path has
	// no symlinks after EvalSymlinks fails. Only in that case is absPath used for
	// containment checks; symlinks and inspection failures fail closed above.
	return absPath, nil
}

func pathHasSymlinkComponent(path string) (bool, error) {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
		current = parent
	}
}

func copyRegularFile(
	ctx context.Context,
	src string,
	dst string,
	mode os.FileMode,
	maxBytes int64,
) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, fmt.Errorf("create snapshot directory: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open tracked file %q: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return 0, fmt.Errorf("create snapshot file %q: %w", dst, err)
	}
	copied, copyErr := copyWithContext(ctx, out, in, maxBytes)
	closeErr := out.Close()
	if copyErr != nil {
		return copied, fmt.Errorf("copy tracked file %q: %w", src, copyErr)
	}
	if closeErr != nil {
		return copied, fmt.Errorf("close snapshot file %q: %w", dst, closeErr)
	}
	return copied, nil
}

func copyWithContext(
	ctx context.Context,
	dst io.Writer,
	src io.Reader,
	maxBytes int64,
) (int64, error) {
	if maxBytes < 0 {
		return 0, errSnapshotCopyLimit
	}
	limited := &io.LimitedReader{
		R: contextReader{ctx: ctx, reader: src},
		N: maxBytes + 1,
	}
	// Hide optional ReaderFrom methods so io.CopyBuffer always uses the fixed
	// buffer and the context-aware reader on every copy step.
	copied, err := io.CopyBuffer(
		writerOnly{Writer: dst},
		limited,
		make([]byte, snapshotCopyBufferBytes),
	)
	if err != nil {
		return copied, err
	}
	if copied > maxBytes {
		return copied, errSnapshotCopyLimit
	}
	if err := ctx.Err(); err != nil {
		return copied, err
	}
	return copied, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type writerOnly struct {
	io.Writer
}
