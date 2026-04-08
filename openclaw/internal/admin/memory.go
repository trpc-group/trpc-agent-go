//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package admin

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

const (
	memoryFileName            = "MEMORY.md"
	maxMemoryFilePreviewBytes = 4 * 1024
	maxMemoryFilePreviewRunes = 220
	maxMemoryPreviewLines     = 3
)

type MemoryFileStore interface {
	Root() string
	ReadFile(path string, maxBytes int) (string, error)
}

type memoryStatus struct {
	Enabled      bool             `json:"enabled"`
	FileEnabled  bool             `json:"file_enabled"`
	Backend      string           `json:"backend,omitempty"`
	Root         string           `json:"root,omitempty"`
	FileCount    int              `json:"file_count"`
	TotalBytes   int64            `json:"total_bytes"`
	LastModified *time.Time       `json:"last_modified,omitempty"`
	Error        string           `json:"error,omitempty"`
	Files        []memoryFileView `json:"files,omitempty"`
}

type memoryFileView struct {
	AppName      string    `json:"app_name,omitempty"`
	UserID       string    `json:"user_id,omitempty"`
	RelativePath string    `json:"relative_path,omitempty"`
	Path         string    `json:"path,omitempty"`
	OpenURL      string    `json:"open_url,omitempty"`
	Preview      string    `json:"preview,omitempty"`
	SizeBytes    int64     `json:"size_bytes"`
	ModifiedAt   time.Time `json:"modified_at"`
}

func (s *Service) memoryStatus() memoryStatus {
	return s.memoryStatusWithFiles(true)
}

func (s *Service) memoryStatusSummary() memoryStatus {
	return s.memoryStatusWithFiles(false)
}

func (s *Service) memoryStatusWithFiles(includeFiles bool) memoryStatus {
	if s == nil {
		return memoryStatus{}
	}
	out := memoryStatus{
		Enabled: strings.TrimSpace(s.cfg.MemoryBackend) != "",
		Backend: strings.TrimSpace(s.cfg.MemoryBackend),
	}
	root, configured, err := configuredMemoryRoot(s.cfg.MemoryFiles)
	if err != nil {
		out.FileEnabled = true
		out.Error = err.Error()
		return out
	}
	if !configured {
		return out
	}
	out.FileEnabled = true
	out.Root = root

	files, err := memoryFileViews(s.cfg.MemoryFiles, includeFiles)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.FileCount = len(files)
	for i := range files {
		out.TotalBytes += files[i].SizeBytes
		if out.LastModified == nil ||
			files[i].ModifiedAt.After(*out.LastModified) {
			modified := files[i].ModifiedAt
			out.LastModified = &modified
		}
	}
	if includeFiles {
		out.Files = files
	}
	return out
}

func configuredMemoryRoot(
	store MemoryFileStore,
) (string, bool, error) {
	if store == nil {
		return "", false, nil
	}
	value := reflect.ValueOf(store)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return "", false, nil
	}
	root := strings.TrimSpace(store.Root())
	if root == "" {
		return "", false, errors.New("memory file root is not configured")
	}
	return root, true, nil
}

func memoryFileViews(
	store MemoryFileStore,
	includePreview bool,
) ([]memoryFileView, error) {
	root, configured, err := configuredMemoryRoot(store)
	if err != nil {
		return nil, err
	}
	if !configured {
		return nil, nil
	}

	apps, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read memory root: %w", err)
	}

	files := make([]memoryFileView, 0)
	for _, appDir := range apps {
		if appDir == nil || !appDir.IsDir() {
			continue
		}
		appPath := filepath.Join(root, appDir.Name())
		users, err := os.ReadDir(appPath)
		if err != nil {
			continue
		}
		for _, userDir := range users {
			if userDir == nil || !userDir.IsDir() {
				continue
			}
			filePath := filepath.Join(
				appPath,
				userDir.Name(),
				memoryFileName,
			)
			info, err := os.Stat(filePath)
			if err != nil || info.IsDir() {
				continue
			}
			rel, err := filepath.Rel(root, filePath)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			preview := ""
			if includePreview {
				preview, _ = store.ReadFile(
					filePath,
					maxMemoryFilePreviewBytes,
				)
			}
			files = append(files, memoryFileView{
				AppName:      decodeMemoryPathPart(appDir.Name()),
				UserID:       decodeMemoryPathPart(userDir.Name()),
				RelativePath: rel,
				Path:         filePath,
				OpenURL: routeMemoryFile + "?" + url.Values{
					queryPath: {rel},
				}.Encode(),
				Preview: summarizeMemoryPreview(
					preview,
					maxMemoryPreviewLines,
					maxMemoryFilePreviewRunes,
				),
				SizeBytes:  info.Size(),
				ModifiedAt: info.ModTime(),
			})
		}
	}

	sort.Slice(files, func(i, j int) bool {
		if !files[i].ModifiedAt.Equal(files[j].ModifiedAt) {
			return files[i].ModifiedAt.After(files[j].ModifiedAt)
		}
		if files[i].AppName != files[j].AppName {
			return files[i].AppName < files[j].AppName
		}
		return files[i].UserID < files[j].UserID
	})
	return files, nil
}

func decodeMemoryPathPart(part string) string {
	trimmed := strings.TrimSpace(part)
	if trimmed == "" {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(trimmed)
	if err != nil {
		return trimmed
	}
	value := strings.TrimSpace(string(decoded))
	if value == "" {
		return trimmed
	}
	return value
}

func summarizeMemoryPreview(
	text string,
	maxLines int,
	maxRunes int,
) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.EqualFold(trimmed, "# Memory") {
			continue
		}
		filtered = append(filtered, trimmed)
	}
	if len(filtered) == 0 {
		return ""
	}
	truncated := false
	if maxLines > 0 && len(filtered) > maxLines {
		filtered = filtered[:maxLines]
		truncated = true
	}
	out := summarizeText(strings.Join(filtered, "\n"), maxRunes)
	if truncated && !strings.HasSuffix(out, "...") {
		out += "..."
	}
	return out
}

func resolveMemoryFile(root string, relPath string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("memory file store is not configured")
	}
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(relPath)))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("memory file path is required")
	}
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid memory file path")
	}
	if filepath.Base(clean) != memoryFileName {
		return "", fmt.Errorf("unsupported memory file: %s", clean)
	}

	candidate := filepath.Join(root, clean)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve memory root: %w", err)
	}
	resolvedRoot := absRoot
	if evaluatedRoot, err := filepath.EvalSymlinks(absRoot); err == nil {
		resolvedRoot = evaluatedRoot
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolve memory root: %w", err)
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve memory file: %w", err)
	}
	if absCandidate != absRoot &&
		!strings.HasPrefix(
			absCandidate,
			absRoot+string(os.PathSeparator),
		) {
		return "", fmt.Errorf("memory file escapes memory root")
	}
	resolvedPath, err := filepath.EvalSymlinks(absCandidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("memory file not found")
		}
		return "", fmt.Errorf("resolve memory file: %w", err)
	}
	absResolved, err := filepath.Abs(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("resolve memory file: %w", err)
	}
	if absResolved != resolvedRoot &&
		!strings.HasPrefix(
			absResolved,
			resolvedRoot+string(os.PathSeparator),
		) {
		return "", fmt.Errorf("memory file escapes memory root")
	}
	info, err := os.Stat(absResolved)
	if err != nil {
		return "", fmt.Errorf("memory file not found")
	}
	if info.IsDir() {
		return "", fmt.Errorf("memory path is a directory")
	}
	return absResolved, nil
}
