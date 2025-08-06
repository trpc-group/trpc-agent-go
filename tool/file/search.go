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

package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// searchFileRequest represents the input for the search file operation.
type searchFileRequest struct {
	Path    string `json:"path" jsonschema:"description=The relative path from the base directory to search in."`
	Pattern string `json:"pattern" jsonschema:"description=The pattern to search for, e.g. '*.md', 'lib*.a', '**/*.go'"`
}

// searchFileResponse represents the output from the search file operation.
type searchFileResponse struct {
	BaseDirectory string   `json:"base_directory"`
	Path          string   `json:"path"`
	Pattern       string   `json:"pattern"`
	Files         []string `json:"files"`
	Folders       []string `json:"folders"`
	Message       string   `json:"message"`
}

// searchFile performs the search file operation.
func (f *fileToolSet) searchFile(_ context.Context, req searchFileRequest) (searchFileResponse, error) {
	// Check if the pattern is empty.
	if strings.TrimSpace(req.Pattern) == "" {
		return searchFileResponse{
			BaseDirectory: f.baseDir,
			Path:          req.Path,
			Pattern:       req.Pattern,
			Message:       "Error: Pattern cannot be empty",
		}, fmt.Errorf("pattern cannot be empty")
	}
	// Resolve and validate the target path.
	targetPath, err := f.resolvePath(req.Path)
	if err != nil {
		return searchFileResponse{
			BaseDirectory: f.baseDir,
			Path:          req.Path,
			Pattern:       req.Pattern,
			Message:       fmt.Sprintf("Error: %v", err),
		}, err
	}
	// Check if the target path exists.
	stat, err := os.Stat(targetPath)
	if err != nil {
		return searchFileResponse{
			BaseDirectory: f.baseDir,
			Path:          req.Path,
			Pattern:       req.Pattern,
			Message:       fmt.Sprintf("Error: cannot access path '%s': %v", req.Path, err),
		}, fmt.Errorf("accessing path '%s': %w", req.Path, err)
	}
	// Check if the target path is a file.
	if !stat.IsDir() {
		// Check if the target path matches the pattern.
		ok, err := doublestar.PathMatch(req.Pattern, filepath.Base(targetPath))
		if err != nil {
			return searchFileResponse{
				BaseDirectory: f.baseDir,
				Path:          req.Path,
				Pattern:       req.Pattern,
				Message:       fmt.Sprintf("Error: searching files with pattern '%s': %v", req.Pattern, err),
			}, fmt.Errorf("searching files with pattern '%s': %w", req.Pattern, err)
		}
		if !ok {
			return searchFileResponse{
				BaseDirectory: f.baseDir,
				Path:          req.Path,
				Pattern:       req.Pattern,
				Message:       fmt.Sprintf("No files found matching pattern '%s' in path '%s'", req.Pattern, req.Path),
			}, nil
		}
		return searchFileResponse{
			BaseDirectory: f.baseDir,
			Path:          req.Path,
			Pattern:       req.Pattern,
			Files:         []string{req.Path},
			Folders:       []string{},
			Message:       fmt.Sprintf("Found file: %s", req.Path),
		}, nil
	}
	// Use doublestar for all patterns.
	// It supports both recursive and non-recursive.
	matches, err := doublestar.Glob(os.DirFS(targetPath), req.Pattern)
	if err != nil {
		return searchFileResponse{
			BaseDirectory: f.baseDir,
			Path:          req.Path,
			Pattern:       req.Pattern,
			Message:       fmt.Sprintf("Error: searching files with pattern '%s': %v", req.Pattern, err),
		}, fmt.Errorf("searching files with pattern '%s': %w", req.Pattern, err)
	}
	// Separate files and folders.
	var files []string
	var folders []string
	for _, match := range matches {
		if match == "." || match == ".." {
			continue
		}
		fullPath := filepath.Join(targetPath, match)
		stat, err := os.Stat(fullPath)
		if err != nil {
			// Skip entries that can't be stat.
			continue
		}
		relativePath := filepath.Join(req.Path, match)
		if stat.IsDir() {
			folders = append(folders, relativePath)
		} else {
			files = append(files, relativePath)
		}
	}
	message := fmt.Sprintf("Found %d files and %d folders matching pattern '%s' in %s",
		len(files), len(folders), req.Pattern, targetPath)
	return searchFileResponse{
		BaseDirectory: f.baseDir,
		Path:          req.Path,
		Pattern:       req.Pattern,
		Files:         files,
		Folders:       folders,
		Message:       message,
	}, nil
}

// searchFileTool returns a callable tool for searching file.
func (f *fileToolSet) searchFileTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.searchFile,
		function.WithName("search_file"),
		function.WithDescription("Searches for files and folders matching the given pattern in a specified directory, "+
			"and returns separate lists for files and folders. "+
			"The 'path' parameter is a relative path from the base directory to search in "+
			"(e.g., 'subdir', 'subdir/nested'). If 'path' is empty or not provided, searches in the base directory. "+
			"Supports both recursive ('**') and non-recursive ('*') glob patterns. "+
			"Pattern examples: '*.txt' (all txt files), 'file*.csv' (csv files starting with 'file'), "+
			"'subdir/*.go' (go files in subdir), '**/*.go' (all go files recursively), '*data*' (filename or "+
			"directory containing 'data'). If the pattern is empty or not provided, returns an error."),
	)
}
