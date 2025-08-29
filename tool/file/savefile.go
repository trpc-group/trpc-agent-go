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

// saveFileRequest represents the input for the save file operation.
type saveFileRequest struct {
	FileName  string `json:"file_name" jsonschema:"description=The relative filepath from the base directory to save."`
	Contents  string `json:"contents" jsonschema:"description=The contents to save to the file."`
	Overwrite bool   `json:"overwrite" jsonschema:"description=Whether to overwrite the file if it already exists."`
}

// saveFileResponse represents the output from the save file operation.
type saveFileResponse struct {
	BaseDirectory string `json:"base_directory"`
	FileName      string `json:"file_name"`
	Message       string `json:"message"`
}

// saveFile performs the save file operation.
func (f *fileToolSet) saveFile(_ context.Context, req *saveFileRequest) (*saveFileResponse, error) {
	rsp := &saveFileResponse{
		BaseDirectory: f.baseDir,
		FileName:      req.FileName,
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
			rsp.Message = fmt.Sprintf("Error: file %s already exists and overwrite is disabled", req.FileName)
			return rsp, fmt.Errorf("file %s already exists and overwrite is disabled", req.FileName)
		}
	}
	// Write the file.
	if err := os.WriteFile(filePath, []byte(req.Contents), f.createFileMode); err != nil {
		rsp.Message = fmt.Sprintf("Error: cannot write to file '%s': %v", req.FileName, err)
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
		function.WithDescription("Saves the contents to a file called 'file_name' and returns the file name if "+
			"successful. Use this tool to create or update file. The 'file_name' parameter is a relative path "+
			"from the base directory (e.g., 'subdir/file.txt'). If 'file_name' is empty or not provided, "+
			"returns an error. If 'overwrite' is true, the file will be overwritten if it already exists. "+
			"If 'overwrite' is false, the file will not be overwritten if it already exists and returns an error. "+
			"The 'contents' parameter is the contents to save to the file."),
	)
}
