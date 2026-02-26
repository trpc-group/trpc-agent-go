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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// searchFileRequest represents the input for the search file operation.
type searchFileRequest struct {
	// Path is a relative directory under base_directory.
	Path string `json:"path"`
	// Pattern is a glob to match file names.
	Pattern string `json:"pattern"`
	// CaseSensitive controls glob case matching.
	CaseSensitive bool `json:"case_sensitive"`
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
func (f *fileToolSet) searchFile(
	ctx context.Context,
	req *searchFileRequest,
) (*searchFileResponse, error) {
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

	ref, err := fileref.Parse(req.Path)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	if ref.Scheme == fileref.SchemeArtifact {
		rsp.Message = "Error: searching artifact:// is not supported"
		return rsp, fmt.Errorf("searching artifact:// is not supported")
	}
	if ref.Scheme == fileref.SchemeWorkspace {
		rsp.Path = fileref.WorkspaceRef(ref.Path)
		files, folders, err := matchWorkspacePaths(
			ctx,
			ref.Path,
			req.Pattern,
			req.CaseSensitive,
		)
		if err != nil {
			rsp.Message = fmt.Sprintf("Error: %v", err)
			return rsp, err
		}
		rsp.Files = files
		rsp.Folders = folders
		rsp.Message = fmt.Sprintf(
			"Found %d files and %d folders matching pattern "+
				"'%s' in %s",
			len(rsp.Files),
			len(rsp.Folders),
			req.Pattern,
			rsp.Path,
		)
		return rsp, nil
	}

	reqPath := strings.TrimSpace(req.Path)
	rsp.Path = reqPath
	// Resolve and validate the target path.
	targetPath, err := f.resolvePath(reqPath)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	// Check if the target path exists.
	stat, err := os.Stat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			wsFiles, wsFolders := listWorkspaceEntries(ctx, reqPath)
			if len(wsFiles) > 0 || len(wsFolders) > 0 {
				clean := filepath.Clean(reqPath)
				if clean == "." {
					clean = ""
				}
				rsp.Path = fileref.WorkspaceRef(clean)
				files, folders, err := matchWorkspacePaths(
					ctx,
					reqPath,
					req.Pattern,
					req.CaseSensitive,
				)
				if err != nil {
					rsp.Message = fmt.Sprintf("Error: %v", err)
					return rsp, err
				}
				rsp.Files = files
				rsp.Folders = folders
				rsp.Message = fmt.Sprintf(
					"Found %d files and %d folders matching "+
						"pattern '%s' in %s",
					len(rsp.Files),
					len(rsp.Folders),
					req.Pattern,
					rsp.Path,
				)
				return rsp, nil
			}
		}
		rsp.Message = fmt.Sprintf(
			"Error: cannot access path '%s': %v",
			reqPath,
			err,
		)
		return rsp, fmt.Errorf("accessing path '%s': %w", reqPath, err)
	}
	// Check if the target path is a file.
	if !stat.IsDir() {
		rsp.Message = fmt.Sprintf(
			"Error: target path '%s' is a file, not a directory",
			reqPath,
		)
		return rsp, fmt.Errorf(
			"target path '%s' is a file, not a directory",
			reqPath,
		)
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
		relativePath := filepath.Join(reqPath, match)
		if stat.IsDir() {
			rsp.Folders = append(rsp.Folders, relativePath)
		} else {
			rsp.Files = append(rsp.Files, relativePath)
		}
	}
	rsp.Message = fmt.Sprintf(
		"Found %d files and %d folders matching '%s' in %s",
		len(rsp.Files),
		len(rsp.Folders),
		req.Pattern,
		targetPath,
	)
	return rsp, nil
}

// searchFileTool returns a callable tool for searching file.
func (f *fileToolSet) searchFileTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.searchFile,
		function.WithName("search_file"),
		function.WithDescription(
			"Find files by glob under base_directory. "+
				"Supports workspace:// paths.",
		),
	)
}
