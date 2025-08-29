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

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// listFileRequest represents the input for the list file operation.
type listFileRequest struct {
	Path string `json:"path" jsonschema:"description=The relative path from the base directory to list."`
}

// listFileResponse represents the output from the list file operation.
type listFileResponse struct {
	BaseDirectory string   `json:"base_directory"`
	Path          string   `json:"path"`
	Files         []string `json:"files"`
	Folders       []string `json:"folders"`
	Message       string   `json:"message"`
}

// listFile performs the list file operation.
func (f *fileToolSet) listFile(_ context.Context, req *listFileRequest) (*listFileResponse, error) {
	rsp := &listFileResponse{
		BaseDirectory: f.baseDir,
		Path:          req.Path,
	}
	// Resolve the target path.
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
	// If the target is a file, return information about that file.
	if !stat.IsDir() {
		rsp.Message = fmt.Sprintf("Error: path '%s' is a file, not a directory", req.Path)
		return rsp, fmt.Errorf("path '%s' is a file, not a directory", req.Path)
	}
	// If the target is a directory, list its contents.
	entries, err := os.ReadDir(targetPath)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: cannot read directory '%s': %v", req.Path, err)
		return rsp, fmt.Errorf("reading directory '%s': %w", req.Path, err)
	}
	// Collect files and folders.
	for _, entry := range entries {
		if entry.IsDir() {
			rsp.Folders = append(rsp.Folders, entry.Name())
		} else {
			rsp.Files = append(rsp.Files, entry.Name())
		}
	}
	// Create a summary message.
	if req.Path == "" {
		rsp.Message = fmt.Sprintf("Found %d files and %d folders in base directory", len(rsp.Files), len(rsp.Folders))
	} else {
		rsp.Message = fmt.Sprintf("Found %d files and %d folders in %s", len(rsp.Files), len(rsp.Folders), req.Path)
	}
	return rsp, nil
}

// listFileTool returns a callable tool for listing file.
func (f *fileToolSet) listFileTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.listFile,
		function.WithName("list_file"),
		function.WithDescription("Lists files and folders in a directory. "+
			"The 'path' parameter is a relative path from the base directory (e.g., 'subdir', 'subdir/nested'). "+
			"If 'path' is empty or not provided, lists the base directory. "+
			"If 'path' points to a directory, lists the files and folders in the directory. "+
			"If 'path' points to a file, returns an error."),
	)
}
