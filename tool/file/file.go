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
)

const inputsDirName = "inputs"

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

// WithName sets the name of the file toolset.
func WithName(name string) Option {
	return func(f *fileToolSet) {
		f.name = name
	}
}

// fileToolSet implements the ToolSet interface for file operations.
type fileToolSet struct {
	baseDir                  string
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
				"are not allowed: %s",
			relativePath,
		)
	}
	return filepath.Join(f.baseDir, reqPath), nil
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
