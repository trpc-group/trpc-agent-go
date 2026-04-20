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
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newGlobTool(runtime *runtime) (tool.Tool, error) {
	return function.NewFunctionTool(
		func(_ context.Context, in globInput) (globOutput, error) {
			baseDir := runtime.currentBaseDir()
			start := time.Now()
			searchDir := baseDir
			searchRel := ""
			if in.Path != "" {
				relPath, absPath, err := normalizePath(baseDir, in.Path)
				if err != nil {
					return globOutput{}, err
				}
				info, err := os.Stat(absPath)
				if err != nil {
					if os.IsNotExist(err) {
						return globOutput{}, fmt.Errorf("Directory does not exist: %s", in.Path)
					}
					return globOutput{}, err
				}
				if !info.IsDir() {
					return globOutput{}, fmt.Errorf("Path is not a directory: %s", in.Path)
				}
				searchDir = absPath
				searchRel = relPath
			}
			matches, err := doublestar.Glob(os.DirFS(searchDir), in.Pattern, doublestar.WithCaseInsensitive())
			if err != nil {
				return globOutput{}, err
			}
			sorted := sortedCopy(matches)
			truncated := false
			if len(sorted) > defaultGlobHeadLimit {
				sorted = sorted[:defaultGlobHeadLimit]
				truncated = true
			}
			filenames := make([]string, 0, len(sorted))
			for _, match := range sorted {
				fullRel := match
				if searchRel != "" {
					fullRel = filepath.ToSlash(filepath.Join(searchRel, match))
				}
				filenames = append(filenames, filepath.ToSlash(filepath.Clean(fullRel)))
			}
			return globOutput{
				DurationMs: max(time.Since(start).Milliseconds(), 1),
				NumFiles:   len(filenames),
				Filenames:  filenames,
				Truncated:  truncated,
			}, nil
		},
		function.WithName(toolGlob),
		function.WithDescription(globDescription()),
	), nil
}

func globDescription() string {
	return fmt.Sprintf(`Fast file pattern matching for workspace paths.

Usage:
- Use %s to find files by name or path pattern.
- pattern uses doublestar-style globs such as "*.go" or "**/*.ts".
- path optionally narrows the search to a specific directory.
- Results are sorted, and the tool returns at most %d filenames per call.
- Use %s instead when you need to search file contents rather than file names.`, toolGlob, defaultGlobHeadLimit, toolGrep)
}
