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
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	// searchSizeCapMultiple scales maxFileSize into the search-size cap. The
	// read limit protects the model's context from whole-file dumps; search
	// returns only matching lines, so it can safely look inside files far
	// larger than a read may return whole. Files above even this cap are
	// reported by name, never skipped silently: a zero-match result must be
	// distinguishable from a file the tool refused to open.
	searchSizeCapMultiple = 64
	// maxMatchesPerFile bounds how many matching lines one file contributes,
	// so an overly broad pattern on a huge file cannot flood the result.
	maxMatchesPerFile = 500
	// maxSearchLineSize bounds a single scanned line during streaming search;
	// a line longer than this fails the file's scan rather than the process.
	maxSearchLineSize = 4 * 1024 * 1024
	// minSearchConcurrency is the floor for the default scan concurrency.
	minSearchConcurrency = 4
)

// defaultSearchConcurrency is how many files a search scans at once when the
// caller sets nothing: enough to keep the disk busy, few enough that a pattern
// covering a whole repository cannot pin a blocked-read thread per file and
// exhaust the runtime's thread limit.
func defaultSearchConcurrency() int {
	return max(minSearchConcurrency, 2*runtime.GOMAXPROCS(0))
}

// searchSizeCap is the largest file search will stream through. It saturates
// rather than overflows: a read limit large enough that scaling it would wrap
// means no file is too large to search.
func (f *fileToolSet) searchSizeCap() int64 {
	if f.maxFileSize > math.MaxInt64/searchSizeCapMultiple {
		return math.MaxInt64
	}
	return f.maxFileSize * searchSizeCapMultiple
}

// searchContentRequest represents the input for the search content operation.
type searchContentRequest struct {
	// Path is a relative directory under base_directory.
	Path string `json:"path" jsonschema:"description=Relative directory path under base_directory or workspace:// directory ref; can also be a single local file path"`
	// FilePattern selects files (glob or workspace://... for an exported
	// workspace file).
	FilePattern string `json:"file_pattern" jsonschema:"description=Glob pattern for files to search or a direct workspace:// or artifact:// file ref"`
	// FileCaseSensitive controls glob case matching.
	FileCaseSensitive bool `json:"file_case_sensitive" jsonschema:"description=Whether file pattern matching should be case-sensitive"`
	// ContentPattern is a regex applied per line.
	ContentPattern string `json:"content_pattern" jsonschema:"description=Regular expression to search for within matched files"`
	// ContentCaseSensitive controls regex case matching.
	ContentCaseSensitive bool `json:"content_case_sensitive" jsonschema:"description=Whether regular expression matching should be case-sensitive"`
}

// searchContentResponse represents the output from the search content
// operation.
type searchContentResponse struct {
	BaseDirectory  string       `json:"base_directory"`
	Path           string       `json:"path"`
	FilePattern    string       `json:"file_pattern"`
	ContentPattern string       `json:"content_pattern"`
	FileMatches    []*fileMatch `json:"file_matches"`
	// SkippedFiles names files that matched the file pattern but were not
	// searched: they exceed the search-size cap, or their scan failed partway
	// on a line the scanner cannot hold. Never silent, so a zero-match result
	// is distinguishable from a file the tool refused.
	SkippedFiles []string `json:"skipped_files,omitempty"`
	// OmittedFiles counts files that matched the content pattern but fell
	// beyond the result cap, so the model knows the listing is partial.
	OmittedFiles int    `json:"omitted_files,omitempty"`
	Message      string `json:"message"`
}

// fileMatch represents all matches within a single file.
type fileMatch struct {
	FilePath string       `json:"file_path"`
	Matches  []*lineMatch `json:"matches"`
	// Truncated reports that the file had more matching lines than
	// maxMatchesPerFile and only the first ones are listed.
	Truncated bool   `json:"truncated,omitempty"`
	Message   string `json:"message"`
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
		err := errors.New("request cannot be nil")
		rsp.Message = "Error: " + err.Error()
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
		matched := len(matches)
		matches, omitted, capped := capSearchMatches(matches, f.searchMatchLimit())
		rsp.FileMatches = matches
		rsp.OmittedFiles = omitted
		rsp.Message = f.searchResultMessage(matched, omitted, capped, nil)
		return rsp, nil
	}

	path, matches, skipped, err := f.searchContentByPath(ctx, req, re)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	matched := len(matches)
	matches, omitted, capped := capSearchMatches(matches, f.searchMatchLimit())
	rsp.Path = path
	rsp.FileMatches = matches
	rsp.SkippedFiles = skipped
	rsp.OmittedFiles = omitted
	rsp.Message = f.searchResultMessage(matched, omitted, capped, skipped)
	return rsp, nil
}

// searchResultMessage summarizes a search, naming any file that matched the
// file pattern but could not be searched — too large, or a scan that failed —
// so a zero-match result is never mistaken for proof of absence, and saying
// when the listing stopped at the result cap.
func (f *fileToolSet) searchResultMessage(
	matched int,
	omitted int,
	capped bool,
	skipped []string,
) string {
	msg := fmt.Sprintf("Found %v files matching", matched)
	if capped {
		msg += fmt.Sprintf(
			"; stopped listing at %d matching lines",
			f.searchMatchLimit(),
		)
		if omitted > 0 {
			msg += fmt.Sprintf(", %d matching file(s) not shown", omitted)
		}
		msg += " — narrow path, the file pattern or the content pattern"
	}
	if len(skipped) > 0 {
		msg += fmt.Sprintf(
			"; %d file(s) matched the file pattern but were NOT searched "+
				"(beyond the %d-byte search cap, or a line the scanner "+
				"cannot hold): %s",
			len(skipped),
			f.searchSizeCap(),
			strings.Join(skipped, ", "),
		)
	}
	return msg
}

// capSearchMatches keeps the first files, in path order, whose matching lines
// fit within limit. The file that crosses the limit is cut to fit and marked
// truncated. It returns the count of files dropped entirely and whether the
// cap cut anything at all, so the caller can report a partial listing rather
// than pass it off as complete.
func capSearchMatches(
	matches []*fileMatch,
	limit int,
) ([]*fileMatch, int, bool) {
	if limit <= 0 {
		return matches, 0, false
	}
	slices.SortFunc(matches, func(a, b *fileMatch) int {
		return strings.Compare(a.FilePath, b.FilePath)
	})
	remaining := limit
	for i, m := range matches {
		if len(m.Matches) <= remaining {
			remaining -= len(m.Matches)
			continue
		}
		if remaining == 0 {
			return matches[:i], len(matches) - i, true
		}
		m.Matches = m.Matches[:remaining]
		m.Truncated = true
		m.Message = fmt.Sprintf(
			"Found %d matches in file '%s' (stopped at the result cap)",
			len(m.Matches),
			m.FilePath,
		)
		return matches[:i+1], len(matches) - i - 1, true
	}
	return matches, 0, false
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
	if int64(len(content)) > f.searchSizeCap() {
		return nil, true, fmt.Errorf(
			"file size is beyond of max search size, "+
				"file size: %d, max search size: %d",
			len(content),
			f.searchSizeCap(),
		)
	}

	path := req.FilePattern
	if ref, err := fileref.Parse(req.FilePattern); err == nil &&
		ref.Scheme == fileref.SchemeWorkspace {
		path = fileref.WorkspaceRef(ref.Path)
	}
	match := searchTextContent(ctx, path, content, re)
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if len(match.Matches) == 0 {
		return []*fileMatch{}, true, nil
	}
	match.Message = fileMatchMessage(match, path)
	return []*fileMatch{match}, true, nil
}

func (f *fileToolSet) searchContentByPath(
	ctx context.Context,
	req *searchContentRequest,
	re *regexp.Regexp,
) (string, []*fileMatch, []string, error) {
	if req == nil || re == nil {
		return "", nil, nil, errors.New("request cannot be nil")
	}
	pathRef, err := fileref.Parse(req.Path)
	if err != nil {
		return "", nil, nil, err
	}
	switch pathRef.Scheme {
	case fileref.SchemeArtifact:
		return "", nil, nil, fmt.Errorf(
			"searching artifact:// path is not supported",
		)
	case fileref.SchemeWorkspace:
		path := fileref.WorkspaceRef(pathRef.Path)
		matches, skipped, err := f.searchWorkspaceContent(ctx, pathRef.Path, req, re)
		if err != nil {
			return path, nil, nil, err
		}
		if err := ctx.Err(); err != nil {
			return path, nil, nil, err
		}
		return path, matches, skipped, nil
	default:
		reqPath := normalizeToolPath(f.baseDir, pathRef.Path)
		matches, skipped, err := f.searchContentLocal(ctx, reqPath, req, re)
		return reqPath, matches, skipped, err
	}
}

func (f *fileToolSet) searchContentLocal(
	ctx context.Context,
	reqPath string,
	req *searchContentRequest,
	re *regexp.Regexp,
) ([]*fileMatch, []string, error) {
	// When path is a file (or a cached workspace output file), search directly
	// within that single file. Models commonly pass a file path in "path"
	// together with a glob file_pattern like "*", which would otherwise be
	// treated as a directory and fail.
	// A cached search stops early on a cancelled context, so the cancellation
	// is reported rather than its partial result returned as a success.
	if matches, ok := f.searchSinglePath(ctx, reqPath, re); ok {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		return matches, nil, nil
	}
	// Fast path: if the requested file exists only as a skill_run output_files
	// entry, search against the cached content instead of the host filesystem.
	// This avoids model loops where a workspace-relative skill output path is
	// passed to file tools whose base directory is different.
	if matches, ok := f.searchSkillCache(ctx, reqPath, req, re); ok {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		return matches, nil, nil
	}

	targetPath, err := f.resolvePath(reqPath)
	if err != nil {
		return nil, nil, err
	}
	stat, err := os.Stat(targetPath)
	if err != nil {
		return nil, nil, fmt.Errorf("accessing path '%s': %w", reqPath, err)
	}
	if !stat.IsDir() {
		match, unsearchable, ok := f.searchSingleLocalFile(ctx, targetPath, reqPath, re)
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, fmt.Errorf(
				"target path '%s' is a file, not a directory",
				reqPath,
			)
		}
		if unsearchable {
			return []*fileMatch{}, []string{reqPath}, nil
		}
		return match, nil, nil
	}

	entries, err := f.walkMatches(
		ctx,
		targetPath,
		req.FilePattern,
		req.FileCaseSensitive,
	)
	if err != nil {
		return nil, nil, err
	}

	// Scans run on a bounded pool: each blocks in a file read, which pins an
	// OS thread, so one goroutine per matched file would let a wide pattern
	// exhaust the runtime's thread limit and abort the process.
	var (
		group       errgroup.Group
		mu          sync.Mutex
		fileMatches []*fileMatch
		skipped     []string
	)
	group.SetLimit(f.searchWorkers())
	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}
		if entry.dir {
			continue
		}
		fullPath := filepath.Join(targetPath, filepath.FromSlash(entry.rel))
		relPath := filepath.Join(reqPath, filepath.FromSlash(entry.rel))
		stat, err := os.Stat(fullPath)
		if err != nil {
			continue
		}
		if stat.IsDir() {
			continue
		}
		if stat.Size() > f.searchSizeCap() {
			// Goroutines for earlier files may be appending scan failures
			// to the same slice, so this append takes the lock too.
			mu.Lock()
			skipped = append(skipped, relPath)
			mu.Unlock()
			continue
		}

		group.Go(func() error {
			match, err := searchFileContent(ctx, fullPath, re)
			if err != nil {
				// A file the scan could not finish — a line beyond the
				// scanner's buffer, or one it could not read — is reported
				// beside the oversized ones rather than as a miss.
				mu.Lock()
				skipped = append(skipped, relPath)
				mu.Unlock()
				return nil
			}
			if len(match.Matches) == 0 {
				return nil
			}
			match.FilePath = relPath
			match.Message = fileMatchMessage(match, relPath)
			mu.Lock()
			fileMatches = append(fileMatches, match)
			mu.Unlock()
			return nil
		})
	}
	_ = group.Wait()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	slices.Sort(skipped)
	return fileMatches, skipped, nil
}

// fileMatchMessage summarizes one file's matches, noting when the list was
// truncated at maxMatchesPerFile.
func fileMatchMessage(m *fileMatch, path string) string {
	if m.Truncated {
		return fmt.Sprintf(
			"Found %d matches in file '%s' (stopped at the first %d)",
			len(m.Matches),
			path,
			maxMatchesPerFile,
		)
	}
	return fmt.Sprintf(
		"Found %d matches in file '%s'",
		len(m.Matches),
		path,
	)
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
	match := searchTextContent(ctx, path, content, re)
	if len(match.Matches) == 0 {
		return []*fileMatch{}, true
	}
	match.Message = fileMatchMessage(match, path)
	return []*fileMatch{match}, true
}

// searchSingleLocalFile searches one local file. The second result reports a
// file that exists but could not be searched — beyond the search cap, or a scan
// that failed partway — so the caller names it rather than reporting a miss.
// The third is false when fullPath is not a file at all.
func (f *fileToolSet) searchSingleLocalFile(
	ctx context.Context,
	fullPath string,
	reqPath string,
	re *regexp.Regexp,
) ([]*fileMatch, bool, bool) {
	if strings.TrimSpace(fullPath) == "" || re == nil {
		return nil, false, false
	}
	st, err := os.Stat(fullPath)
	if err != nil || st.IsDir() {
		return nil, false, false
	}
	if st.Size() > f.searchSizeCap() {
		return []*fileMatch{}, true, true
	}
	match, err := searchFileContent(ctx, fullPath, re)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, true
		}
		return []*fileMatch{}, true, true
	}
	if len(match.Matches) == 0 {
		return []*fileMatch{}, false, true
	}
	match.FilePath = reqPath
	match.Message = fileMatchMessage(match, reqPath)
	return []*fileMatch{match}, false, true
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
	match := searchTextContent(ctx, candidate, content, re)
	if len(match.Matches) == 0 {
		return []*fileMatch{}, true
	}
	match.Message = fileMatchMessage(match, candidate)
	return []*fileMatch{match}, true
}

// searchWorkspaceContent searches exported workspace files under dir. The
// file limit applies here as it does on disk: a file pattern that selects more
// workspace files than the limit is refused rather than scanned. Entries beyond
// the search cap are not searched; they are returned by name in the second
// result, sorted, so the caller reports them in skipped_files exactly as an
// oversized local file is reported.
func (f *fileToolSet) searchWorkspaceContent(
	ctx context.Context,
	dir string,
	req *searchContentRequest,
	re *regexp.Regexp,
) ([]*fileMatch, []string, error) {
	if req == nil || re == nil {
		return []*fileMatch{}, nil, nil
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

	limit := f.searchFileLimit()
	selected := 0
	var (
		out     []*fileMatch
		skipped []string
	)
	for _, entry := range fileref.WorkspaceFiles(ctx) {
		if ctx.Err() != nil {
			break
		}
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
		selected++
		if selected > limit {
			return nil, nil, &tooManyFilesError{
				pattern: req.FilePattern,
				path:    fileref.WorkspaceRef(base),
				limit:   limit,
			}
		}
		path := fileref.WorkspaceRef(full)
		if int64(len(entry.Content)) > f.searchSizeCap() {
			skipped = append(skipped, path)
			continue
		}
		match := searchTextContent(ctx, path, entry.Content, re)
		if len(match.Matches) == 0 {
			continue
		}
		match.Message = fileMatchMessage(match, path)
		out = append(out, match)
	}
	slices.SortFunc(out, func(a, b *fileMatch) int {
		return strings.Compare(a.FilePath, b.FilePath)
	})
	slices.Sort(skipped)
	return out, skipped, nil
}

func hasGlob(p string) bool {
	return strings.ContainsAny(p, "*?[")
}

// searchTextContent matches re against in-memory content line by line under
// the same bounds as a streamed file: at most maxMatchesPerFile lines are
// collected, with the result marked truncated past that, and a cancelled
// context stops the scan early — the caller reports the cancellation.
func searchTextContent(
	ctx context.Context,
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
		if lineNum&1023 == 0 && ctx.Err() != nil {
			return matches
		}
		if !re.MatchString(line) {
			continue
		}
		if len(matches.Matches) >= maxMatchesPerFile {
			matches.Truncated = true
			return matches
		}
		matches.Matches = append(matches.Matches, &lineMatch{
			LineNumber:  lineNum + 1,
			LineContent: line,
		})
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
				"match a regex. Skips .git and anything .gitignore "+
				"excludes; refuses a file pattern that matches too "+
				"many files and caps the lines returned, so narrow "+
				"the path or patterns when told to. Supports "+
				"workspace:// paths and artifact:// single-file refs.",
		),
	)
}

// validatePattern validates the file and content patterns.
func validatePattern(filePattern string, contentPattern string) error {
	if filePattern == "" {
		return errors.New("file pattern cannot be empty")
	}
	if contentPattern == "" {
		return errors.New("content pattern cannot be empty")
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

// newLineSplitter returns a split function that yields exactly the lines
// strings.Split(content, "\n") yields in searchTextContent, so the streamed and
// in-memory backends number lines identically and match the same patterns. It
// splits on "\n" alone, so a CRLF line keeps its "\r", and it emits the line
// after the last "\n" even when that line is empty: "foo\n" is two lines and an
// empty file is one, which is what a pattern such as "^$" relies on.
func newLineSplitter() bufio.SplitFunc {
	// pending reports that the line after the last delimiter has not been
	// emitted yet; it starts true because an empty input is still one line.
	pending := true
	return func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			pending = true
			return i + 1, data[:i], nil
		}
		if !atEOF || !pending {
			return 0, nil, nil
		}
		pending = false
		if len(data) == 0 {
			// A non-nil empty token is the final empty line; a nil token
			// would end the scan without it.
			return 0, []byte{}, nil
		}
		return len(data), data, nil
	}
}

// searchFileContent searches for content matches in a single file. It streams
// the file line by line, so its memory use is bounded by the longest line, not
// the file size — this is what lets search look inside files far larger than
// the read limit. A cancelled context aborts the scan rather than letting an
// abandoned request keep burning I/O on a huge file.
func searchFileContent(
	ctx context.Context,
	filePath string,
	re *regexp.Regexp,
) (*fileMatch, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	fileMatches := &fileMatch{Matches: []*lineMatch{}}
	sc := bufio.NewScanner(file)
	// The scanner's maximum is the token-buffer size, and a terminated line
	// occupies its length plus one for "\n"; the extra byte keeps a line of
	// exactly maxSearchLineSize bytes searchable and fails only longer ones.
	sc.Buffer(make([]byte, 64*1024), maxSearchLineSize+1)
	sc.Split(newLineSplitter())
	lineNum := 0
	for sc.Scan() {
		lineNum++
		if lineNum&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if line := sc.Text(); re.MatchString(line) {
			if len(fileMatches.Matches) >= maxMatchesPerFile {
				fileMatches.Truncated = true
				return fileMatches, nil
			}
			fileMatches.Matches = append(fileMatches.Matches, &lineMatch{
				LineNumber:  lineNum, // Line numbers are 1-based.
				LineContent: line,
			})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return fileMatches, nil
}
