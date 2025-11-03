//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"
)

// Tool implementations (simulated file operations).

// readFile simulates reading a file.
func (e *userContextExample) readFile(ctx context.Context, args *fileArgs) (*fileResult, error) {
	// Simulate file reading.
	content := fmt.Sprintf("Contents of %s:\nLine 1: Hello\nLine 2: World", args.Filename)
	return &fileResult{
		Filename:  args.Filename,
		Operation: permissionRead,
		Content:   content,
		Success:   true,
	}, nil
}

// writeFile simulates writing to a file.
func (e *userContextExample) writeFile(ctx context.Context, args *writeFileArgs) (*fileResult, error) {
	// Simulate file writing.
	return &fileResult{
		Filename:  args.Filename,
		Operation: permissionWrite,
		Content:   fmt.Sprintf("Wrote %d bytes to %s", len(args.Content), args.Filename),
		Success:   true,
	}, nil
}

// deleteFile simulates deleting a file.
func (e *userContextExample) deleteFile(ctx context.Context, args *fileArgs) (*fileResult, error) {
	// Simulate file deletion.
	return &fileResult{
		Filename:  args.Filename,
		Operation: permissionDelete,
		Content:   fmt.Sprintf("Deleted %s successfully", args.Filename),
		Success:   true,
	}, nil
}

// listFiles simulates listing files.
func (e *userContextExample) listFiles(ctx context.Context, args *listFilesArgs) (*listFilesResult, error) {
	// Simulate file listing.
	files := []string{"config.txt", "data.json", "readme.md", "notes.txt"}
	return &listFilesResult{
		Directory: args.Directory,
		Files:     files,
		Count:     len(files),
	}, nil
}

// Data structures.

// fileArgs represents arguments for file operations.
type fileArgs struct {
	Filename string `json:"filename" description:"The name of the file"`
}

// writeFileArgs represents arguments for writing a file.
type writeFileArgs struct {
	Filename string `json:"filename" description:"The name of the file"`
	Content  string `json:"content" description:"The content to write"`
}

// listFilesArgs represents arguments for listing files.
type listFilesArgs struct {
	Directory string `json:"directory" description:"The directory to list (default: current)"`
}

// fileResult represents the result of a file operation.
type fileResult struct {
	Filename  string `json:"filename"`
	Operation string `json:"operation"`
	Content   string `json:"content"`
	Success   bool   `json:"success"`
}

// listFilesResult represents the result of listing files.
type listFilesResult struct {
	Directory string   `json:"directory"`
	Files     []string `json:"files"`
	Count     int      `json:"count"`
}
