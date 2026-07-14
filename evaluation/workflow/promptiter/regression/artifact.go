//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regression

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ArtifactWriter persists pipeline artifacts by safe relative path.
type ArtifactWriter interface {
	Write(relativePath string, payload []byte) error
}

// FileArtifactWriter atomically persists artifacts below one output directory.
type FileArtifactWriter struct {
	root string
}

// NewFileArtifactWriter validates outputDir and returns a filesystem writer.
func NewFileArtifactWriter(outputDir string) (*FileArtifactWriter, error) {
	return NewFileArtifactWriterWithInputs(outputDir)
}

// NewFileArtifactWriterWithInputs returns a writer after verifying that the
// output directory does not contain or equal any protected input path.
func NewFileArtifactWriterWithInputs(outputDir string, inputPaths ...string) (*FileArtifactWriter, error) {
	if strings.TrimSpace(outputDir) == "" {
		return nil, errors.New("artifact output directory is empty")
	}
	root, err := filepath.Abs(outputDir)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact output directory: %w", err)
	}
	root = filepath.Clean(root)
	for _, inputPath := range inputPaths {
		if strings.TrimSpace(inputPath) == "" {
			continue
		}
		input, err := filepath.Abs(inputPath)
		if err != nil {
			return nil, fmt.Errorf("resolve protected input path: %w", err)
		}
		relative, err := filepath.Rel(root, filepath.Clean(input))
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("artifact output directory %q contains protected input %q", outputDir, inputPath)
		}
	}
	return &FileArtifactWriter{root: root}, nil
}

// Write atomically replaces one artifact and syncs its containing directory
// when the platform supports directory handles.
func (w *FileArtifactWriter) Write(relativePath string, payload []byte) error {
	if w == nil {
		return errors.New("artifact writer is nil")
	}
	target, err := w.resolve(relativePath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".artifact-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary artifact: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod temporary artifact: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		return fmt.Errorf("write temporary artifact: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary artifact: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("replace artifact: %w", err)
	}
	committed = true
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync artifact directory: %w", err)
	}
	return nil
}

func (w *FileArtifactWriter) resolve(relativePath string) (string, error) {
	normalized := strings.ReplaceAll(relativePath, `\`, "/")
	windowsDrive := len(normalized) >= 2 && normalized[1] == ':'
	if normalized == "" || strings.HasPrefix(normalized, "/") || windowsDrive || filepath.IsAbs(relativePath) || filepath.VolumeName(relativePath) != "" {
		return "", fmt.Errorf("unsafe artifact path %q", relativePath)
	}
	cleanSlash := filepath.ToSlash(filepath.Clean(filepath.FromSlash(normalized)))
	if cleanSlash == "." || cleanSlash == ".." || strings.HasPrefix(cleanSlash, "../") {
		return "", fmt.Errorf("unsafe artifact path %q", relativePath)
	}
	target := filepath.Join(w.root, filepath.FromSlash(cleanSlash))
	relative, err := filepath.Rel(w.root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact path %q escapes output directory", relativePath)
	}
	return target, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		if runtime.GOOS == "windows" {
			return nil
		}
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil && runtime.GOOS != "windows" {
		return err
	}
	return nil
}

func writeJSON(writer ArtifactWriter, path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return writer.Write(path, append(payload, '\n'))
}
