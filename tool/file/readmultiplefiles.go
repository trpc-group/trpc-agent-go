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
	"slices"
	"strings"
	"sync"

	multierror "github.com/hashicorp/go-multierror"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// readMultipleFilesRequest represents the input for the read multiple files
// operation.
type readMultipleFilesRequest struct {
	Patterns      []string `json:"patterns" jsonschema:"description=Globs"`
	CaseSensitive bool     `json:"case_sensitive" jsonschema:"description=Case"`
}

// readMultipleFilesResponse represents the output from the
// read_multiple_files operation.
type readMultipleFilesResponse struct {
	BaseDirectory string            `json:"base_directory"`
	Files         []*fileReadResult `json:"files"`
	Message       string            `json:"message"`
}

// fileReadResult represents the per-file read result.
type fileReadResult struct {
	FileName string `json:"file_name"`
	Contents string `json:"contents"`
	Message  string `json:"message"`
}

// readMultipleFiles performs the read multiple files operation with support
// for glob patterns.
func (f *fileToolSet) readMultipleFiles(
	ctx context.Context,
	req *readMultipleFilesRequest,
) (*readMultipleFilesResponse, error) {
	rsp := &readMultipleFilesResponse{BaseDirectory: f.baseDir}
	if len(req.Patterns) == 0 {
		rsp.Message = "Error: patterns cannot be empty"
		return rsp, fmt.Errorf("patterns cannot be empty")
	}
	var (
		files []string
		errs  *multierror.Error
	)
	for _, pattern := range req.Patterns {
		if strings.HasPrefix(pattern, fileref.WorkspacePrefix) {
			wsPattern := strings.TrimPrefix(pattern, fileref.WorkspacePrefix)
			idx := buildWorkspaceIndex(ctx)
			for _, name := range idx.files {
				ok, err := matchWorkspacePattern(
					wsPattern,
					name,
					req.CaseSensitive,
				)
				if err != nil {
					errs = multierror.Append(errs, err)
					break
				}
				if ok {
					files = append(files, fileref.WorkspaceRef(name))
				}
			}
			continue
		}
		if strings.HasPrefix(pattern, fileref.ArtifactPrefix) {
			if hasGlob(pattern) {
				errs = multierror.Append(
					errs,
					fmt.Errorf(
						"artifact:// does not support glob: %s",
						pattern,
					),
				)
				continue
			}
			files = append(files, pattern)
			continue
		}
		matchedFiles, err := f.matchFiles(f.baseDir, pattern, req.CaseSensitive)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		if len(matchedFiles) == 0 {
			idx := buildWorkspaceIndex(ctx)
			for _, name := range idx.files {
				ok, err := matchWorkspacePattern(
					pattern,
					name,
					req.CaseSensitive,
				)
				if err != nil {
					errs = multierror.Append(errs, err)
					break
				}
				if ok {
					files = append(files, fileref.WorkspaceRef(name))
				}
			}
			continue
		}
		files = append(files, matchedFiles...)
	}
	slices.Sort(files)
	files = slices.Compact(files)
	rsp.Files = f.readFiles(ctx, files)
	rsp.Message = fmt.Sprintf("Read %d file(s)", len(rsp.Files))
	if errs != nil {
		rsp.Message += fmt.Sprintf(
			". In finding files matched with patterns: %v",
			errs,
		)
	}
	return rsp, nil
}

// readFiles concurrently reads the given relative path files.
func (f *fileToolSet) readFiles(
	ctx context.Context,
	files []string,
) []*fileReadResult {
	n := len(files)
	results := make([]*fileReadResult, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		relativePath := files[i]
		wg.Add(1)
		// Capture the per-iteration path to avoid data race on the loop variable.
		go func(idx int, rp string) {
			defer func() {
				wg.Done()
			}()
			results[idx] = &fileReadResult{FileName: rp}

			content, _, handled, err := fileref.TryRead(ctx, rp)
			if handled {
				if err != nil {
					results[idx].Message = fmt.Sprintf(
						"Error: cannot read %s: %v",
						rp,
						err,
					)
					return
				}
				if int64(len(content)) > f.maxFileSize {
					results[idx].Message = fmt.Sprintf(
						"Error: %s is too large, "+
							"file size: %d, "+
							"max file size: %d",
						rp,
						len(content),
						f.maxFileSize,
					)
					return
				}
				results[idx].Contents = content
				lines := strings.Count(content, "\n") + 1
				results[idx].Message = fmt.Sprintf(
					"Successfully read %s, total lines: %d",
					rp,
					lines,
				)
				return
			}

			fullPath, err := f.resolvePath(rp)
			if err != nil {
				results[idx].Message = fmt.Sprintf(
					"Error: cannot resolve path %s: %v",
					rp,
					err,
				)
				return
			}
			stats, err := os.Stat(fullPath)
			if err != nil {
				results[idx].Message = fmt.Sprintf(
					"Error: cannot stat file %s: %v",
					rp,
					err,
				)
				return
			}
			if stats.IsDir() {
				results[idx].Message = fmt.Sprintf("Error: %s is a directory", rp)
				return
			}
			if stats.Size() > f.maxFileSize {
				results[idx].Message = fmt.Sprintf(
					"Error: %s is too large: %d > %d",
					rp,
					stats.Size(),
					f.maxFileSize,
				)
				return
			}
			data, err := os.ReadFile(fullPath)
			if err != nil {
				results[idx].Message = fmt.Sprintf(
					"Error: cannot read file %s: %v",
					rp,
					err,
				)
				return
			}
			if len(data) == 0 {
				results[idx].Contents = ""
				results[idx].Message = fmt.Sprintf(
					"Successfully read %s, but file is empty",
					rp,
				)
				return
			}
			lines := strings.Count(string(data), "\n") + 1
			results[idx].Contents = string(data)
			results[idx].Message = fmt.Sprintf(
				"Successfully read %s, total lines: %d",
				rp,
				lines,
			)
		}(i, relativePath)
	}
	wg.Wait()
	return results
}

// readMultipleFilesTool returns a callable tool for reading multiple files.
func (f *fileToolSet) readMultipleFilesTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.readMultipleFiles,
		function.WithName("read_multiple_files"),
		function.WithDescription(
			"Read multiple text files under base_directory. "+
				"Supports glob patterns and workspace:// refs.",
		),
	)
}
