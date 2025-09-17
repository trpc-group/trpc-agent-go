//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// searchFileRequest represents the input for the search file operation.
type searchFileRequest struct {
	Path          string `json:"path" jsonschema:"description=The relative path from the base directory to search in."`
	Pattern       string `json:"pattern" jsonschema:"description=The pattern to search for."`
	CaseSensitive bool   `json:"case_sensitive" jsonschema:"description=Whether pattern matching should be case sensitive."`
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
func (f *fileToolSet) searchFile(_ context.Context, req *searchFileRequest) (*searchFileResponse, error) {
	rsp := &searchFileResponse{
		BaseDirectory: f.baseDir,
		Path:          req.Path,
		Pattern:       req.Pattern,
	}
	// Validate pattern
	if req.Pattern == "" {
		rsp.Message = "Error: pattern cannot be empty"
		return rsp, fmt.Errorf("pattern cannot be empty")
	}
	// Resolve and validate the target path.
	targetPath, err := f.resolvePath(req.Path)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	// Check if the target path exists.
	stat, err := os.Stat(targetPath)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: cannot access path '%s': %v", req.Path, err)
		return rsp, fmt.Errorf("accessing path '%s': %w", req.Path, err)
	}
	// Check if the target path is a file.
	if !stat.IsDir() {
		rsp.Message = fmt.Sprintf("Error: target path '%s' is a file, not a directory", req.Path)
		return rsp, fmt.Errorf("target path '%s' is a file, not a directory", req.Path)
	}
	// Find files matching the pattern.
	matches, err := f.matchFiles(targetPath, req.Pattern, req.CaseSensitive)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	// Separate files and folders.
	for _, match := range matches {
		fullPath := filepath.Join(targetPath, match)
		stat, err := os.Stat(fullPath)
		if err != nil {
			// Skip entries that can't be stat.
			continue
		}
		relativePath := filepath.Join(req.Path, match)
		if stat.IsDir() {
			rsp.Folders = append(rsp.Folders, relativePath)
		} else {
			rsp.Files = append(rsp.Files, relativePath)
		}
	}
	rsp.Message = fmt.Sprintf("Found %d files and %d folders matching pattern '%s' in %s",
		len(rsp.Files), len(rsp.Folders), req.Pattern, targetPath)
	return rsp, nil
}

// searchFileTool returns a callable tool for searching file.
func (f *fileToolSet) searchFileTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.searchFile,
		function.WithName("search_file"),
		function.WithDescription("Searches for files and folders matching the given pattern in a specified directory, "+
			"and returns separate lists for files and folders. "+
			"The 'path' parameter specifies the directory to search in, relative to the base directory "+
			"(e.g., 'subdir', 'subdir/nested'). "+
			"If 'path' is empty or not provided, searches in the base directory. "+
			"If 'path' points to a file instead of a directory, returns an error. "+
			"Supports both recursive ('**') and non-recursive ('*') glob patterns. "+
			"The 'case_sensitive' parameter controls whether pattern matching is case sensitive, false by default. "+
			"Pattern examples: '*.txt' (all txt files), 'file*.csv' (csv files starting with 'file'), "+
			"'subdir/*.go' (go files in subdir), '**/*.go' (all go files recursively), '*data*' (filename or "+
			"directory containing 'data'). If the pattern is empty or not provided, returns an error."),
	)
}
