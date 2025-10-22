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
	"slices"
	"strings"
	"sync"

	multierror "github.com/hashicorp/go-multierror"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// readMultipleFilesRequest represents the input for the read multiple files operation.
type readMultipleFilesRequest struct {
	Patterns      []string `json:"patterns" jsonschema:"description=glob patterns of relative file path"`
	CaseSensitive bool     `json:"case_sensitive" jsonschema:"description=Whether pattern matching is case sensitive"`
}

// readMultipleFilesResponse represents the output from the read multiple files operation.
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

// readMultipleFiles performs the read multiple files operation with support for glob patterns.
func (f *fileToolSet) readMultipleFiles(_ context.Context,
	req *readMultipleFilesRequest) (*readMultipleFilesResponse, error) {
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
		matchedFiles, err := f.matchFiles(f.baseDir, pattern, req.CaseSensitive)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		files = append(files, matchedFiles...)
	}
	slices.Sort(files)
	files = slices.Compact(files)
	rsp.Files = f.readFiles(files)
	rsp.Message = fmt.Sprintf("Read %d file(s)", len(rsp.Files))
	if errs != nil {
		rsp.Message += fmt.Sprintf(". In finding files matched with patterns: %v", errs)
	}
	return rsp, nil
}

// readFiles concurrently reads the given relative path files.
func (f *fileToolSet) readFiles(files []string) []*fileReadResult {
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
			fullPath, err := f.resolvePath(rp)
			if err != nil {
				results[idx].Message = fmt.Sprintf("Error: cannot resolve path %s: %v", rp, err)
				return
			}
			stats, err := os.Stat(fullPath)
			if err != nil {
				results[idx].Message = fmt.Sprintf("Error: cannot stat file %s: %v", rp, err)
				return
			}
			if stats.IsDir() {
				results[idx].Message = fmt.Sprintf("Error: %s is a directory", rp)
				return
			}
			if stats.Size() > f.maxFileSize {
				results[idx].Message = fmt.Sprintf("Error: %s is too large, file size: %d, max file size: %d",
					rp, stats.Size(), f.maxFileSize)
				return
			}
			data, err := os.ReadFile(fullPath)
			if err != nil {
				results[idx].Message = fmt.Sprintf("Error: cannot read file %s: %v", rp, err)
				return
			}
			if len(data) == 0 {
				results[idx].Contents = ""
				results[idx].Message = fmt.Sprintf("Successfully read %s, but file is empty", rp)
				return
			}
			lines := strings.Count(string(data), "\n") + 1
			results[idx].Contents = string(data)
			results[idx].Message = fmt.Sprintf("Successfully read %s, total lines: %d", rp, lines)
		}(i, relativePath)
	}
	wg.Wait()
	return results
}

// readMultipleFilesTool returns a callable tool for reading multiple files with glob support.
func (f *fileToolSet) readMultipleFilesTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.readMultipleFiles,
		function.WithName("read_multiple_files"),
		function.WithDescription(
			"Reads multiple files matched by 'patterns' (glob, relative to base directory). "+
				"The 'patterns' parameter is a list of glob patterns, like ['*.go', 'src/**/*.go', '**/*.md', "+
				"'README.md', 'config/**']. "+
				"'*' matches within a single path segment; '**' matches recursively across directories. "+
				"The 'case_sensitive' flag controls case sensitivity (false by default). "+
				"For each file, returns 'file_name', 'contents', and a 'message' describing the result. "+
				"If a pattern fails to expand, the error is aggregated while other patterns continue. ",
		),
	)
}
