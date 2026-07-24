//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxSnapshotBytes = 64 << 20
	maxSnapshotFiles = 10000
	maxSnapshotFile  = 4 << 20
)

var excludedSnapshotDirs = map[string]bool{
	".git": true, ".idea": true, ".vscode": true,
	"node_modules": true, ".cache": true,
}

func createRepoSnapshot(repoPath string) (string, func(), error) {
	root, err := filepath.Abs(repoPath)
	if err != nil {
		return "", nil, fmt.Errorf("resolve repository path: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", nil, fmt.Errorf("stat repository: %w", err)
	}
	if !info.IsDir() {
		return "", nil, errors.New("repository path is not a directory")
	}
	snapshot, err := os.MkdirTemp("", "trpc-review-snapshot-*")
	if err != nil {
		return "", nil, fmt.Errorf("create repository snapshot: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(snapshot) }
	var totalBytes int64
	var files int
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			if excludedSnapshotDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(snapshot, relative), 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		if excludedSnapshotFile(entry.Name()) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxSnapshotFile {
			return fmt.Errorf("snapshot file %q exceeds %d-byte limit",
				relative, maxSnapshotFile)
		}
		files++
		totalBytes += info.Size()
		if files > maxSnapshotFiles || totalBytes > maxSnapshotBytes {
			return fmt.Errorf(
				"repository snapshot exceeds %d files or %d bytes",
				maxSnapshotFiles, maxSnapshotBytes,
			)
		}
		return copySnapshotFile(
			current, filepath.Join(snapshot, relative), info.Mode(),
		)
	})
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return snapshot, cleanup, nil
}

func excludedSnapshotFile(name string) bool {
	lower := strings.ToLower(name)
	if lower == ".env" || strings.HasPrefix(lower, ".env.") {
		return true
	}
	switch filepath.Ext(lower) {
	case ".pem", ".key", ".p12", ".pfx":
		return true
	}
	return lower == "review.db" ||
		lower == "review_report.json" ||
		lower == "review_report.md"
}

func copySnapshotFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	outputMode := os.FileMode(0o644)
	if mode&0o111 != 0 {
		outputMode = 0o755
	}
	output, err := os.OpenFile(
		destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, outputMode,
	)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
