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

func newWriteTool(runtime *runtime) (tool.Tool, error) {
	return function.NewFunctionTool(
		func(_ context.Context, in writeInput) (writeOutput, error) {
			baseDir := runtime.currentBaseDir()
			_, absPath, err := normalizePath(baseDir, in.FilePath)
			if err != nil {
				return writeOutput{}, err
			}
			runtime.fileState.mu.Lock()
			defer runtime.fileState.mu.Unlock()
			snapshot, err := readLocalFileSnapshot(absPath, runtime.maxFileSize)
			if err != nil {
				return writeOutput{}, err
			}
			writeType := "create"
			var originalFile *string
			encoding := "utf8"
			lineEnding := "\n"
			mode := snapshot.Mode
			if snapshot.Exists {
				writeType = "update"
				originalFile = &snapshot.Content
				encoding = snapshot.Encoding
				lineEnding = snapshot.LineEnding
				if err := ensureWriteAllowed(absPath, snapshot, runtime.fileState); err != nil {
					return writeOutput{}, err
				}
			}
			if err := writeLocalFile(absPath, in.Content, mode, encoding, lineEnding); err != nil {
				return writeOutput{}, err
			}
			current, err := readLocalFileSnapshot(absPath, runtime.maxFileSize)
			if err != nil {
				return writeOutput{}, err
			}
			storeReadView(runtime.fileState, absPath, current.Content, current.Timestamp, nil, nil, "", false, false)
			previous := ""
			if originalFile != nil {
				previous = *originalFile
			}
			return writeOutput{
				Type:            writeType,
				FilePath:        absPath,
				Content:         in.Content,
				StructuredPatch: buildStructuredPatch(previous, in.Content),
				OriginalFile:    originalFile,
			}, nil
		},
		function.WithName(toolWrite),
		function.WithDescription(writeDescription()),
	), nil
}

func writeDescription() string {
	return fmt.Sprintf(`Create or overwrite a file.

Usage:
- Use %s when you want to replace the whole file or create a brand-new file.
- When overwriting an existing file, read it with %s first. A partial read is not enough to authorize a full overwrite.
- Prefer %s when you only need a targeted replacement inside an existing text file.
- Prefer %s when editing .ipynb files instead of rewriting notebook JSON manually.
- Updates preserve the existing file mode, encoding, and line endings when possible.`, toolWrite, toolRead, toolEdit, toolNotebookEdit)
}
