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
	"slices"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// listFileRequest represents the input for the list file operation.
type listFileRequest struct {
	// Path is a relative directory under base_directory.
	Path string `json:"path"`

	// WithSize returns the size of the files.
	WithSize bool `json:"with_size"`
}

// listFileResponse represents the output from the list file operation.
type listFileResponse struct {
	BaseDirectory string   `json:"base_directory"`
	Path          string   `json:"path"`
	Files         []string `json:"files"`
	Folders       []string `json:"folders"`
	Message       string   `json:"message"`

	FilesWithSize []fileInfo `json:"files_with_size"`
}

type fileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// listFile performs the list file operation.
func (f *fileToolSet) listFile(
	ctx context.Context,
	req *listFileRequest,
) (*listFileResponse, error) {
	rsp := &listFileResponse{
		BaseDirectory: f.baseDir,
		Path:          req.Path,
	}

	ref, err := fileref.Parse(req.Path)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	if ref.Scheme == fileref.SchemeArtifact {
		rsp.Message = "Error: listing artifact:// is not supported"
		return rsp, fmt.Errorf("listing artifact:// is not supported")
	}
	if ref.Scheme == fileref.SchemeWorkspace {
		rsp.Path = fileref.WorkspaceRef(ref.Path)
		rsp.Files, rsp.Folders = listWorkspaceEntries(ctx, ref.Path)
		rsp.Message = fmt.Sprintf(
			"Found %d files and %d folders in %s",
			len(rsp.Files),
			len(rsp.Folders),
			rsp.Path,
		)
		return rsp, nil
	}

	reqPath := strings.TrimSpace(req.Path)
	rsp.Path = reqPath
	// Resolve the target path.
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
				rsp.Files = wsFiles
				rsp.Folders = wsFolders
				rsp.Message = fmt.Sprintf(
					"Found %d files and %d folders in %s",
					len(rsp.Files),
					len(rsp.Folders),
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
	// If the target is a file, return information about that file.
	if !stat.IsDir() {
		rsp.Message = fmt.Sprintf(
			"Error: path '%s' is a file, not a directory",
			reqPath,
		)
		return rsp, fmt.Errorf(
			"path '%s' is a file, not a directory",
			reqPath,
		)
	}
	// If the target is a directory, list its contents.
	entries, err := os.ReadDir(targetPath)
	if err != nil {
		rsp.Message = fmt.Sprintf(
			"Error: cannot read directory '%s': %v",
			reqPath,
			err,
		)
		return rsp, fmt.Errorf(
			"reading directory '%s': %w",
			reqPath,
			err,
		)
	}
	// Collect files and folders.
	for _, entry := range entries {
		if entry.IsDir() {
			rsp.Folders = append(rsp.Folders, entry.Name())
		} else {
			rsp.Files = append(rsp.Files, entry.Name())
			if req.WithSize {
				if info, _ := entry.Info(); info != nil {
					rsp.FilesWithSize = append(rsp.FilesWithSize, fileInfo{
						Name: entry.Name(),
						Size: info.Size(),
					})
				}
			}
		}
	}
	// Create a summary message.
	if reqPath == "" {
		rsp.Message = fmt.Sprintf(
			"Found %d files and %d folders in base directory",
			len(rsp.Files),
			len(rsp.Folders),
		)
	} else {
		rsp.Message = fmt.Sprintf(
			"Found %d files and %d folders in %s",
			len(rsp.Files),
			len(rsp.Folders),
			reqPath,
		)
	}
	return rsp, nil
}

func listWorkspaceEntries(
	ctx context.Context,
	dir string,
) ([]string, []string) {
	sep := string(filepath.Separator)
	prefix := filepath.Clean(strings.TrimSpace(dir))
	if prefix == "." {
		prefix = ""
	}
	if prefix != "" {
		prefix += sep
	}

	filesSet := make(map[string]struct{})
	foldersSet := make(map[string]struct{})

	for _, f := range fileref.WorkspaceFiles(ctx) {
		name := filepath.Clean(strings.TrimSpace(f.Name))
		if name == "" || name == "." {
			continue
		}
		if prefix != "" {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			name = strings.TrimPrefix(name, prefix)
		}
		if name == "" || name == "." {
			continue
		}
		head, _, found := strings.Cut(name, sep)
		if !found {
			filesSet[prefix+head] = struct{}{}
			continue
		}
		foldersSet[prefix+head] = struct{}{}
	}

	files := make([]string, 0, len(filesSet))
	for n := range filesSet {
		files = append(files, fileref.WorkspaceRef(n))
	}
	folders := make([]string, 0, len(foldersSet))
	for n := range foldersSet {
		folders = append(folders, fileref.WorkspaceRef(n))
	}
	slices.Sort(files)
	slices.Sort(folders)
	return files, folders
}

// listFileTool returns a callable tool for listing file.
func (f *fileToolSet) listFileTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.listFile,
		function.WithName("list_file"),
		function.WithDescription(
			"List files under base_directory. Supports workspace:// paths.",
		),
	)
}
