//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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

	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// saveFileRequest represents the input for the save file operation.
type saveFileRequest struct {
	// FileName is a path relative to base_directory.
	FileName string `json:"file_name"`
	// Contents is the file content to write.
	Contents string `json:"contents"`
	// Overwrite controls whether an existing file is replaced.
	Overwrite bool `json:"overwrite"`
}

// saveFileResponse represents the output from the save file operation.
type saveFileResponse struct {
	BaseDirectory string `json:"base_directory"`
	FileName      string `json:"file_name"`
	Message       string `json:"message"`
}

// saveFile performs the save file operation.
func (f *fileToolSet) saveFile(
	_ context.Context,
	req *saveFileRequest,
) (*saveFileResponse, error) {
	rsp := &saveFileResponse{
		BaseDirectory: f.baseDir,
		FileName:      req.FileName,
	}
	ref, err := fileref.Parse(req.FileName)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	if ref.Scheme != "" {
		rsp.Message = fmt.Sprintf(
			"Error: save_file does not support %s:// refs",
			ref.Scheme,
		)
		return rsp, fmt.Errorf(
			"save_file does not support %s:// refs",
			ref.Scheme,
		)
	}
	// Resolve and validate the file path.
	filePath, err := f.resolvePath(req.FileName)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	// Create parent directories if they don't exist.
	parentDir := filepath.Dir(filePath)
	if err := os.MkdirAll(parentDir, f.createDirMode); err != nil {
		rsp.Message = fmt.Sprintf("Error: cannot create directory: %v", err)
		return rsp, fmt.Errorf("error creating directory: %w", err)
	}
	// Check if file exists and overwrite is disabled.
	if !req.Overwrite {
		if _, err := os.Stat(filePath); err == nil {
			rsp.Message = fmt.Sprintf(
				"Error: file exists and overwrite=false: %s",
				req.FileName,
			)
			return rsp, fmt.Errorf(
				"file exists and overwrite=false: %s",
				req.FileName,
			)
		}
	}
	// Write the file.
	if err := os.WriteFile(
		filePath,
		[]byte(req.Contents),
		f.createFileMode,
	); err != nil {
		rsp.Message = fmt.Sprintf(
			"Error: cannot write to file '%s': %v",
			req.FileName,
			err,
		)
		return rsp, fmt.Errorf("writing to file '%s': %w", req.FileName, err)
	}
	rsp.Message = fmt.Sprintf("Successfully saved: %s", req.FileName)
	return rsp, nil
}

// saveFileTool returns a callable tool for saving file.
func (f *fileToolSet) saveFileTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.saveFile,
		function.WithName("save_file"),
		function.WithDescription(
			"Write a text file under base_directory (optional overwrite).",
		),
	)
}
