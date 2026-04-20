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

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newEditTool(runtime *runtime) (tool.Tool, error) {
	return function.NewFunctionTool(
		func(_ context.Context, in editInput) (editOutput, error) {
			baseDir := runtime.currentBaseDir()
			_, absPath, err := normalizePath(baseDir, in.FilePath)
			if err != nil {
				return editOutput{}, err
			}
			runtime.fileState.mu.Lock()
			defer runtime.fileState.mu.Unlock()
			return editLocalFile(absPath, in, runtime)
		},
		function.WithName(toolEdit),
		function.WithDescription(editDescription()),
	), nil
}

func editDescription() string {
	return fmt.Sprintf(`Replace text inside an existing file.

Usage:
- Always read the file with %s before editing it.
- Use this tool for targeted string replacements, not whole-file rewrites. Use %s when you want to replace the entire file.
- old_string must match the current file contents exactly. Missing matches, stale reads, or ambiguous matches are rejected.
- This tool does not edit notebooks. Use %s for .ipynb files.`, toolRead, toolWrite, toolNotebookEdit)
}
