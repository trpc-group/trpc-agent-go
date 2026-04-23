//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceexec

// Workspace file tools.
//
// This file hosts every workspace-scoped file tool built on top of
// ExecTool: read, list, search-by-name, search-by-content, write, and
// replace. They are grouped here instead of being split across six
// per-tool files because they share the same skeleton
// (input/output/Declaration/Call), the same backend helpers in
// file_backend.go, and the same NewFileTools constructor. Keeping
// them in one place makes it easier to audit tool surface parity
// with tool/file and to reason about write-protection invariants
// (work/inputs/**, skills/**, bootstrap targets) as a whole.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// -----------------------------------------------------------------------------
// File tool set (constructor + options).
// -----------------------------------------------------------------------------

// FileToolsOptions selectively disables individual file tools when
// the caller does not want to expose the full set. The zero value
// enables all six tools, which is the recommended configuration for
// llmagent callers. Each Disable* switch is expressed explicitly
// (rather than via a bitmask) so that the API stays
// self-documenting.
type FileToolsOptions struct {
	DisableReadFile       bool
	DisableListDir        bool
	DisableSearchFile     bool
	DisableSearchContent  bool
	DisableWriteFile      bool
	DisableReplaceContent bool
}

// NewFileTools constructs the set of workspace file tools bound to
// the provided ExecTool. File tools share the same executor,
// workspace resolver, and reconciler wiring as workspace_exec, so
// they always observe a workspace that has already been converged
// to the desired state.
//
// The returned slice is freshly allocated on every call so callers
// can safely append it to their own tool list.
func NewFileTools(exec *ExecTool, opts FileToolsOptions) []tool.Tool {
	if exec == nil {
		return nil
	}
	tools := make([]tool.Tool, 0, 6)
	if !opts.DisableReadFile {
		tools = append(tools, newReadFileTool(exec))
	}
	if !opts.DisableListDir {
		tools = append(tools, newListDirTool(exec))
	}
	if !opts.DisableSearchFile {
		tools = append(tools, newSearchFileTool(exec))
	}
	if !opts.DisableSearchContent {
		tools = append(tools, newSearchContentTool(exec))
	}
	if !opts.DisableWriteFile {
		tools = append(tools, newWriteFileTool(exec))
	}
	if !opts.DisableReplaceContent {
		tools = append(tools, newReplaceContentTool(exec))
	}
	return tools
}

// -----------------------------------------------------------------------------
// workspace_read_file
// -----------------------------------------------------------------------------

// readFileTool reads a text file from the shared executor workspace.
// It is the file-tool analogue of `cat <file>` but adds line
// windowing, size capping, and UTF-8 validation so the model always
// receives a well-formed textual payload.
type readFileTool struct {
	exec *ExecTool
}

// newReadFileTool constructs the tool bound to an existing ExecTool.
// ExecTool owns the workspace resolver and reconciler so file tools
// always operate on the same workspace snapshot as workspace_exec.
func newReadFileTool(exec *ExecTool) *readFileTool {
	return &readFileTool{exec: exec}
}

type readFileInput struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	NumLines  int    `json:"num_lines,omitempty"`
	MaxBytes  int64  `json:"max_bytes,omitempty"`
}

type readFileOutput struct {
	Path       string `json:"path"`
	Contents   string `json:"contents"`
	MIMEType   string `json:"mime_type"`
	SizeBytes  int64  `json:"size_bytes"`
	Truncated  bool   `json:"truncated"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	TotalLines int    `json:"total_lines"`
}

// Declaration describes the tool schema.
func (t *readFileTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "workspace_read_file",
		Description: "Read a UTF-8 text file from the shared executor " +
			"workspace. Prefer this tool over `cat` via workspace_exec: " +
			"it enforces a size cap, validates UTF-8, and supports line " +
			"windows via start_line and num_lines. Binary files are " +
			"rejected with a clear error.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"path"},
			Properties: map[string]*tool.Schema{
				"path": {
					Type:        "string",
					Description: "Workspace-relative file path (for example work/main.py).",
				},
				"start_line": {
					Type:        "integer",
					Description: "Optional 1-based line to start reading from.",
				},
				"num_lines": {
					Type:        "integer",
					Description: "Optional maximum number of lines to return. Defaults to all remaining lines.",
				},
				"max_bytes": {
					Type:        "integer",
					Description: "Maximum number of bytes to read from the file. Defaults to 1048576 (1 MiB).",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"path":        {Type: "string"},
				"contents":    {Type: "string", Description: "Selected text window."},
				"mime_type":   {Type: "string"},
				"size_bytes":  {Type: "integer", Description: "Full file size in bytes."},
				"truncated":   {Type: "boolean", Description: "True when the tool returned fewer bytes than the file contains."},
				"start_line":  {Type: "integer"},
				"end_line":    {Type: "integer"},
				"total_lines": {Type: "integer"},
			},
		},
	}
}

// Call executes the read operation and returns the selected window.
func (t *readFileTool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.exec == nil {
		return nil, errors.New("workspace_read_file is not configured")
	}
	var in readFileInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	rel, err := cleanWorkspaceRelPath(in.Path, false)
	if err != nil {
		return nil, err
	}
	eng, ws, err := t.exec.prepareForFileTool(ctx)
	if err != nil {
		return nil, err
	}
	data, size, truncated, err := readFileLimited(
		ctx, eng, ws, rel, in.MaxBytes,
	)
	if err != nil {
		return nil, err
	}
	if err := validateTextBytes(data, rel); err != nil {
		return nil, err
	}
	body, start, end, total := sliceTextByLines(
		string(data), in.StartLine, limitWindowEnd(in.StartLine, in.NumLines),
	)
	return readFileOutput{
		Path:       rel,
		Contents:   body,
		MIMEType:   detectTextMIME(data, rel),
		SizeBytes:  size,
		Truncated:  truncated,
		StartLine:  start,
		EndLine:    end,
		TotalLines: total,
	}, nil
}

// limitWindowEnd converts a (start_line, num_lines) pair into the
// inclusive endLine expected by sliceTextByLines. Zero or negative
// limits mean "read to end of file", which is signaled to
// sliceTextByLines by passing zero.
func limitWindowEnd(startLine, numLines int) int {
	if numLines <= 0 {
		return 0
	}
	start := startLine
	if start <= 0 {
		start = 1
	}
	return start + numLines - 1
}

// -----------------------------------------------------------------------------
// workspace_list_dir
// -----------------------------------------------------------------------------

// listDirTool enumerates the direct children of a workspace
// directory. It is the file-tool analogue of `ls -la <dir>` but
// returns structured entries.
type listDirTool struct {
	exec *ExecTool
}

// newListDirTool constructs the tool bound to an existing ExecTool.
func newListDirTool(exec *ExecTool) *listDirTool {
	return &listDirTool{exec: exec}
}

type listDirInput struct {
	Path string `json:"path,omitempty"`
}

type listDirEntryOut struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

type listDirOutput struct {
	Path      string            `json:"path"`
	Entries   []listDirEntryOut `json:"entries"`
	Truncated bool              `json:"truncated"`
}

// Declaration describes the tool schema.
func (t *listDirTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "workspace_list_dir",
		Description: "List the direct children of a workspace directory. " +
			"Prefer this tool over `ls` via workspace_exec: it returns " +
			"structured entries with type and size and works consistently " +
			"across local and container executors. Use workspace_search_file " +
			"for recursive discovery.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"path": {
					Type: "string",
					Description: "Workspace-relative directory path. " +
						"Defaults to the workspace root.",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"path": {Type: "string"},
				"entries": {
					Type: "array",
					Items: &tool.Schema{
						Type: "object",
						Properties: map[string]*tool.Schema{
							"name": {Type: "string"},
							"type": {Type: "string", Description: "file, dir, symlink, or other."},
							"size": {Type: "integer"},
						},
					},
				},
				"truncated": {Type: "boolean"},
			},
		},
	}
}

// Call executes the listing.
func (t *listDirTool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.exec == nil {
		return nil, errors.New("workspace_list_dir is not configured")
	}
	var in listDirInput
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
	}
	rel, err := cleanWorkspaceRelPath(in.Path, true)
	if err != nil {
		return nil, err
	}
	eng, ws, err := t.exec.prepareForFileTool(ctx)
	if err != nil {
		return nil, err
	}
	entries, truncated, err := listDirEntries(ctx, eng, ws, rel)
	if err != nil {
		return nil, err
	}
	out := listDirOutput{
		Path:      rel,
		Entries:   make([]listDirEntryOut, 0, len(entries)),
		Truncated: truncated,
	}
	for _, e := range entries {
		out.Entries = append(out.Entries, listDirEntryOut{
			Name: e.Name,
			Type: e.Type,
			Size: e.Size,
		})
	}
	return out, nil
}

// -----------------------------------------------------------------------------
// workspace_search_file
// -----------------------------------------------------------------------------

// searchFileTool recursively searches the workspace for files whose
// paths match a doublestar glob. It is the file-tool analogue of
// `find . -name ...` and avoids the fragility of parsing shell
// output from a generic find invocation.
type searchFileTool struct {
	exec *ExecTool
}

// newSearchFileTool constructs the tool bound to an existing ExecTool.
func newSearchFileTool(exec *ExecTool) *searchFileTool {
	return &searchFileTool{exec: exec}
}

type searchFileInput struct {
	Path       string `json:"path,omitempty"`
	Pattern    string `json:"pattern"`
	MaxResults int    `json:"max_results,omitempty"`
	FilesOnly  bool   `json:"files_only,omitempty"`
}

type searchFileMatch struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

type searchFileOutput struct {
	Root      string            `json:"root"`
	Matches   []searchFileMatch `json:"matches"`
	Truncated bool              `json:"truncated"`
}

// Declaration describes the tool schema.
func (t *searchFileTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "workspace_search_file",
		Description: "Recursively find workspace files whose path matches " +
			"a doublestar glob pattern. Use ** to match across directory " +
			"levels, for example '**/*.go' or 'work/**/input*.csv'. " +
			"Prefer this tool over `find` via workspace_exec because it " +
			"returns structured results and works consistently across " +
			"backends.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"pattern"},
			Properties: map[string]*tool.Schema{
				"path": {
					Type: "string",
					Description: "Workspace-relative directory to search. " +
						"Defaults to the workspace root.",
				},
				"pattern": {
					Type:        "string",
					Description: "Doublestar glob pattern matched against the path relative to `path`.",
				},
				"files_only": {
					Type:        "boolean",
					Description: "When true, directory entries are filtered out.",
				},
				"max_results": {
					Type:        "integer",
					Description: "Maximum number of matches to return. Defaults to 200.",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"root": {Type: "string"},
				"matches": {
					Type: "array",
					Items: &tool.Schema{
						Type: "object",
						Properties: map[string]*tool.Schema{
							"path": {Type: "string"},
							"type": {Type: "string"},
							"size": {Type: "integer"},
						},
					},
				},
				"truncated": {Type: "boolean"},
			},
		},
	}
}

// Call executes the recursive search.
func (t *searchFileTool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.exec == nil {
		return nil, errors.New("workspace_search_file is not configured")
	}
	var in searchFileInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return nil, errors.New("pattern is required")
	}
	root, err := cleanWorkspaceRelPath(in.Path, true)
	if err != nil {
		return nil, err
	}
	eng, ws, err := t.exec.prepareForFileTool(ctx)
	if err != nil {
		return nil, err
	}
	entries, treeTruncated, err := listTreeAll(ctx, eng, ws, root)
	if err != nil {
		return nil, err
	}
	limit := in.MaxResults
	if limit <= 0 {
		limit = 200
	}
	out := searchFileOutput{
		Root:    root,
		Matches: []searchFileMatch{},
		// Propagate tree-walk truncation up-front: even when
		// max_results does not fire, the results may be incomplete
		// because the directory walk itself was clipped at
		// maxListingEntries. Pre-setting this flag keeps the model
		// honest about the possibility.
		Truncated: treeTruncated,
	}
	for _, e := range entries {
		if in.FilesOnly && e.Type != "file" {
			continue
		}
		if !matchGlob(in.Pattern, e.Name) {
			continue
		}
		full := e.Name
		if root != "." {
			full = path.Join(root, e.Name)
		}
		out.Matches = append(out.Matches, searchFileMatch{
			Path: full,
			Type: e.Type,
			Size: e.Size,
		})
		if len(out.Matches) >= limit {
			out.Truncated = true
			break
		}
	}
	return out, nil
}

// -----------------------------------------------------------------------------
// workspace_search_content
// -----------------------------------------------------------------------------

// searchContentTool scans workspace files for a regex and returns
// matching line previews. It is the file-tool analogue of
// `grep -rn` and removes the need for the model to write brittle
// grep invocations.
type searchContentTool struct {
	exec *ExecTool
}

// newSearchContentTool constructs the tool bound to an existing ExecTool.
func newSearchContentTool(exec *ExecTool) *searchContentTool {
	return &searchContentTool{exec: exec}
}

type searchContentInput struct {
	Path            string `json:"path,omitempty"`
	Pattern         string `json:"pattern"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
	FileGlob        string `json:"file_glob,omitempty"`
	MaxMatches      int    `json:"max_matches,omitempty"`
	MaxFiles        int    `json:"max_files,omitempty"`
	MaxBytes        int64  `json:"max_bytes,omitempty"`
}

type searchContentMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Preview string `json:"preview"`
}

type searchContentOutput struct {
	Root         string               `json:"root"`
	Matches      []searchContentMatch `json:"matches"`
	FilesScanned int                  `json:"files_scanned"`
	FilesSkipped int                  `json:"files_skipped"`
	FilesPartial int                  `json:"files_partial"`
	Truncated    bool                 `json:"truncated"`
}

// Declaration describes the tool schema.
func (t *searchContentTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "workspace_search_content",
		Description: "Search the contents of UTF-8 text files in the " +
			"workspace for a regular expression and return matching lines. " +
			"Use file_glob to restrict the search to specific files, for " +
			"example '**/*.go' or 'work/**/*.csv'. Prefer this tool over " +
			"`grep` via workspace_exec because it handles text validation, " +
			"result capping, and binary-file skipping for you.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"pattern"},
			Properties: map[string]*tool.Schema{
				"path": {
					Type:        "string",
					Description: "Workspace-relative directory to search. Defaults to the workspace root.",
				},
				"pattern":          {Type: "string", Description: "RE2 regular expression."},
				"case_insensitive": {Type: "boolean", Description: "When true, the regex is matched case-insensitively."},
				"file_glob": {
					Type:        "string",
					Description: "Optional doublestar glob that matching file paths must satisfy.",
				},
				"max_matches": {Type: "integer", Description: "Maximum number of matches across all files. Defaults to 100."},
				"max_files":   {Type: "integer", Description: "Maximum number of files to inspect. Defaults to 200."},
				"max_bytes":   {Type: "integer", Description: "Maximum number of bytes to read per file. Defaults to 1048576 (1 MiB)."},
			},
		},
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"root": {Type: "string"},
				"matches": {
					Type: "array",
					Items: &tool.Schema{
						Type: "object",
						Properties: map[string]*tool.Schema{
							"path":    {Type: "string"},
							"line":    {Type: "integer"},
							"preview": {Type: "string"},
						},
					},
				},
				"files_scanned": {Type: "integer"},
				"files_skipped": {Type: "integer", Description: "Number of files skipped because they were not UTF-8 text or could not be read."},
				"files_partial": {Type: "integer", Description: "Number of files scanned only up to max_bytes; their matches beyond that offset are not reported."},
				"truncated":     {Type: "boolean", Description: "True when any of the result limits fired, the tree walk was clipped, or at least one file was only scanned partially."},
			},
		},
	}
}

// Call executes the recursive content search.
func (t *searchContentTool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.exec == nil {
		return nil, errors.New("workspace_search_content is not configured")
	}
	var in searchContentInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return nil, errors.New("pattern is required")
	}
	rx, err := compileContentRegex(in.Pattern, in.CaseInsensitive)
	if err != nil {
		return nil, err
	}
	root, err := cleanWorkspaceRelPath(in.Path, true)
	if err != nil {
		return nil, err
	}
	eng, ws, err := t.exec.prepareForFileTool(ctx)
	if err != nil {
		return nil, err
	}
	entries, treeTruncated, err := listTreeAll(ctx, eng, ws, root)
	if err != nil {
		return nil, err
	}
	maxMatches := in.MaxMatches
	if maxMatches <= 0 {
		maxMatches = 100
	}
	maxFiles := in.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 200
	}
	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultFileToolMaxBytes
	}
	out := searchContentOutput{
		Root:      root,
		Matches:   []searchContentMatch{},
		Truncated: treeTruncated,
	}
	for _, e := range entries {
		if e.Type != "file" {
			continue
		}
		if in.FileGlob != "" && !matchGlob(in.FileGlob, e.Name) {
			continue
		}
		if out.FilesScanned >= maxFiles {
			out.Truncated = true
			break
		}
		full := e.Name
		if root != "." {
			full = path.Join(root, e.Name)
		}
		data, _, fileTruncated, err := readFileLimited(
			ctx, eng, ws, full, maxBytes,
		)
		if err != nil {
			out.FilesSkipped++
			continue
		}
		if err := validateTextBytes(data, full); err != nil {
			out.FilesSkipped++
			continue
		}
		out.FilesScanned++
		if fileTruncated {
			// The file exceeded max_bytes; we are about to scan only
			// the prefix. Book-keep the partial scan so the caller
			// knows the match list is incomplete for this file, and
			// mark the overall response truncated so downstream
			// reasoning does not treat a clean result as proof of
			// absence.
			out.FilesPartial++
			out.Truncated = true
		}
		remaining := maxMatches - len(out.Matches)
		if remaining <= 0 {
			out.Truncated = true
			break
		}
		hits := scanContentLines(rx, string(data), remaining, 240)
		for _, h := range hits {
			out.Matches = append(out.Matches, searchContentMatch{
				Path:    full,
				Line:    h.Line,
				Preview: h.Preview,
			})
		}
		if len(out.Matches) >= maxMatches {
			out.Truncated = true
			break
		}
	}
	return out, nil
}

// -----------------------------------------------------------------------------
// workspace_write_file
// -----------------------------------------------------------------------------

// writeFileTool creates or overwrites a UTF-8 text file inside the
// workspace. It is the file-tool analogue of `printf ... > file` and
// enforces two invariants that the shell form does not:
//
//  1. The target must not resolve to a framework-managed path such
//     as work/inputs/**, skills/**, or a WorkspaceBootstrapSpec.Files
//     target. These paths are owned by the reconciler and must not be
//     mutated out of band.
//  2. Unless overwrite=true, an existing file is preserved. This
//     matches the "read-before-edit" semantics that other agent
//     ecosystems expose and prevents silent loss of prior state.
type writeFileTool struct {
	exec *ExecTool
}

// newWriteFileTool constructs the tool bound to an existing ExecTool.
func newWriteFileTool(exec *ExecTool) *writeFileTool {
	return &writeFileTool{exec: exec}
}

type writeFileInput struct {
	Path      string `json:"path"`
	Contents  string `json:"contents"`
	Overwrite bool   `json:"overwrite,omitempty"`
	Mode      uint32 `json:"mode,omitempty"`
}

type writeFileOutput struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytes_written"`
	Overwritten  bool   `json:"overwritten"`
	MIMEType     string `json:"mime_type"`
}

// Declaration describes the tool schema.
func (t *writeFileTool) Declaration() *tool.Declaration {
	return writeFileDeclaration()
}

// writeFileDeclaration is factored into a helper so the tool schema
// can be reused by external wiring (for example, prompt generation)
// without invoking the tool itself.
func writeFileDeclaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "workspace_write_file",
		Description: "Create or overwrite a UTF-8 text file in the " +
			"shared executor workspace. Refuses to write into framework-" +
			"managed paths (work/inputs/**, skills/**, or declared " +
			"bootstrap file targets) and refuses to overwrite an existing " +
			"file unless overwrite=true.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"path", "contents"},
			Properties: map[string]*tool.Schema{
				"path": {
					Type:        "string",
					Description: "Workspace-relative file path.",
				},
				"contents": {
					Type:        "string",
					Description: "File contents as UTF-8 text.",
				},
				"overwrite": {
					Type:        "boolean",
					Description: "When true, replaces the file if it already exists.",
				},
				"mode": {
					Type:        "integer",
					Description: "Optional POSIX mode (for example 420 for 0644). Defaults to 0644.",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"path":          {Type: "string"},
				"bytes_written": {Type: "integer"},
				"overwritten":   {Type: "boolean"},
				"mime_type":     {Type: "string"},
			},
		},
	}
}

// Call executes the write.
func (t *writeFileTool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.exec == nil {
		return nil, errors.New("workspace_write_file is not configured")
	}
	var in writeFileInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	rel, err := cleanWorkspaceRelPath(in.Path, false)
	if err != nil {
		return nil, err
	}
	if isBuiltInProtectedWorkspacePath(rel) || t.exec.isBootstrapProtectedPath(rel) {
		return nil, fmt.Errorf(
			"path %q is managed by the framework and cannot be written through workspace_write_file",
			rel,
		)
	}
	data := []byte(in.Contents)
	if err := validateTextBytes(data, rel); err != nil {
		return nil, err
	}
	eng, ws, err := t.exec.prepareForFileTool(ctx)
	if err != nil {
		return nil, err
	}
	exists, err := workspacePathExists(ctx, eng, ws, rel)
	if err != nil {
		return nil, err
	}
	if exists && !in.Overwrite {
		return nil, fmt.Errorf(
			"file already exists and overwrite=false: %s", rel,
		)
	}
	mode := in.Mode
	if mode == 0 {
		mode = defaultFileToolMode
	}
	if err := writeFileBytes(ctx, eng, ws, rel, data, mode); err != nil {
		return nil, err
	}
	return writeFileOutput{
		Path:         rel,
		BytesWritten: len(data),
		Overwritten:  exists,
		MIMEType:     detectTextMIME(data, rel),
	}, nil
}

// -----------------------------------------------------------------------------
// workspace_replace_content
// -----------------------------------------------------------------------------

// replaceContentTool performs an in-place text substitution on a
// workspace file. It is the file-tool analogue of
// `sed -i 's/old/new/'` but:
//
//  - keeps substitution literal by default (no accidental regex
//    meta interpretation);
//  - requires the file to already contain the search string:
//    missing matches surface as an explicit error so the model
//    cannot mistake a silent no-match for a successful edit. This
//    is stricter than tool/file.replace_content (which reports
//    "not found" via message + nil error); workspace_replace_content
//    deliberately diverges here because the shared workspace edit
//    loop is less forgiving of no-op "successes";
//  - treats old_string == new_string as a success no-op, matching
//    tool/file.replace_content;
//  - refuses to modify framework-managed paths.
type replaceContentTool struct {
	exec *ExecTool
}

// newReplaceContentTool constructs the tool bound to an existing ExecTool.
func newReplaceContentTool(exec *ExecTool) *replaceContentTool {
	return &replaceContentTool{exec: exec}
}

type replaceContentInput struct {
	Path            string `json:"path"`
	OldString       string `json:"old_string"`
	NewString       string `json:"new_string"`
	NumReplacements int    `json:"num_replacements,omitempty"`
	Regex           bool   `json:"regex,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
	MaxBytes        int64  `json:"max_bytes,omitempty"`
}

type replaceContentOutput struct {
	Path            string `json:"path"`
	NumReplacements int    `json:"num_replacements"`
	TotalMatches    int    `json:"total_matches"`
	BytesWritten    int    `json:"bytes_written"`
}

// Declaration describes the tool schema.
func (t *replaceContentTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "workspace_replace_content",
		Description: "Replace text inside a workspace file in place. " +
			"By default performs a literal, single-occurrence replacement; " +
			"set regex=true to interpret old_string as an RE2 pattern and " +
			"num_replacements<0 to replace every occurrence. Refuses to " +
			"modify framework-managed paths (work/inputs/**, skills/**, " +
			"and declared bootstrap file targets).",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"path", "old_string", "new_string"},
			Properties: map[string]*tool.Schema{
				"path":             {Type: "string", Description: "Workspace-relative file path."},
				"old_string":       {Type: "string", Description: "Literal text to find, or RE2 pattern when regex=true."},
				"new_string":       {Type: "string", Description: "Replacement text."},
				"num_replacements": {Type: "integer", Description: "Optional replacement limit; 0 means 1 and negative means replace all matches."},
				"regex":            {Type: "boolean", Description: "When true, interpret `old_string` as an RE2 regex."},
				"case_insensitive": {Type: "boolean", Description: "When true combined with regex, matching is case-insensitive."},
				"max_bytes":        {Type: "integer", Description: "Maximum bytes to read when loading the file. Defaults to 1048576 (1 MiB)."},
			},
		},
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"path":             {Type: "string"},
				"num_replacements": {Type: "integer", Description: "Number of occurrences actually replaced."},
				"total_matches":    {Type: "integer", Description: "Number of occurrences of old_string found in the file."},
				"bytes_written":    {Type: "integer"},
			},
		},
	}
}

// Call executes the substitution.
func (t *replaceContentTool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.exec == nil {
		return nil, errors.New("workspace_replace_content is not configured")
	}
	var in replaceContentInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if in.OldString == "" {
		return nil, errors.New("old_string must not be empty")
	}
	rel, err := cleanWorkspaceRelPath(in.Path, false)
	if err != nil {
		return nil, err
	}
	if isBuiltInProtectedWorkspacePath(rel) || t.exec.isBootstrapProtectedPath(rel) {
		return nil, fmt.Errorf(
			"path %q is managed by the framework and cannot be written through workspace_replace_content",
			rel,
		)
	}
	if in.OldString == in.NewString {
		// Parity with tool/file.replace_content: a rewrite that
		// would produce identical content is reported as a
		// successful no-op instead of an error. We still validate
		// the path above so obviously bad calls do not get a
		// misleading success.
		return replaceContentOutput{Path: rel}, nil
	}
	eng, ws, err := t.exec.prepareForFileTool(ctx)
	if err != nil {
		return nil, err
	}
	data, _, truncated, err := readFileLimited(ctx, eng, ws, rel, in.MaxBytes)
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, fmt.Errorf(
			"file %q exceeds the configured max_bytes and cannot be safely replaced",
			rel,
		)
	}
	if err := validateTextBytes(data, rel); err != nil {
		return nil, err
	}
	updated, applied, total, err := applyReplacement(string(data), in)
	if err != nil {
		return nil, err
	}
	if total == 0 {
		return nil, fmt.Errorf(
			"no occurrences of the search pattern were found in %s", rel,
		)
	}
	if err := writeFileBytes(
		ctx, eng, ws, rel, []byte(updated), defaultFileToolMode,
	); err != nil {
		return nil, err
	}
	return replaceContentOutput{
		Path:            rel,
		NumReplacements: applied,
		TotalMatches:    total,
		BytesWritten:    len(updated),
	}, nil
}

// applyReplacement performs the literal or regex-based substitution
// and returns the updated content, the number of replacements
// actually applied, and the total number of matches found. The
// num_replacements input is interpreted the same way the existing
// tool/file.replace_content does: 0 means 1, and any negative value
// means "replace every occurrence". Positive values are clamped to
// the total match count so the caller always learns the real
// upper bound.
func applyReplacement(
	src string, in replaceContentInput,
) (string, int, int, error) {
	limit := in.NumReplacements
	if limit == 0 {
		limit = 1
	}

	if in.Regex {
		rx, err := compileContentRegex(in.OldString, in.CaseInsensitive)
		if err != nil {
			return "", 0, 0, err
		}
		all := rx.FindAllStringIndex(src, -1)
		total := len(all)
		if total == 0 {
			return src, 0, 0, nil
		}
		if limit < 0 || limit > total {
			limit = total
		}
		if limit == total {
			return rx.ReplaceAllString(src, in.NewString), limit, total, nil
		}
		var b strings.Builder
		b.Grow(len(src))
		prev := 0
		for i := 0; i < limit; i++ {
			loc := all[i]
			b.WriteString(src[prev:loc[0]])
			match := src[loc[0]:loc[1]]
			b.WriteString(rx.ReplaceAllString(match, in.NewString))
			prev = loc[1]
		}
		b.WriteString(src[prev:])
		return b.String(), limit, total, nil
	}

	if in.CaseInsensitive {
		rx := regexp.MustCompile("(?i)" + regexp.QuoteMeta(in.OldString))
		all := rx.FindAllStringIndex(src, -1)
		total := len(all)
		if total == 0 {
			return src, 0, 0, nil
		}
		if limit < 0 || limit > total {
			limit = total
		}
		if limit == total {
			return rx.ReplaceAllLiteralString(src, in.NewString), limit, total, nil
		}
		var b strings.Builder
		b.Grow(len(src))
		prev := 0
		for i := 0; i < limit; i++ {
			loc := all[i]
			b.WriteString(src[prev:loc[0]])
			b.WriteString(in.NewString)
			prev = loc[1]
		}
		b.WriteString(src[prev:])
		return b.String(), limit, total, nil
	}

	total := strings.Count(src, in.OldString)
	if total == 0 {
		return src, 0, 0, nil
	}
	if limit < 0 || limit > total {
		limit = total
	}
	return strings.Replace(src, in.OldString, in.NewString, limit), limit, total, nil
}
