//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression/report"
)

// WriteReports renders and atomically stores the final JSON and Markdown reports.
func WriteReports(
	ctx context.Context,
	store *Store,
	result *regression.RunResult,
) ([]File, error) {
	if store == nil || result == nil || result.RunID == "" {
		return nil, errors.New("store and completed run result are required")
	}
	if err := validateRunDirectoryName(result.RunID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	jsonReport, err := report.JSON(result)
	if err != nil {
		return nil, err
	}
	markdownReport, err := report.Markdown(result)
	if err != nil {
		return nil, err
	}
	return store.writeBundle(ctx, result.RunID, []bundleFile{
		{name: "optimization_report.json", content: jsonReport},
		{name: "optimization_report.md", content: markdownReport},
	})
}

type bundleFile struct {
	name    string
	content []byte
}

func validateRunDirectoryName(runID string) error {
	if runID == "." || runID == ".." {
		return fmt.Errorf("invalid run id %q: reserved path component", runID)
	}
	if err := regression.ValidateRunID(runID); err != nil {
		return fmt.Errorf("invalid run id %q: %w", runID, err)
	}
	return nil
}

func (s *Store) writeBundle(
	ctx context.Context,
	directory string,
	values []bundleFile,
) ([]File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	finalDirectory, err := s.runPath(directory)
	if err != nil {
		return nil, err
	}
	if existing, verifyErr := verifyBundle(finalDirectory, directory, values); verifyErr == nil {
		return existing, nil
	} else if !errors.Is(verifyErr, os.ErrNotExist) {
		return nil, verifyErr
	}
	temporaryDirectory, err := os.MkdirTemp(s.root, ".report-bundle-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary report bundle: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporaryDirectory)
		}
	}()
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if filepath.Base(value.name) != value.name || value.name == "." || value.name == ".." {
			return nil, fmt.Errorf("invalid report file name %q", value.name)
		}
		if err := writeSyncedFile(filepath.Join(temporaryDirectory, value.name), value.content); err != nil {
			return nil, err
		}
	}
	if err := syncDirectory(temporaryDirectory); err != nil {
		return nil, fmt.Errorf("sync temporary report bundle: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.Rename(temporaryDirectory, finalDirectory); err != nil {
		if existing, verifyErr := verifyBundle(finalDirectory, directory, values); verifyErr == nil {
			return existing, nil
		} else if !errors.Is(verifyErr, os.ErrNotExist) {
			return nil, errors.Join(
				fmt.Errorf("commit report bundle: %w", err),
				verifyErr,
			)
		}
		return nil, fmt.Errorf("commit report bundle: %w", err)
	}
	committed = true
	if err := syncDirectory(s.root); err != nil {
		// The rename already made this immutable bundle visible. Deleting it here
		// can race with another writer that has verified the same bundle.
		return nil, fmt.Errorf("sync artifact root after publishing report bundle: %w", err)
	}
	files, err := verifyBundle(finalDirectory, directory, values)
	if err != nil {
		return nil, fmt.Errorf("verify published report bundle: %w", err)
	}
	return files, nil
}

func writeSyncedFile(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return fmt.Errorf("create report file: %w", err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("write report file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync report file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close report file: %w", err)
	}
	return nil
}

func verifyBundle(
	directory string,
	directoryName string,
	values []bundleFile,
) ([]File, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("report bundle %q is not an immutable directory", directoryName)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read report bundle: %w", err)
	}
	if len(entries) != len(values) {
		return nil, fmt.Errorf("report bundle %q has unexpected files", directoryName)
	}
	files := make([]File, 0, len(values))
	for _, value := range values {
		path := filepath.Join(directory, value.name)
		digest, err := digestFile(path)
		if err != nil {
			return nil, fmt.Errorf("inspect report file %q: %w", value.name, err)
		}
		expected := digestBytes(value.content)
		if digest != expected {
			return nil, fmt.Errorf("report bundle %q already exists with different content", directoryName)
		}
		name := filepath.ToSlash(filepath.Join(directoryName, value.name))
		files = append(files, *metadata(name, path, digest))
	}
	return files, nil
}

func digestBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func digestFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("report file %q is a symbolic link", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return digestBytes(content), nil
}
