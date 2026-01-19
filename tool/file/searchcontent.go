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
	"regexp"
	"slices"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// searchContentRequest represents the input for the search content operation.
type searchContentRequest struct {
	// Path is a relative directory under base_directory.
	Path string `json:"path"`
	// FilePattern selects files (glob or workspace://... for an exported
	// workspace file).
	FilePattern string `json:"file_pattern"`
	// FileCaseSensitive controls glob case matching.
	FileCaseSensitive bool `json:"file_case_sensitive"`
	// ContentPattern is a regex applied per line.
	ContentPattern string `json:"content_pattern"`
	// ContentCaseSensitive controls regex case matching.
	ContentCaseSensitive bool `json:"content_case_sensitive"`
}

// searchContentResponse represents the output from the search content
// operation.
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
func (f *fileToolSet) searchContent(
	ctx context.Context,
	req *searchContentRequest,
) (*searchContentResponse, error) {
	rsp := &searchContentResponse{
		BaseDirectory:  f.baseDir,
		Path:           "",
		FilePattern:    "",
		ContentPattern: "",
		FileMatches:    []*fileMatch{},
	}
	if req == nil {
		err := fmt.Errorf("request cannot be nil")
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	rsp.Path = req.Path
	rsp.FilePattern = req.FilePattern
	rsp.ContentPattern = req.ContentPattern

	// Validate required parameters.
	if err := validatePattern(req.FilePattern, req.ContentPattern); err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	// Compile content pattern as regex.
	re, err := regexCompile(req.ContentPattern, req.ContentCaseSensitive)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}

	matches, ok, err := f.searchContentByFilePatternRef(ctx, req, re)
	if ok {
		if err != nil {
			rsp.Message = fmt.Sprintf("Error: %v", err)
			return rsp, err
		}
		rsp.FileMatches = matches
		rsp.Message = fmt.Sprintf("Found %v files matching", len(matches))
		return rsp, nil
	}

	path, matches, err := f.searchContentByPath(ctx, req, re)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	rsp.Path = path
	rsp.FileMatches = matches
	rsp.Message = fmt.Sprintf("Found %v files matching", len(matches))
	return rsp, nil
}

func (f *fileToolSet) searchContentByFilePatternRef(
	ctx context.Context,
	req *searchContentRequest,
	re *regexp.Regexp,
) ([]*fileMatch, bool, error) {
	if req == nil || re == nil || hasGlob(req.FilePattern) {
		return nil, false, nil
	}
	content, _, handled, err := fileref.TryRead(ctx, req.FilePattern)
	if !handled {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if int64(len(content)) > f.maxFileSize {
		return nil, true, fmt.Errorf(
			"file size is beyond of max file size, "+
				"file size: %d, max file size: %d",
			len(content),
			f.maxFileSize,
		)
	}

	path := req.FilePattern
	if ref, err := fileref.Parse(req.FilePattern); err == nil &&
		ref.Scheme == fileref.SchemeWorkspace {
		path = fileref.WorkspaceRef(ref.Path)
	}
	match := searchTextContent(path, content, re)
	if len(match.Matches) == 0 {
		return []*fileMatch{}, true, nil
	}
	match.Message = fmt.Sprintf(
		"Found %d matches in file '%s'",
		len(match.Matches),
		path,
	)
	return []*fileMatch{match}, true, nil
}

func (f *fileToolSet) searchContentByPath(
	ctx context.Context,
	req *searchContentRequest,
	re *regexp.Regexp,
) (string, []*fileMatch, error) {
	if req == nil || re == nil {
		return "", nil, fmt.Errorf("request cannot be nil")
	}
	pathRef, err := fileref.Parse(req.Path)
	if err != nil {
		return "", nil, err
	}
	switch pathRef.Scheme {
	case fileref.SchemeArtifact:
		return "", nil, fmt.Errorf(
			"searching artifact:// path is not supported",
		)
	case fileref.SchemeWorkspace:
		path := fileref.WorkspaceRef(pathRef.Path)
		matches := f.searchWorkspaceContent(ctx, pathRef.Path, req, re)
		return path, matches, nil
	default:
		reqPath := normalizeToolPath(f.baseDir, pathRef.Path)
		matches, err := f.searchContentLocal(ctx, reqPath, req, re)
		return reqPath, matches, err
	}
}

func (f *fileToolSet) searchContentLocal(
	ctx context.Context,
	reqPath string,
	req *searchContentRequest,
	re *regexp.Regexp,
) ([]*fileMatch, error) {
	// When path is a file (or a cached workspace output file), search directly
	// within that single file. Models commonly pass a file path in "path"
	// together with a glob file_pattern like "*", which would otherwise be
	// treated as a directory and fail.
	if matches, ok := f.searchSinglePath(ctx, reqPath, re); ok {
		return matches, nil
	}
	// Fast path: if the requested file exists only as a skill_run output_files
	// entry, search against the cached content instead of the host filesystem.
	// This avoids model loops where a workspace-relative skill output path is
	// passed to file tools whose base directory is different.
	if matches, ok := f.searchSkillCache(ctx, reqPath, req, re); ok {
		return matches, nil
	}

	targetPath, err := f.resolvePath(reqPath)
	if err != nil {
		return nil, err
	}
	stat, err := os.Stat(targetPath)
	if err != nil {
		return nil, fmt.Errorf("accessing path '%s': %w", reqPath, err)
	}
	if !stat.IsDir() {
		match, ok := f.searchSingleLocalFile(targetPath, reqPath, re)
		if ok {
			return match, nil
		}
		return nil, fmt.Errorf(
			"target path '%s' is a file, not a directory",
			reqPath,
		)
	}

	files, err := f.matchFiles(
		targetPath,
		req.FilePattern,
		req.FileCaseSensitive,
	)
	if err != nil {
		return nil, err
	}

	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		fileMatches []*fileMatch
	)
	for _, file := range files {
		fullPath := filepath.Join(targetPath, file)
		relPath := filepath.Join(reqPath, file)
		stat, err := os.Stat(fullPath)
		if err != nil {
			continue
		}
		if stat.IsDir() || stat.Size() > f.maxFileSize {
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			match, err := searchFileContent(fullPath, re)
			if err != nil || len(match.Matches) == 0 {
				return
			}
			match.FilePath = relPath
			match.Message = fmt.Sprintf(
				"Found %d matches in file '%s'",
				len(match.Matches),
				relPath,
			)
			mu.Lock()
			fileMatches = append(fileMatches, match)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return fileMatches, nil
}

func (f *fileToolSet) searchSinglePath(
	ctx context.Context,
	reqPath string,
	re *regexp.Regexp,
) ([]*fileMatch, bool) {
	if reqPath == "" || re == nil {
		return nil, false
	}
	content, _, ok := toolcache.LookupSkillRunOutputFileFromContext(
		ctx,
		reqPath,
	)
	if !ok {
		return nil, false
	}
	path := fileref.WorkspaceRef(reqPath)
	match := searchTextContent(path, content, re)
	if len(match.Matches) == 0 {
		return []*fileMatch{}, true
	}
	match.Message = fmt.Sprintf(
		"Found %d matches in file '%s'",
		len(match.Matches),
		path,
	)
	return []*fileMatch{match}, true
}

func (f *fileToolSet) searchSingleLocalFile(
	fullPath string,
	reqPath string,
	re *regexp.Regexp,
) ([]*fileMatch, bool) {
	if strings.TrimSpace(fullPath) == "" || re == nil {
		return nil, false
	}
	st, err := os.Stat(fullPath)
	if err != nil || st.IsDir() {
		return nil, false
	}
	if st.Size() > f.maxFileSize {
		return []*fileMatch{}, true
	}
	match, err := searchFileContent(fullPath, re)
	if err != nil || len(match.Matches) == 0 {
		if err == nil {
			return []*fileMatch{}, true
		}
		return nil, false
	}
	match.FilePath = reqPath
	match.Message = fmt.Sprintf(
		"Found %d matches in file '%s'",
		len(match.Matches),
		reqPath,
	)
	return []*fileMatch{match}, true
}

func normalizeToolPath(baseDir string, p string) string {
	s := strings.TrimSpace(p)
	if s == "" || s == "." {
		return ""
	}
	base := filepath.Base(baseDir)
	if base != "" && base != "." && s == base {
		if _, err := os.Stat(filepath.Join(baseDir, s)); err != nil {
			if os.IsNotExist(err) {
				return ""
			}
		}
	}
	return s
}

func (f *fileToolSet) searchSkillCache(
	ctx context.Context,
	reqPath string,
	req *searchContentRequest,
	re *regexp.Regexp,
) ([]*fileMatch, bool) {
	if req == nil || re == nil {
		return nil, false
	}
	if hasGlob(req.FilePattern) {
		return nil, false
	}

	candidate := strings.TrimSpace(req.FilePattern)
	if candidate == "" {
		return nil, false
	}
	if reqPath != "" && !strings.ContainsAny(candidate, `/\`) {
		candidate = filepath.Join(reqPath, candidate)
	}

	content, _, ok := toolcache.LookupSkillRunOutputFileFromContext(
		ctx,
		candidate,
	)
	if !ok {
		return nil, false
	}
	match := searchTextContent(candidate, content, re)
	if len(match.Matches) == 0 {
		return []*fileMatch{}, true
	}
	match.Message = fmt.Sprintf(
		"Found %d matches in file '%s'",
		len(match.Matches),
		candidate,
	)
	return []*fileMatch{match}, true
}

func (f *fileToolSet) searchWorkspaceContent(
	ctx context.Context,
	dir string,
	req *searchContentRequest,
	re *regexp.Regexp,
) []*fileMatch {
	if req == nil || re == nil {
		return []*fileMatch{}
	}

	sep := string(filepath.Separator)
	base := filepath.Clean(strings.TrimSpace(dir))
	if base == "." {
		base = ""
	}
	prefix := base
	if prefix != "" {
		prefix += sep
	}

	var out []*fileMatch
	for _, entry := range fileref.WorkspaceFiles(ctx) {
		full := filepath.Clean(strings.TrimSpace(entry.Name))
		if full == "" || full == "." {
			continue
		}
		if prefix != "" && !strings.HasPrefix(full, prefix) {
			continue
		}
		rel := strings.TrimPrefix(full, prefix)
		ok, err := matchWorkspacePattern(
			req.FilePattern,
			rel,
			req.FileCaseSensitive,
		)
		if err != nil || !ok {
			continue
		}
		if int64(len(entry.Content)) > f.maxFileSize {
			continue
		}
		path := fileref.WorkspaceRef(full)
		match := searchTextContent(path, entry.Content, re)
		if len(match.Matches) == 0 {
			continue
		}
		match.Message = fmt.Sprintf(
			"Found %d matches in file '%s'",
			len(match.Matches),
			path,
		)
		out = append(out, match)
	}
	slices.SortFunc(out, func(a, b *fileMatch) int {
		return strings.Compare(a.FilePath, b.FilePath)
	})
	return out
}

func hasGlob(p string) bool {
	return strings.ContainsAny(p, "*?[")
}

func searchTextContent(
	path string,
	content string,
	re *regexp.Regexp,
) *fileMatch {
	lines := strings.Split(content, "\n")
	matches := &fileMatch{
		FilePath: path,
		Matches:  []*lineMatch{},
	}
	for lineNum, line := range lines {
		if re.MatchString(line) {
			matches.Matches = append(matches.Matches, &lineMatch{
				LineNumber:  lineNum + 1,
				LineContent: line,
			})
		}
	}
	return matches
}

// searchContentTool returns a callable tool for searching content.
func (f *fileToolSet) searchContentTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.searchContent,
		function.WithName("search_content"),
		function.WithDescription(
			"Search text files under base_directory for lines that "+
				"match a regex. Supports workspace:// paths and "+
				"artifact:// single-file refs.",
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
func regexCompile(
	pattern string,
	caseSensitive bool,
) (*regexp.Regexp, error) {
	flags := ""
	if !caseSensitive {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + pattern)
	if err != nil {
		return nil, fmt.Errorf(
			"invalid content pattern '%s': %w",
			pattern,
			err,
		)
	}
	return re, nil
}

// searchFileContent searches for content matches in a single file.
func searchFileContent(
	filePath string,
	re *regexp.Regexp,
) (*fileMatch, error) {
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
