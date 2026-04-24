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
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	toolBash       = "Bash"
	toolRead       = "Read"
	toolWrite      = "Write"
	toolEdit       = "Edit"
	toolGlob       = "Glob"
	toolGrep       = "Grep"
	toolWebFetch   = "WebFetch"
	toolWebSearch  = "WebSearch"
	toolTaskStop   = "TaskStop"
	toolTaskOutput = "TaskOutput"

	defaultToolSetName     = "claudecode"
	defaultGrepHeadLimit   = 250
	defaultGlobHeadLimit   = 100
	toolNotebookEdit       = "NotebookEdit"
	defaultHTTPTimeout     = 30 * time.Second
	defaultBashTimeoutMs   = 120_000
	maxBashTimeoutMs       = 600_000
	maxEditableFileSize    = 1024 * 1024 * 1024
	pdfInlineReadThreshold = 10
	pdfMaxPagesPerRead     = 20
)

var (
	envGoogleAPIKey   = strings.Join([]string{"GOOGLE", "API", "KEY"}, "_")
	envGoogleEngineID = strings.Join([]string{"GOOGLE", "SEARCH", "ENGINE", "ID"}, "_")
	ripgrepOnce       sync.Once
	ripgrepPath       string
	ripgrepLookPath   = func(file string) (string, error) { return exec.LookPath(file) }
	pdftoppmPath      string
	pdftoppmOnce      sync.Once
	pdftoppmLookPath  = func(file string) (string, error) {
		return exec.LookPath(file)
	}
)

var grepExcludedDirs = []string{
	".git",
	".svn",
	".hg",
	".bzr",
	".jj",
	".sl",
}
