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
func (f *fileToolSet) listFile(_ context.Context, req listFileRequest) (listFileResponse, error) {
	// Resolve the target path.
	targetPath, err := f.resolvePath(req.Path)
	if err != nil {
		return listFileResponse{
			BaseDirectory: f.baseDir,
			Path:          req.Path,
			Message:       fmt.Sprintf("Error: %v", err),
		}, err
	}
	// Check if the target path exists.
	stat, err := os.Stat(targetPath)
	if err != nil {
		return listFileResponse{
			BaseDirectory: f.baseDir,
			Path:          req.Path,
			Message:       fmt.Sprintf("Error: cannot access path '%s': %v", req.Path, err),
		}, fmt.Errorf("accessing path '%s': %w", req.Path, err)
	}
	// If the target is a file, return information about that file.
	if !stat.IsDir() {
		fileName := filepath.Base(targetPath)
		return listFileResponse{
			BaseDirectory: f.baseDir,
			Path:          req.Path,
			Files:         []string{fileName},
			Folders:       []string{},
			Message:       fmt.Sprintf("Found file: %s", fileName),
		}, nil
	}
	// If the target is a directory, list its contents.
	entries, err := os.ReadDir(targetPath)
	if err != nil {
		return listFileResponse{
			BaseDirectory: f.baseDir,
			Path:          req.Path,
			Message:       fmt.Sprintf("Error: cannot read directory '%s': %v", req.Path, err),
		}, fmt.Errorf("reading directory '%s': %w", req.Path, err)
	}
	// Collect files and folders.
	var files []string
	var folders []string
	for _, entry := range entries {
		if entry.IsDir() {
			folders = append(folders, entry.Name())
		} else {
			files = append(files, entry.Name())
		}
	}
	// Create a summary message.
	var message string
	if req.Path == "" {
		message = fmt.Sprintf("Found %d files and %d folders in base directory", len(files), len(folders))
	} else {
		message = fmt.Sprintf("Found %d files and %d folders in %s", len(files), len(folders), req.Path)
	}
	return listFileResponse{
		BaseDirectory: f.baseDir,
		Path:          req.Path,
		Files:         files,
		Folders:       folders,
		Message:       message,
	}, nil
}

// listFileTool returns a callable tool for listing file.
func (f *fileToolSet) listFileTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.listFile,
		function.WithName("list_file"),
		function.WithDescription("Lists files and folders in a directory, or returns information about a specific "+
			"file. The 'path' parameter is a relative path from the base directory (e.g., 'subdir', 'subdir/nested',"+
			" 'file.txt'). If 'path' is empty or not provided, lists the base directory. If 'path' points to a file,"+
			" just returns that file. If 'path' points to a directory, lists the files and folders in the directory."),
	)
}
