//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package claudecode

import (
	"context"
	"os"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type compositeToolSet struct {
	name  string
	tools []tool.Tool
}

type runtime struct {
	mu          sync.RWMutex
	baseDir     string
	maxFileSize int64
	fileState   *fileState
	taskState   *taskState
}

type fileView struct {
	Content       string
	Timestamp     int64
	Offset        *int
	Limit         *int
	Pages         string
	IsPartialView bool
	FromRead      bool
}

type fileState struct {
	mu    sync.Mutex
	views map[string]fileView
}

type taskState struct {
	mu    sync.Mutex
	tasks map[string]*backgroundTask
}

type backgroundTask struct {
	ID         string
	Command    string
	Type       string
	OutputPath string
	Process    *os.Process
	Status     string
	ExitCode   *int
}

type localFileSnapshot struct {
	Exists       bool
	Path         string
	Raw          []byte
	Content      string
	Mode         os.FileMode
	Timestamp    int64
	Encoding     string
	LineEnding   string
	MediaType    string
	OriginalSize int64
}

type patchHunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}

type bashInput struct {
	Command         string `json:"command"`
	Timeout         *int   `json:"timeout,omitempty"`
	RunInBackground bool   `json:"run_in_background,omitempty"`
}

type bashOutput struct {
	Command          string `json:"command"`
	ExitCode         int    `json:"exitCode"`
	Stdout           string `json:"stdout,omitempty"`
	Stderr           string `json:"stderr,omitempty"`
	Output           string `json:"output,omitempty"`
	DurationMs       int64  `json:"durationMs"`
	TimedOut         bool   `json:"timedOut,omitempty"`
	BackgroundTaskID string `json:"taskId,omitempty"`
	OutputPath       string `json:"outputPath,omitempty"`
}

type taskStopInput struct {
	TaskID  string `json:"task_id,omitempty"`
	ShellID string `json:"shell_id,omitempty"`
}

type taskStopOutput struct {
	Message  string `json:"message"`
	TaskID   string `json:"task_id"`
	TaskType string `json:"task_type"`
	Command  string `json:"command,omitempty"`
}

type taskOutputInput struct {
	TaskID  string `json:"task_id"`
	Block   *bool  `json:"block,omitempty"`
	Timeout *int   `json:"timeout,omitempty"`
}

type taskOutputTask struct {
	TaskID      string `json:"task_id"`
	TaskType    string `json:"task_type"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Output      string `json:"output"`
	ExitCode    *int   `json:"exitCode,omitempty"`
	Error       string `json:"error,omitempty"`
}

type taskOutputOutput struct {
	RetrievalStatus string          `json:"retrieval_status"`
	Task            *taskOutputTask `json:"task"`
}

type readInput struct {
	FilePath string `json:"file_path"`
	Offset   *int   `json:"offset,omitempty"`
	Limit    *int   `json:"limit,omitempty"`
	Pages    string `json:"pages,omitempty"`
}

type readFile struct {
	FilePath     string           `json:"filePath,omitempty"`
	Content      string           `json:"content,omitempty"`
	NumLines     int              `json:"numLines,omitempty"`
	StartLine    int              `json:"startLine,omitempty"`
	TotalLines   int              `json:"totalLines,omitempty"`
	Base64       string           `json:"base64,omitempty"`
	Type         string           `json:"type,omitempty"`
	MediaType    string           `json:"mediaType,omitempty"`
	OriginalSize int64            `json:"originalSize,omitempty"`
	Count        int              `json:"count,omitempty"`
	OutputDir    string           `json:"outputDir,omitempty"`
	Cells        []map[string]any `json:"cells,omitempty"`
}

type readOutput struct {
	Type string    `json:"type"`
	File *readFile `json:"file,omitempty"`
}

type writeInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

type writeOutput struct {
	Type            string      `json:"type"`
	FilePath        string      `json:"filePath"`
	Content         string      `json:"content"`
	StructuredPatch []patchHunk `json:"structuredPatch"`
	OriginalFile    *string     `json:"originalFile"`
}

type editInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type editOutput struct {
	FilePath        string      `json:"filePath"`
	OldString       string      `json:"oldString"`
	NewString       string      `json:"newString"`
	OriginalFile    string      `json:"originalFile"`
	StructuredPatch []patchHunk `json:"structuredPatch"`
	UserModified    bool        `json:"userModified"`
	ReplaceAll      bool        `json:"replaceAll"`
}

type globInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

type globOutput struct {
	DurationMs int64    `json:"durationMs"`
	NumFiles   int      `json:"numFiles"`
	Filenames  []string `json:"filenames"`
	Truncated  bool     `json:"truncated"`
}

type grepInput struct {
	Pattern     string `json:"pattern"`
	Path        string `json:"path,omitempty"`
	Glob        string `json:"glob,omitempty"`
	OutputMode  string `json:"output_mode,omitempty"`
	Before      *int   `json:"-B,omitempty"`
	After       *int   `json:"-A,omitempty"`
	Context     *int   `json:"-C,omitempty"`
	ContextAlt  *int   `json:"context,omitempty"`
	ShowLineNum *bool  `json:"-n,omitempty"`
	IgnoreCase  *bool  `json:"-i,omitempty"`
	Type        string `json:"type,omitempty"`
	HeadLimit   *int   `json:"head_limit,omitempty"`
	Offset      *int   `json:"offset,omitempty"`
	Multiline   bool   `json:"multiline,omitempty"`
}

type grepOutput struct {
	Mode          string   `json:"mode,omitempty"`
	NumFiles      int      `json:"numFiles"`
	Filenames     []string `json:"filenames"`
	Content       string   `json:"content,omitempty"`
	NumLines      int      `json:"numLines,omitempty"`
	NumMatches    int      `json:"numMatches,omitempty"`
	AppliedLimit  *int     `json:"appliedLimit,omitempty"`
	AppliedOffset int      `json:"appliedOffset,omitempty"`
}

type webFetchInput struct {
	URL    string `json:"url"`
	Prompt string `json:"prompt"`
}

type webFetchOutput struct {
	Bytes      int    `json:"bytes"`
	Code       int    `json:"code"`
	CodeText   string `json:"codeText"`
	Result     string `json:"result"`
	DurationMs int64  `json:"durationMs"`
	URL        string `json:"url"`
}

type webSearchInput struct {
	Query          string   `json:"query"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

type webSearchHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type webSearchResult struct {
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   []webSearchHit `json:"content,omitempty"`
	Text      string         `json:"text,omitempty"`
}

type webSearchOutput struct {
	Query           string            `json:"query"`
	Results         []webSearchResult `json:"results"`
	DurationSeconds float64           `json:"durationSeconds"`
}

func (s *compositeToolSet) Tools(ctx context.Context) []tool.Tool {
	out := make([]tool.Tool, 0, len(s.tools))
	out = append(out, s.tools...)
	return out
}

func (s *compositeToolSet) Close() error {
	return nil
}

func (s *compositeToolSet) Name() string {
	return s.name
}
