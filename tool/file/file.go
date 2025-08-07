//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package file provides file operation tools for AI agents.
// This tool provides capabilities for saving file, reading file, listing file,
// and searching file in a specified base directory.
package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// defaultBaseDir is the default base directory for file operations.
	defaultBaseDir = "."
	// defaultCreateDirMode is the default permission mode for directory (0755: rwxr-xr-x).
	defaultCreateDirMode = os.FileMode(0755)
	// defaultCreateFileMode is the default permission mode for file (0644: rw-r--r--).
	defaultCreateFileMode = os.FileMode(0644)
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

// fileToolSet implements the ToolSet interface for file operations.
type fileToolSet struct {
	baseDir           string
	saveFileEnabled   bool
	readFileEnabled   bool
	listFileEnabled   bool
	searchFileEnabled bool
	createDirMode     os.FileMode
	createFileMode    os.FileMode
	tools             []tool.CallableTool
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
		baseDir:           defaultBaseDir,
		saveFileEnabled:   true,
		readFileEnabled:   true,
		listFileEnabled:   true,
		searchFileEnabled: true,
		createDirMode:     defaultCreateDirMode,
		createFileMode:    defaultCreateFileMode,
	}
	// Apply user-provided options.
	for _, opt := range opts {
		opt(fileToolSet)
	}
	// Clean the base directory.
	fileToolSet.baseDir = filepath.Clean(fileToolSet.baseDir)
	// Check if the base directory exists.
	if _, err := os.Stat(fileToolSet.baseDir); err != nil {
		return nil, fmt.Errorf("base directory '%s' does not exist: %w", fileToolSet.baseDir, err)
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
