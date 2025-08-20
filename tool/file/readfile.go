//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
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
	FileName  string `json:"file_name" jsonschema:"description=The relative path from the base directory to read."`
	StartLine *int   `json:"start_line,omitempty" jsonschema:"description=The line number to start reading from."`
	NumLines  *int   `json:"num_lines,omitempty" jsonschema:"description=The maximum number of lines to read."`
}

// readFileResponse represents the output from the read file operation.
type readFileResponse struct {
	BaseDirectory string `json:"base_directory"`
	FileName      string `json:"file_name"`
	Contents      string `json:"contents"`
	Message       string `json:"message"`
}

// readFile performs the read file operation.
func (f *fileToolSet) readFile(_ context.Context, req *readFileRequest) (*readFileResponse, error) {
	rsp := &readFileResponse{
		BaseDirectory: f.baseDir,
		FileName:      req.FileName,
	}
	// Validate the start line and number of lines.
	if req.StartLine != nil && *req.StartLine <= 0 {
		rsp.Message = fmt.Sprintf("Error: start line must be greater than 0, which is %v", *req.StartLine)
		return rsp, fmt.Errorf("start line must be greater than 0, which is %v", *req.StartLine)
	}
	if req.NumLines != nil && *req.NumLines <= 0 {
		rsp.Message = fmt.Sprintf("Error: number of lines must be greater than 0, which is %v", *req.NumLines)
		return rsp, fmt.Errorf("number of lines must be greater than 0, which is %v", *req.NumLines)
	}
	// Resolve and validate the file path.
	filePath, err := f.resolvePath(req.FileName)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	// Check if the target path exists.
	stat, err := os.Stat(filePath)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: cannot access file '%s': %v", req.FileName, err)
		return rsp, fmt.Errorf("accessing file '%s': %w", req.FileName, err)
	}
	// Check if the target path is a file.
	if stat.IsDir() {
		rsp.Message = fmt.Sprintf("Error: target path '%s' is a directory, not a file", req.FileName)
		return rsp, fmt.Errorf("target path '%s' is a directory, not a file", req.FileName)
	}
	if stat.Size() > f.maxFileSize {
		rsp.Message = fmt.Sprintf("Error: file size is beyond of max file size, file size: %d, max file size: %d",
			stat.Size(), f.maxFileSize)
		return rsp, fmt.Errorf("file size is beyond of max file size, file size: %d, max file size: %d",
			stat.Size(), f.maxFileSize)
	}
	// Read the file.
	contents, err := os.ReadFile(filePath)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: cannot read file: %v", err)
		return rsp, fmt.Errorf("reading file: %w", err)
	}
	if len(contents) == 0 {
		rsp.Message = fmt.Sprintf("Successfully read %s, but file is empty", req.FileName)
		rsp.Contents = ""
		return rsp, nil
	}
	// Split the file contents into lines.
	lines := strings.Split(string(contents), "\n")
	totalLines := len(lines)
	// Set default values for offset and limit.
	startLine := 1
	numLines := totalLines
	if req.StartLine != nil {
		startLine = *req.StartLine
	}
	if req.NumLines != nil {
		numLines = *req.NumLines
	}
	// Validate offset.
	if startLine > totalLines {
		rsp.Message = fmt.Sprintf("Error: start line is out of range, start line: %d, total lines: %d", startLine, totalLines)
		return rsp, fmt.Errorf("start line is out of range, start line: %d, total lines: %d", startLine, totalLines)
	}
	// Calculate end line.
	endLine := startLine + numLines - 1
	if endLine > totalLines {
		endLine = totalLines
	}
	// Extract the requested lines.
	rsp.Contents = strings.Join(lines[startLine-1:endLine], "\n")
	rsp.Message = fmt.Sprintf("Successfully read %s, start line: %d, end line: %d, total lines: %d",
		rsp.FileName, startLine, endLine, totalLines)
	return rsp, nil
}

// readFileTool returns a callable tool for reading file.
func (f *fileToolSet) readFileTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.readFile,
		function.WithName("read_file"),
		function.WithDescription("Reads the contents of the file 'file_name' and returns the contents if successful. "+
			"The 'file_name' parameter is a relative path from the base directory (e.g., 'subdir/file.txt'). If "+
			"'file_name' points to a directory, returns an error. If 'file_name' points to a file, returns the "+
			"contents of the file. If 'file_name' is empty or not provided, returns an error. "+
			"Optional 'start_line' parameter specifies the line number to start reading from. "+
			"Optional 'num_lines' parameter specifies the maximum number of lines to read. "+
			"If start_line or num_lines are not specified, reads the entire file. "+
			"If start_line is not greater than 0 or out of range, returns an error. "+
			"If num_lines is not greater than 0, returns an error."),
	)
}
