//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package file provides file operation tools for AI agents.
// This tool provides capabilities for saving file, reading file, listing file,
// searching file, and searching content in a specified base directory.
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
	// defaultCreateDirMode is the default permission mode for directory (0755: rwxr-xr-x).
	defaultCreateDirMode = os.FileMode(0755)
	// defaultCreateFileMode is the default permission mode for file (0644: rw-r--r--).
	defaultCreateFileMode = os.FileMode(0644)
	// defaultMaxFileSize is the default maximum file size to read, which is 1MB.
	defaultMaxFileSize = 1024 * 1024
)

// Option is a functional option for configuring the file tool set.
type Option func(*fileToolSet)

// WithBaseDir sets the base directory for file operations, default is the current directory.
func WithBaseDir(baseDir string) Option {
	return func(f *fileToolSet) {
		f.baseDir = baseDir
	}
}

// WithSaveFileEnabled enables or disables the save file functionality, default is true.
func WithSaveFileEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.saveFileEnabled = e
	}
}

// WithReadFileEnabled enables or disables the read file functionality, default is true.
func WithReadFileEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.readFileEnabled = e
	}
}

// WithListFileEnabled enables or disables the list file functionality, default is true.
func WithListFileEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.listFileEnabled = e
	}
}

// WithSearchFileEnabled enables or disables the search file functionality, default is true.
func WithSearchFileEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.searchFileEnabled = e
	}
}

// WithSearchContentEnabled enables or disables the search content functionality, default is true.
func WithSearchContentEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.searchContentEnabled = e
	}
}

// WithReplaceContentEnabled enables or disables the replace content functionality, default is true.
func WithReplaceContentEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.replaceContentEnabled = e
	}
}

// WithCreateDirMode sets the permission mode for creating directory, default is 0755 (rwxr-xr-x).
func WithCreateDirMode(m os.FileMode) Option {
	return func(f *fileToolSet) {
		f.createDirMode = m
	}
}

// WithCreateFileMode sets the permission mode for creating file, default is 0644 (rw-r--r--).
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

// fileToolSet implements the ToolSet interface for file operations.
type fileToolSet struct {
	baseDir               string
	saveFileEnabled       bool
	readFileEnabled       bool
	listFileEnabled       bool
	searchFileEnabled     bool
	searchContentEnabled  bool
	replaceContentEnabled bool
	createDirMode         os.FileMode
	createFileMode        os.FileMode
	maxFileSize           int64
	tools                 []tool.CallableTool
}

// Tools implements the ToolSet interface.
func (f *fileToolSet) Tools(ctx context.Context) []tool.CallableTool {
	return f.tools
}

// Close implements the ToolSet interface.
func (f *fileToolSet) Close() error {
	// No resources to clean up for file tools.
	return nil
}

// NewToolSet creates a new file operation tool set with the provided options.
func NewToolSet(opts ...Option) (tool.ToolSet, error) {
	// Apply default configuration.
	fileToolSet := &fileToolSet{
		baseDir:               defaultBaseDir,
		saveFileEnabled:       true,
		readFileEnabled:       true,
		listFileEnabled:       true,
		searchFileEnabled:     true,
		searchContentEnabled:  true,
		replaceContentEnabled: true,
		createDirMode:         defaultCreateDirMode,
		createFileMode:        defaultCreateFileMode,
		maxFileSize:           defaultMaxFileSize,
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
		return nil, fmt.Errorf("base directory '%s' does not exist: %w", fileToolSet.baseDir, err)
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("base directory '%s' is not a directory", fileToolSet.baseDir)
	}
	// Create function tools based on enabled features.
	var tools []tool.CallableTool
	if fileToolSet.saveFileEnabled {
		tools = append(tools, fileToolSet.saveFileTool())
	}
	if fileToolSet.readFileEnabled {
		tools = append(tools, fileToolSet.readFileTool())
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
	if filepath.IsAbs(relativePath) || strings.Contains(relativePath, "..") {
		return "", fmt.Errorf("invalid path - absolute paths and '..' are not allowed: %s", relativePath)
	}
	return filepath.Join(f.baseDir, relativePath), nil
}

func (f *fileToolSet) matchFiles(targetPath string, pattern string, caseSensitive bool) ([]string, error) {
	// First validate the pattern by trying to match it (this will catch invalid patterns).
	_, err := doublestar.Match(pattern, "test")
	if err != nil {
		return nil, fmt.Errorf("invalid pattern '%s': %w", pattern, err)
	}

	// For case-sensitive matching, use doublestar directly.
	if caseSensitive {
		files, err := doublestar.Glob(os.DirFS(targetPath), pattern)
		if err != nil {
			return nil, fmt.Errorf("searching files with pattern '%s': %w", pattern, err)
		}
		return files, nil
	}

	// For case-insensitive matching, we need to implement it manually
	// since doublestar v4.6.1 doesn't support case-insensitive matching.
	
	// Get all possible files using a broad pattern, then filter manually.
	allFiles, err := doublestar.Glob(os.DirFS(targetPath), "**")
	if err != nil {
		return nil, fmt.Errorf("searching files: %w", err)
	}
	
	// Also get directories if the pattern might match them.
	allDirs, err := doublestar.Glob(os.DirFS(targetPath), "**/")
	if err != nil {
		return nil, fmt.Errorf("searching directories: %w", err)
	}
	
	// Combine files and directories.
	allPaths := append(allFiles, allDirs...)
	
	var matches []string
	lowerPattern := strings.ToLower(pattern)
	
	for _, path := range allPaths {
		lowerPath := strings.ToLower(path)
		// For directory patterns, we need to add the trailing slash for matching.
		testPath := lowerPath
		if strings.HasSuffix(lowerPattern, "/") && !strings.HasSuffix(testPath, "/") {
			// Check if this path is a directory by looking in the original allDirs slice.
			isDir := false
			for _, dir := range allDirs {
				if strings.ToLower(dir) == lowerPath {
					isDir = true
					break
				}
			}
			if isDir {
				testPath += "/"
			}
		}
		
		// Use doublestar.Match for case-insensitive pattern matching.
		matched, err := doublestar.Match(lowerPattern, testPath)
		if err != nil {
			continue // Skip paths with match errors.
		}
		if matched {
			matches = append(matches, path)
		}
	}
	
	// Remove duplicates.
	seen := make(map[string]bool)
	var uniqueMatches []string
	for _, match := range matches {
		if !seen[match] {
			seen[match] = true
			uniqueMatches = append(uniqueMatches, match)
		}
	}
	
	return uniqueMatches, nil
}
