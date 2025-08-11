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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// readFileRequest represents the input for the read file operation.
type readFileRequest struct {
	FileName string `json:"file_name" jsonschema:"description=The relative path from the base directory to read."`
}

// readFileResponse represents the output from the read file operation.
type readFileResponse struct {
	BaseDirectory string `json:"base_directory"`
	FileName      string `json:"file_name"`
	Contents      string `json:"contents"`
	Message       string `json:"message"`
}

// readFile performs the read file operation.
func (f *fileToolSet) readFile(_ context.Context, req readFileRequest) (readFileResponse, error) {
	// Validate the file name.
	if strings.TrimSpace(req.FileName) == "" {
		return readFileResponse{
			BaseDirectory: f.baseDir,
			FileName:      req.FileName,
			Message:       "Error: File name cannot be empty",
		}, fmt.Errorf("file name cannot be empty")
	}
	// Resolve and validate the file path.
	filePath, err := f.resolvePath(req.FileName)
	if err != nil {
		return readFileResponse{
			BaseDirectory: f.baseDir,
			FileName:      req.FileName,
			Message:       fmt.Sprintf("Error: %v", err),
		}, err
	}
	// Check if the target path exists.
	stat, err := os.Stat(filePath)
	if err != nil {
		return readFileResponse{
			BaseDirectory: f.baseDir,
			FileName:      req.FileName,
			Message:       fmt.Sprintf("cannot access file '%s': %v", req.FileName, err),
		}, fmt.Errorf("accessing file '%s': %w", req.FileName, err)
	}
	// Check if the target path is a file.
	if stat.IsDir() {
		return readFileResponse{
			BaseDirectory: f.baseDir,
			FileName:      req.FileName,
			Message:       fmt.Sprintf("Error: target path '%s' is a directory, not a file", req.FileName),
		}, fmt.Errorf("target path '%s' is a directory, not a file", req.FileName)
	}
	// Read the file.
	contents, err := os.ReadFile(filePath)
	if err != nil {
		return readFileResponse{
			BaseDirectory: f.baseDir,
			FileName:      req.FileName,
			Message:       fmt.Sprintf("Error: cannot read file: %v", err),
		}, fmt.Errorf("reading file: %w", err)
	}
	return readFileResponse{
		BaseDirectory: f.baseDir,
		FileName:      req.FileName,
		Contents:      string(contents),
		Message:       fmt.Sprintf("Successfully read %s", req.FileName),
	}, nil
}

// readFileTool returns a callable tool for reading file.
func (f *fileToolSet) readFileTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.readFile,
		function.WithName("read_file"),
		function.WithDescription("Reads the contents of the file 'file_name' and returns the contents if successful. "+
			"The 'file_name' parameter is a relative path from the base directory (e.g., 'subdir/file.txt'). If "+
			"'file_name' points to a directory, returns an error. If 'file_name' points to a file, returns the "+
			"contents of the file. If 'file_name' is empty or not provided, returns an error."),
	)
}
