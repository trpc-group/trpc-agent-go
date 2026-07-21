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
	"crypto/rand"
	"encoding/hex"
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
	rootHandle *os.Root
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
	root, err = canonicalPath(root)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact output directory symlinks: %w", err)
	}
	for _, inputPath := range inputPaths {
		if strings.TrimSpace(inputPath) == "" {
			continue
		}
		input, err := canonicalPath(inputPath)
		if err != nil {
			return nil, fmt.Errorf("resolve protected input path: %w", err)
		}
		relative, err := filepath.Rel(root, input)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("artifact output directory %q contains protected input %q", outputDir, inputPath)
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact output directory: %w", err)
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open artifact output directory: %w", err)
	}
	return &FileArtifactWriter{rootHandle: rootHandle}, nil
}

// Close releases the filesystem root handle held by the writer.
func (w *FileArtifactWriter) Close() error {
	if w == nil || w.rootHandle == nil {
		return nil
	}
	return w.rootHandle.Close()
}

// Write atomically replaces one artifact and syncs its containing directory
// when the platform supports directory handles.
func (w *FileArtifactWriter) Write(relativePath string, payload []byte) error {
	if w == nil {
		return errors.New("artifact writer is nil")
	}
	if w.rootHandle == nil {
		return errors.New("artifact writer root is closed or unavailable")
	}
	target, err := w.resolve(relativePath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(target)
	if err := w.rootHandle.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	temporary, temporaryPath, err := createRootTemp(w.rootHandle, dir)
	if err != nil {
		return fmt.Errorf("create temporary artifact: %w", err)
	}
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = w.rootHandle.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(payload); err != nil {
		return fmt.Errorf("write temporary artifact: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary artifact: %w", err)
	}
	if err := w.rootHandle.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("replace artifact: %w", err)
	}
	committed = true
	if err := syncRootDirectory(w.rootHandle, dir); err != nil {
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
	return filepath.FromSlash(cleanSlash), nil
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	current := abs
	missing := make([]string, 0)
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func createRootTemp(root *os.Root, dir string) (*os.File, string, error) {
	for range 100 {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", err
		}
		name := filepath.Join(dir, ".artifact-"+hex.EncodeToString(random[:])+".tmp")
		file, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return file, name, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("exhausted temporary artifact names")
}

func syncRootDirectory(root *os.Root, path string) error {
	directory, err := root.Open(path)
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
