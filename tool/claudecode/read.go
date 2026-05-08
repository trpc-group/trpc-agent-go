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
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newReadTool(runtime *runtime) (tool.Tool, error) {
	return function.NewFunctionTool(
		func(_ context.Context, in readInput) (readOutput, error) {
			baseDir := runtime.currentBaseDir()
			_, absPath, err := normalizePath(baseDir, in.FilePath)
			if err != nil {
				return readOutput{}, err
			}
			snapshot, err := readLocalFileSnapshot(absPath, runtime.maxFileSize)
			if err != nil {
				return readOutput{}, err
			}
			if !snapshot.Exists {
				return readOutput{}, fmt.Errorf("File does not exist: %s", in.FilePath)
			}
			runtime.fileState.mu.Lock()
			existing, ok := runtime.fileState.views[absPath]
			if ok && existing.Timestamp == snapshot.Timestamp && matchesReadView(existing, in.Offset, in.Limit, in.Pages) {
				runtime.fileState.mu.Unlock()
				return readOutput{
					Type: "file_unchanged",
					File: &readFile{
						FilePath: absPath,
					},
				}, nil
			}
			runtime.fileState.mu.Unlock()
			ext := strings.ToLower(absPath)
			switch {
			case strings.HasSuffix(ext, ".ipynb"):
				return readNotebook(runtime, snapshot, in)
			case strings.HasSuffix(ext, ".pdf"):
				return readPDF(runtime, snapshot, in)
			case strings.HasPrefix(snapshot.MediaType, "image/"):
				return readImage(runtime, snapshot, in)
			default:
				if isProbablyBinary(snapshot.Raw) {
					return readOutput{}, fmt.Errorf("This tool cannot read binary files.")
				}
				return readText(runtime, snapshot, in)
			}
		},
		function.WithName(toolRead),
		function.WithDescription(readDescription()),
	), nil
}

func readText(runtime *runtime, snapshot localFileSnapshot, in readInput) (readOutput, error) {
	startLine := 1
	if in.Offset != nil && *in.Offset > 0 {
		startLine = *in.Offset
	}
	content, actualStartLine, totalLines := sliceLines(snapshot.Content, startLine, in.Limit)
	runtime.fileState.mu.Lock()
	storeReadView(runtime.fileState, snapshot.Path, content, snapshot.Timestamp, in.Offset, in.Limit, in.Pages, in.Limit != nil || startLine > 1, true)
	runtime.fileState.mu.Unlock()
	return readOutput{
		Type: "text",
		File: &readFile{
			FilePath:   snapshot.Path,
			Content:    content,
			NumLines:   countLines(content),
			StartLine:  actualStartLine,
			TotalLines: totalLines,
		},
	}, nil
}

func readNotebook(runtime *runtime, snapshot localFileSnapshot, in readInput) (readOutput, error) {
	var notebook struct {
		Cells []map[string]any `json:"cells"`
	}
	if err := json.Unmarshal(snapshot.Raw, &notebook); err != nil {
		return readOutput{}, err
	}
	runtime.fileState.mu.Lock()
	storeReadView(runtime.fileState, snapshot.Path, snapshot.Content, snapshot.Timestamp, in.Offset, in.Limit, in.Pages, false, true)
	runtime.fileState.mu.Unlock()
	return readOutput{
		Type: "notebook",
		File: &readFile{
			FilePath: snapshot.Path,
			Cells:    notebook.Cells,
		},
	}, nil
}

func readPDF(runtime *runtime, snapshot localFileSnapshot, in readInput) (readOutput, error) {
	pageCount, err := pdfPageCount(snapshot.Raw)
	if err != nil {
		return readOutput{}, err
	}
	if strings.TrimSpace(in.Pages) != "" {
		pageRange, rangeErr := resolvePDFPageRange(in.Pages, pageCount)
		if rangeErr != nil {
			return readOutput{}, rangeErr
		}
		outputDir, renderedCount, extractErr := extractPDFPages(snapshot.Path, pageRange)
		if extractErr != nil {
			return readOutput{}, extractErr
		}
		runtime.fileState.mu.Lock()
		storeReadView(runtime.fileState, snapshot.Path, snapshot.Content, snapshot.Timestamp, in.Offset, in.Limit, in.Pages, true, true)
		runtime.fileState.mu.Unlock()
		return readOutput{
			Type: "parts",
			File: &readFile{
				FilePath:     snapshot.Path,
				OriginalSize: snapshot.OriginalSize,
				Count:        renderedCount,
				OutputDir:    outputDir,
			},
		}, nil
	}
	if pageCount > pdfInlineReadThreshold {
		return readOutput{}, fmt.Errorf("This PDF has %d pages, which is too many to read at once. Use the pages parameter to read specific page ranges (e.g., pages: \"1-5\"). Maximum %d pages per request.", pageCount, pdfMaxPagesPerRead)
	}
	runtime.fileState.mu.Lock()
	storeReadView(runtime.fileState, snapshot.Path, snapshot.Content, snapshot.Timestamp, in.Offset, in.Limit, in.Pages, false, true)
	runtime.fileState.mu.Unlock()
	return readOutput{
		Type: "pdf",
		File: &readFile{
			FilePath:     snapshot.Path,
			Base64:       fileBase64(snapshot.Raw),
			OriginalSize: snapshot.OriginalSize,
		},
	}, nil
}

func readImage(runtime *runtime, snapshot localFileSnapshot, in readInput) (readOutput, error) {
	runtime.fileState.mu.Lock()
	storeReadView(runtime.fileState, snapshot.Path, snapshot.Content, snapshot.Timestamp, in.Offset, in.Limit, in.Pages, false, true)
	runtime.fileState.mu.Unlock()
	return readOutput{
		Type: "image",
		File: &readFile{
			FilePath:     snapshot.Path,
			Base64:       fileBase64(snapshot.Raw),
			Type:         snapshot.MediaType,
			MediaType:    snapshot.MediaType,
			OriginalSize: snapshot.OriginalSize,
		},
	}, nil
}

func readDescription() string {
	return fmt.Sprintf(`Read one file from the workspace.

Usage:
- Use %s for reading text files, screenshots, other images, PDF files, and Jupyter notebooks.
- file_path may be workspace-relative or absolute.
- By default the tool reads from the beginning of the file. Use offset and limit for targeted text reads when you already know the region you need.
- Re-reading the same unchanged file slice may return type=file_unchanged instead of repeating the content.
- For PDFs larger than %d pages, you MUST provide the pages parameter. A single request can read at most %d pages.
- This tool reads files only. Use %s or %s for directory exploration.
- This tool does not read arbitrary binary files. Images, PDFs, and notebooks are handled as structured formats instead.`, toolRead, pdfInlineReadThreshold, pdfMaxPagesPerRead, toolBash, toolGlob)
}
