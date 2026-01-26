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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// replaceContentRequest represents the input for the replace content
// operation.
type replaceContentRequest struct {
	FileName string `json:"file_name"`
	// OldString is replaced by NewString. It can be multi-line.
	OldString string `json:"old_string"`
	// NewString is inserted in place of OldString. It can be multi-line.
	NewString string `json:"new_string"`
	// NumReplacements limits replacements (default 1). Negative means all.
	NumReplacements int `json:"num_replacements,omitempty"`
}

// replaceContentResponse represents the output from the replace content
// operation.
type replaceContentResponse struct {
	BaseDirectory string `json:"base_directory"`
	FileName      string `json:"file_name"`
	Message       string `json:"message"`
}

// replaceContent performs the replace content operation.
func (f *fileToolSet) replaceContent(
	_ context.Context,
	req *replaceContentRequest,
) (*replaceContentResponse, error) {
	rsp := &replaceContentResponse{
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
			"Error: replace_content does not support %s:// refs",
			ref.Scheme,
		)
		return rsp, fmt.Errorf(
			"replace_content does not support %s:// refs",
			ref.Scheme,
		)
	}
	// Validate old string.
	if req.OldString == "" {
		rsp.Message = "Error: old_string cannot be empty"
		return rsp, fmt.Errorf("old_string cannot be empty")
	}
	if req.OldString == req.NewString {
		rsp.Message = "old_string equals new_string; no changes made"
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
		rsp.Message = fmt.Sprintf(
			"Error: cannot access file '%s': %v",
			req.FileName,
			err,
		)
		return rsp, fmt.Errorf("accessing file '%s': %w", req.FileName, err)
	}
	if st.IsDir() {
		rsp.Message = fmt.Sprintf(
			"Error: '%s' is a directory, not a file",
			req.FileName,
		)
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
		rsp.Message = fmt.Sprintf(
			"'%s' not found in '%s'",
			req.OldString,
			req.FileName,
		)
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
	newContent := strings.Replace(
		content,
		req.OldString,
		req.NewString,
		numReplacements,
	)
	// Write back preserving permissions.
	err = os.WriteFile(filePath, []byte(newContent), st.Mode())
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: writing file '%s': %v", req.FileName, err)
		return rsp, fmt.Errorf("writing file '%s': %w", req.FileName, err)
	}
	rsp.Message = fmt.Sprintf(
		"Successfully replaced %d of %d in '%s'",
		numReplacements,
		totalCount,
		req.FileName,
	)
	return rsp, nil
}

// replaceContentTool returns a callable tool for replacing content in a file.
func (f *fileToolSet) replaceContentTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.replaceContent,
		function.WithName("replace_content"),
		function.WithDescription(
			"Replace a string in a file under base_directory. "+
				"Supports multi-line old_string/new_string.",
		),
	)
}
