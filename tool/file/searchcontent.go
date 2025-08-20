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
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// searchContentRequest represents the input for the search content operation.
type searchContentRequest struct {
	Path                 string `json:"path" jsonschema:"description=The relative path from the base directory to search in."`
	FilePattern          string `json:"file_pattern" jsonschema:"description=The file pattern to match."`
	FileCaseSensitive    bool   `json:"file_case_sensitive" jsonschema:"description=Whether file pattern matching should be case sensitive."`
	ContentPattern       string `json:"content_pattern" jsonschema:"description=The pattern to search for in file content."`
	ContentCaseSensitive bool   `json:"content_case_sensitive" jsonschema:"description=Whether content pattern matching should be case sensitive."`
}

// searchContentResponse represents the output from the search content operation.
type searchContentResponse struct {
	BaseDirectory  string       `json:"base_directory"`
	Path           string       `json:"path"`
	FilePattern    string       `json:"file_pattern"`
	ContentPattern string       `json:"content_pattern"`
	FileMatches    []*fileMatch `json:"file_matches"`
	Message        string       `json:"message"`
}

// fileMatch represents all matches within a single file.
type fileMatch struct {
	FilePath string       `json:"file_path"`
	Matches  []*lineMatch `json:"matches"`
	Message  string       `json:"message"`
}

// lineMatch represents a single line match within a file.
type lineMatch struct {
	LineNumber  int    `json:"line_number"`
	LineContent string `json:"line_content"`
}

// searchContent performs the search content operation.
func (f *fileToolSet) searchContent(_ context.Context, req *searchContentRequest) (*searchContentResponse, error) {
	rsp := &searchContentResponse{
		BaseDirectory:  f.baseDir,
		Path:           req.Path,
		FilePattern:    req.FilePattern,
		ContentPattern: req.ContentPattern,
		FileMatches:    []*fileMatch{},
	}
	// Validate required parameters.
	if err := validatePattern(req.FilePattern, req.ContentPattern); err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	// Resolve and validate the target path.
	targetPath, err := f.resolvePath(req.Path)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	// Check if the target path exists.
	stat, err := os.Stat(targetPath)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: cannot access path '%s': %v", req.Path, err)
		return rsp, fmt.Errorf("accessing path '%s': %w", req.Path, err)
	}
	// Check if the target path is a file.
	if !stat.IsDir() {
		rsp.Message = fmt.Sprintf("Error: target path '%s' is a file, not a directory", req.Path)
		return rsp, fmt.Errorf("target path '%s' is a file, not a directory", req.Path)
	}
	// Compile content pattern as regex.
	re, err := regexCompile(req.ContentPattern, req.ContentCaseSensitive)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	// Find files matching the file pattern.
	files, err := f.matchFiles(targetPath, req.FilePattern, req.FileCaseSensitive)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	// Search content in files concurrently.
	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		fileMatches []*fileMatch
	)
	for _, file := range files {
		fullPath := filepath.Join(targetPath, file)
		relPath := filepath.Join(req.Path, file)
		stat, err := os.Stat(fullPath)
		// Skip directories and files we can't stat.
		if err != nil || stat.IsDir() || stat.Size() > f.maxFileSize {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			match, err := searchFileContent(fullPath, re)
			if err == nil && len(match.Matches) > 0 {
				match.FilePath = relPath
				match.Message = fmt.Sprintf("Found %d matches in file '%s'", len(match.Matches), relPath)
				mu.Lock()
				fileMatches = append(fileMatches, match)
				mu.Unlock()
			}
		}()
	}
	// Wait for all goroutines to complete.
	wg.Wait()
	rsp.FileMatches = fileMatches
	rsp.Message = fmt.Sprintf("Found %v files matching", len(fileMatches))
	return rsp, nil
}

// searchContentTool returns a callable tool for searching content.
func (f *fileToolSet) searchContentTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.searchContent,
		function.WithName("search_content"),
		function.WithDescription("Search files matched by 'file_pattern' under 'path' and return lines that match "+
			"'content_pattern' using a regular expression. "+
			"The 'path' parameter specifies the directory to search in, relative to the base directory "+
			"(e.g., 'subdir', 'subdir/nested'). If 'path' is empty or not provided, searches in the base "+
			"directory. If 'path' points to a file instead of a directory, returns an error. "+
			"The 'file_pattern' parameter selects files using a glob and supports '**' for recursive matching. "+
			"The 'file_case_sensitive' parameter controls whether file pattern matching is case sensitive, "+
			"false by default. "+
			"The 'content_pattern' parameter is a regular expression applied per line. "+
			"The 'content_case_sensitive' parameter controls whether content matching is case sensitive, "+
			"false by default. "+
			"If 'file_pattern' is empty or not provided, returns an error. "+
			"If 'content_pattern' is empty or not provided, returns an error. "+
			"Pattern examples: '*.txt' (all txt files), 'file*.csv' (csv files starting with 'file'), "+
			"'subdir/*.go' (go files in subdir), '**/*.go' (all go files recursively), '*data*' (filename or "+
			"directory containing 'data').",
		),
	)
}

// validatePattern validates the file and content patterns.
func validatePattern(filePattern string, contentPattern string) error {
	if filePattern == "" {
		return fmt.Errorf("file pattern cannot be empty")
	}
	if contentPattern == "" {
		return fmt.Errorf("content pattern cannot be empty")
	}
	return nil
}

// regexCompile compiles a regular expression with case sensitivity.
func regexCompile(pattern string, caseSensitive bool) (*regexp.Regexp, error) {
	flags := ""
	if !caseSensitive {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid content pattern '%s': %w", pattern, err)
	}
	return re, nil
}

// searchFileContent searches for content matches in a single file.
func searchFileContent(filePath string, re *regexp.Regexp) (*fileMatch, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(content), "\n")
	fileMatches := &fileMatch{Matches: []*lineMatch{}}
	// Search each line for matches.
	for lineNum, line := range lines {
		if re.MatchString(line) {
			fileMatches.Matches = append(fileMatches.Matches, &lineMatch{
				LineNumber:  lineNum + 1, // Line numbers are 1-based.
				LineContent: line,
			})
		}
	}
	return fileMatches, nil
}
