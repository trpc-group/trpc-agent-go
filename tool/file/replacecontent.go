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

// replaceContentRequest represents the input for the replace content operation.
type replaceContentRequest struct {
	FileName        string `json:"file_name" jsonschema:"description=The relative path from the base directory to replace."`
	OldString       string `json:"old_string" jsonschema:"description=The old string to be replaced."`
	NewString       string `json:"new_string" jsonschema:"description=The new string to replace with."`
	NumReplacements int    `json:"num_replacements,omitempty" jsonschema:"description=The maximum number of replacements to perform."`
}

// replaceContentResponse represents the output from the replace content operation.
type replaceContentResponse struct {
	BaseDirectory string `json:"base_directory"`
	FileName      string `json:"file_name"`
	Message       string `json:"message"`
}

// replaceContent performs the replace content operation.
func (f *fileToolSet) replaceContent(_ context.Context, req *replaceContentRequest) (*replaceContentResponse, error) {
	rsp := &replaceContentResponse{
		BaseDirectory: f.baseDir,
		FileName:      req.FileName,
	}
	// Validate old string.
	if req.OldString == "" {
		rsp.Message = "Error: old_string cannot be empty"
		return rsp, fmt.Errorf("old_string cannot be empty")
	}
	if req.OldString == req.NewString {
		rsp.Message = "old_string and new_string are the same, no replacements will be made"
		return rsp, nil
	}
	// Resolve path and ensure it's a regular file.
	filePath, err := f.resolvePath(req.FileName)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	st, err := os.Stat(filePath)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: cannot access file '%s': %v", req.FileName, err)
		return rsp, fmt.Errorf("accessing file '%s': %w", req.FileName, err)
	}
	if st.IsDir() {
		rsp.Message = fmt.Sprintf("Error: '%s' is a directory, not a file", req.FileName)
		return rsp, fmt.Errorf("target path '%s' is a directory", req.FileName)
	}
	// Read file.
	data, err := os.ReadFile(filePath)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: reading file '%s': %v", req.FileName, err)
		return rsp, fmt.Errorf("reading file '%s': %w", req.FileName, err)
	}
	content := string(data)
	// Check if old string is found.
	totalCount := strings.Count(content, req.OldString)
	if totalCount == 0 {
		rsp.Message = fmt.Sprintf("'%s' not found in '%s'", req.OldString, req.FileName)
		return rsp, nil
	}
	// Calculate number of replacements.
	numReplacements := req.NumReplacements
	if numReplacements == 0 {
		numReplacements = 1
	}
	if numReplacements < 0 || numReplacements > totalCount {
		numReplacements = totalCount
	}
	// Replace old string with new string.
	newContent := strings.Replace(content, req.OldString, req.NewString, numReplacements)
	// Write back preserving permissions.
	err = os.WriteFile(filePath, []byte(newContent), st.Mode())
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: writing file '%s': %v", req.FileName, err)
		return rsp, fmt.Errorf("writing file '%s': %w", req.FileName, err)
	}
	rsp.Message = fmt.Sprintf("Successfully replaced %d of %d occurrence(s) in '%s'",
		numReplacements, totalCount, req.FileName)
	return rsp, nil
}

// replaceContentTool returns a callable tool for replacing content in a file.
func (f *fileToolSet) replaceContentTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.replaceContent,
		function.WithName("replace_content"),
		function.WithDescription("Replace a specific string in a file to a new string. The tool is designed for "+
			"precise, targeted modifications and requires sufficient context around old_string to ensure the correct "+
			"location is modified. "+
			"The 'file_name' parameter is a relative path from the base directory (e.g., 'subdir/file.txt'). "+
			"The 'old_string' parameter is the old string to be replaced, support multi-line. "+
			"The 'new_string' parameter is the new string to replace with, support multi-line. "+
			"The 'num_replacements' parameter is the maximum number of replacements to perform, "+
			"defaults to 1. If 'num_replacements' is negative, replace all occurrences."),
	)
}
