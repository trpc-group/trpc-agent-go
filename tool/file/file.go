//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package file provides file operation tools for AI agents.
// This tool provides capabilities for saving file, reading file,
// listing file, searching file, and searching content in a specified
// base directory.
package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// defaultBaseDir is the default base directory for file operations.
	defaultBaseDir = "."
	// defaultCreateDirMode is the default permission mode for directory
	// (0755: rwxr-xr-x).
	defaultCreateDirMode = os.FileMode(0755)
	// defaultCreateFileMode is the default permission mode for file
	// (0644: rw-r--r--).
	defaultCreateFileMode = os.FileMode(0644)
	// defaultMaxFileSize is the default maximum file size to read, which is 1MB.
	defaultMaxFileSize = 1024 * 1024
	// missingFileHintMaxEntries limits how many top-level entries are
	// suggested when a requested file is missing.
	missingFileHintMaxEntries = 6
)

const (
	inputsDirName = "inputs"

	missingFileEntriesSeparator = ", "
	missingFileDirectorySuffix  = "/"
	missingFileListToolName     = "list_file"
	missingFileSearchToolName   = "search_file"
	missingFileTopLevelPrefix   = "Top-level entries: "
	missingFileBaseDirPrefix    = "Base directory: "
	relativePathGuidance        = "Use a relative path under " +
		"base_directory. If exec_command wrote an absolute temp " +
		"path, fs_read_file can read it only when that path is " +
		"under a configured read-only root; otherwise have " +
		"exec_command print the needed data directly."
	extraReadRootGuidance = "Absolute paths are readable only when " +
		"they are under a configured read-only root."
	missingFileRecoveryGuidance = "Use " +
		missingFileListToolName + " or " +
		missingFileSearchToolName +
		" to inspect available paths."
	missingFileNoEntriesFallback = "(no visible entries)"
)

// Option is a functional option for configuring the file tool set.
type Option func(*fileToolSet)

// WithBaseDir sets the base directory for file operations, default is
// the current directory.
func WithBaseDir(baseDir string) Option {
	return func(f *fileToolSet) {
		f.baseDir = baseDir
	}
}

// WithSaveFileEnabled enables or disables the save file functionality,
// default is true.
func WithSaveFileEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.saveFileEnabled = e
	}
}

// WithReadFileEnabled enables or disables the read file functionality,
// default is true.
func WithReadFileEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.readFileEnabled = e
	}
}

// WithReadMultipleFilesEnabled enables or disables the read multiple
// files functionality, default is true.
func WithReadMultipleFilesEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.readMultipleFilesEnabled = e
	}
}

// WithListFileEnabled enables or disables the list file functionality,
// default is true.
func WithListFileEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.listFileEnabled = e
	}
}

// WithSearchFileEnabled enables or disables the search file
// functionality, default is true.
func WithSearchFileEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.searchFileEnabled = e
	}
}

// WithSearchContentEnabled enables or disables the search content
// functionality, default is true.
func WithSearchContentEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.searchContentEnabled = e
	}
}

// WithReplaceContentEnabled enables or disables the replace content
// functionality, default is true.
func WithReplaceContentEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.replaceContentEnabled = e
	}
}

// WithCreateDirMode sets the permission mode for creating directory,
// default is 0755 (rwxr-xr-x).
func WithCreateDirMode(m os.FileMode) Option {
	return func(f *fileToolSet) {
		f.createDirMode = m
	}
}

// WithCreateFileMode sets the permission mode for creating file,
// default is 0644 (rw-r--r--).
func WithCreateFileMode(m os.FileMode) Option {
	return func(f *fileToolSet) {
		f.createFileMode = m
	}
}

// WithMaxFileSize sets the maximum file size to read, default is 1MB.
func WithMaxFileSize(s int64) Option {
	return func(f *fileToolSet) {
		f.maxFileSize = s
	}
}

// WithReadOnlyDirs allows read_file and read_multiple_files to read absolute
// paths under the given directories. It does not expand write, replace, list,
// or search permissions.
func WithReadOnlyDirs(dirs ...string) Option {
	return func(f *fileToolSet) {
		f.extraReadRoots = append(f.extraReadRoots, dirs...)
	}
}

// WithName sets the name of the file toolset.
func WithName(name string) Option {
	return func(f *fileToolSet) {
		f.name = name
	}
}

// fileToolSet implements the ToolSet interface for file operations.
type fileToolSet struct {
	baseDir                  string
	extraReadRoots           []string
	hasInputsDir             bool
	saveFileEnabled          bool
	readFileEnabled          bool
	readMultipleFilesEnabled bool
	listFileEnabled          bool
	searchFileEnabled        bool
	searchContentEnabled     bool
	replaceContentEnabled    bool
	createDirMode            os.FileMode
	createFileMode           os.FileMode
	maxFileSize              int64
	tools                    []tool.Tool
	name                     string
}

// Tools implements the ToolSet interface.
func (f *fileToolSet) Tools(ctx context.Context) []tool.Tool {
	return f.tools
}

// Close implements the ToolSet interface.
func (f *fileToolSet) Close() error {
	// No resources to clean up for file tools.
	return nil
}

// Name implements the ToolSet interface.
func (f *fileToolSet) Name() string {
	return f.name
}

// NewToolSet creates a new file operation tool set with the provided
// options.
func NewToolSet(opts ...Option) (tool.ToolSet, error) {
	// Apply default configuration.
	fileToolSet := &fileToolSet{
		baseDir:                  defaultBaseDir,
		saveFileEnabled:          true,
		readFileEnabled:          true,
		readMultipleFilesEnabled: true,
		listFileEnabled:          true,
		searchFileEnabled:        true,
		searchContentEnabled:     true,
		replaceContentEnabled:    true,
		createDirMode:            defaultCreateDirMode,
		createFileMode:           defaultCreateFileMode,
		maxFileSize:              defaultMaxFileSize,
		name:                     "file",
	}
	// Apply user-provided options.
	for _, opt := range opts {
		opt(fileToolSet)
	}
	// Clean the base directory.
	fileToolSet.baseDir = filepath.Clean(fileToolSet.baseDir)
	fileToolSet.extraReadRoots = normalizeExtraReadRoots(
		fileToolSet.extraReadRoots,
	)
	// Check if the base directory exists.
	stat, err := os.Stat(fileToolSet.baseDir)
	if err != nil {
		return nil, fmt.Errorf(
			"base directory '%s' does not exist: %w",
			fileToolSet.baseDir,
			err,
		)
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf(
			"base directory '%s' is not a directory",
			fileToolSet.baseDir,
		)
	}
	if st, err := os.Stat(
		filepath.Join(fileToolSet.baseDir, inputsDirName),
	); err == nil && st.IsDir() {
		fileToolSet.hasInputsDir = true
	}
	// Create function tools based on enabled features.
	var tools []tool.Tool
	if fileToolSet.saveFileEnabled {
		tools = append(tools, fileToolSet.saveFileTool())
	}
	if fileToolSet.readFileEnabled {
		tools = append(tools, fileToolSet.readFileTool())
	}
	if fileToolSet.readMultipleFilesEnabled {
		tools = append(tools, fileToolSet.readMultipleFilesTool())
	}
	if fileToolSet.listFileEnabled {
		tools = append(tools, fileToolSet.listFileTool())
	}
	if fileToolSet.searchFileEnabled {
		tools = append(tools, fileToolSet.searchFileTool())
	}
	if fileToolSet.searchContentEnabled {
		tools = append(tools, fileToolSet.searchContentTool())
	}
	if fileToolSet.replaceContentEnabled {
		tools = append(tools, fileToolSet.replaceContentTool())
	}
	fileToolSet.tools = tools
	return fileToolSet, nil
}

// resolvePath validates a path to prevent directory traversal attacks,
// and resolves a relative path within the base directory.
func (f *fileToolSet) resolvePath(relativePath string) (string, error) {
	reqPath := f.normalizeInputsAlias(relativePath)
	if filepath.IsAbs(reqPath) || strings.Contains(reqPath, "..") {
		return "", fmt.Errorf(
			"invalid path - absolute paths and '..' "+
				"are not allowed: %s. %s",
			relativePath,
			relativePathGuidance,
		)
	}
	return filepath.Join(f.baseDir, reqPath), nil
}

func (f *fileToolSet) resolveReadPath(requestPath string) (string, error) {
	raw := strings.TrimSpace(requestPath)
	if !filepath.IsAbs(raw) {
		return f.resolvePath(requestPath)
	}
	clean := filepath.Clean(raw)
	if _, ok := f.matchExtraReadRoot(clean); ok {
		return clean, nil
	}
	return "", fmt.Errorf(
		"invalid path - absolute path is outside configured "+
			"read-only roots: %s. %s %s",
		requestPath,
		extraReadRootGuidance,
		relativePathGuidance,
	)
}

func (f *fileToolSet) matchExtraReadRoot(absPath string) (string, bool) {
	if f == nil || len(f.extraReadRoots) == 0 {
		return "", false
	}
	candidate := filepath.Clean(absPath)
	evaluatedCandidate, resolvedPath := evalPathWithExistingParent(candidate)
	for _, root := range f.extraReadRoots {
		candidateInRoot := isPathWithinRoot(candidate, root)
		evaluatedInRoot := resolvedPath &&
			isPathWithinRoot(evaluatedCandidate, root)
		if candidateInRoot && (!resolvedPath || evaluatedInRoot) {
			return root, true
		}
		if !candidateInRoot && evaluatedInRoot {
			return root, true
		}
	}
	return "", false
}

func evalPathWithExistingParent(path string) (string, bool) {
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(resolved), true
	}

	current := clean
	var suffix []string
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			parts := append([]string{resolved}, suffix...)
			return filepath.Clean(filepath.Join(parts...)), true
		}
		current = parent
	}
}

func normalizeExtraReadRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, raw := range roots {
		root := strings.TrimSpace(raw)
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = filepath.Clean(resolved)
		}
		if _, err := os.Stat(root); err != nil {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	return out
}

func isPathWithinRoot(candidate string, root string) bool {
	if strings.TrimSpace(candidate) == "" || strings.TrimSpace(root) == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (f *fileToolSet) normalizeInputsAlias(relativePath string) string {
	if f == nil || f.hasInputsDir {
		return strings.TrimSpace(relativePath)
	}
	raw := strings.TrimSpace(relativePath)
	slashed := filepath.ToSlash(raw)
	const prefix = inputsDirName + "/"
	if slashed == inputsDirName {
		return ""
	}
	if strings.HasPrefix(slashed, prefix) {
		return filepath.FromSlash(strings.TrimPrefix(slashed, prefix))
	}
	return raw
}

func (f *fileToolSet) missingFileHint() string {
	if f == nil {
		return ""
	}

	parts := []string{
		missingFileBaseDirPrefix + f.baseDir,
	}
	if entries := f.topLevelEntriesHint(); entries != "" {
		parts = append(
			parts,
			missingFileTopLevelPrefix+entries,
		)
	}
	parts = append(parts, missingFileRecoveryGuidance)
	return strings.Join(parts, ". ")
}

func (f *fileToolSet) topLevelEntriesHint() string {
	if f == nil || strings.TrimSpace(f.baseDir) == "" {
		return ""
	}

	entries, err := os.ReadDir(f.baseDir)
	if err != nil {
		return ""
	}
	if len(entries) == 0 {
		return missingFileNoEntriesFallback
	}

	names := make([]string, 0, missingFileHintMaxEntries)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += missingFileDirectorySuffix
		}
		names = append(names, name)
		if len(names) >= missingFileHintMaxEntries {
			break
		}
	}
	if len(entries) > len(names) {
		names = append(names, "...")
	}
	return strings.Join(names, missingFileEntriesSeparator)
}

// matchFiles matches files with the given pattern in the target path.
// It returns a list of relative paths, filtered out the "", "." and
// ".." paths.
func (f *fileToolSet) matchFiles(
	targetPath string,
	pattern string,
	caseSensitive bool,
) ([]string, error) {
	if pattern == "" {
		return nil, fmt.Errorf("pattern cannot be empty")
	}
	opts := []doublestar.GlobOption{}
	if !caseSensitive {
		opts = append(opts, doublestar.WithCaseInsensitive())
	}
	matches, err := doublestar.Glob(os.DirFS(targetPath), pattern, opts...)
	if err != nil {
		return nil, fmt.Errorf(
			"searching files with pattern '%s': %w",
			pattern,
			err,
		)
	}
	files := matches[:0]
	for _, match := range matches {
		if match == "" || match == "." || match == ".." {
			continue
		}
		files = append(files, match)
	}
	return files, nil
}
