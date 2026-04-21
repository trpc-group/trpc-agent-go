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
	"context"
	"crypto/sha256"
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

	memoryCardIDPrefix      = "memory-file-"
	memoryFilePerm          = 0o600
	memoryTempPatternSuffix = ".tmp-*"
)

type MemoryFileStore interface {
	Root() string
	ReadFile(path string, maxBytes int) (string, error)
}

type MemoryUserLabelResolver interface {
	ResolveMemoryUserLabel(appName string, userID string) string
}

type MemoryUserLabelResolverFunc func(
	appName string,
	userID string,
) string

func (f MemoryUserLabelResolverFunc) ResolveMemoryUserLabel(
	appName string,
	userID string,
) string {
	if f == nil {
		return ""
	}
	return f(appName, userID)
}

type memoryFileSaver interface {
	SaveResolvedMemoryFile(
		ctx context.Context,
		path string,
		content string,
	) error
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
	UserLabel    string    `json:"user_label,omitempty"`
	RelativePath string    `json:"relative_path,omitempty"`
	Path         string    `json:"path,omitempty"`
	OpenURL      string    `json:"open_url,omitempty"`
	LoadURL      string    `json:"load_url,omitempty"`
	CardID       string    `json:"card_id,omitempty"`
	SearchValue  string    `json:"search_value,omitempty"`
	Preview      string    `json:"preview,omitempty"`
	SizeBytes    int64     `json:"size_bytes"`
	ModifiedAt   time.Time `json:"modified_at"`
}

type memoryFileDetail struct {
	AppName      string    `json:"app_name,omitempty"`
	UserID       string    `json:"user_id,omitempty"`
	UserLabel    string    `json:"user_label,omitempty"`
	RelativePath string    `json:"relative_path,omitempty"`
	OpenURL      string    `json:"open_url,omitempty"`
	LoadURL      string    `json:"load_url,omitempty"`
	Content      string    `json:"content,omitempty"`
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

	files, err := memoryFileViewsWithResolver(
		s.cfg.MemoryFiles,
		s.cfg.MemoryUserLabels,
		includeFiles,
	)
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
	return memoryFileViewsWithResolver(store, nil, includePreview)
}

func memoryFileViewsWithResolver(
	store MemoryFileStore,
	resolver MemoryUserLabelResolver,
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
			appName := decodeMemoryPathPart(appDir.Name())
			userID := decodeMemoryPathPart(userDir.Name())
			userLabel := resolveMemoryUserLabel(
				resolver,
				appName,
				userID,
			)
			files = append(files, memoryFileView{
				AppName:      appName,
				UserID:       userID,
				UserLabel:    userLabel,
				RelativePath: rel,
				Path:         filePath,
				OpenURL: routeMemoryFile + "?" + url.Values{
					queryPath: {rel},
				}.Encode(),
				LoadURL: routeMemoryFileAPI + "?" + url.Values{
					queryPath: {rel},
				}.Encode(),
				CardID: memoryCardID(rel),
				SearchValue: buildMemorySearchValue(
					appName,
					userID,
					userLabel,
					rel,
					preview,
				),
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
	clean, err := normalizeMemoryRelativePath(relPath)
	if err != nil {
		return "", err
	}

	candidate := filepath.Join(root, filepath.FromSlash(clean))
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

func normalizeMemoryRelativePath(relPath string) (string, error) {
	clean := filepath.Clean(
		filepath.FromSlash(strings.TrimSpace(relPath)),
	)
	if clean == "." || clean == "" {
		return "", fmt.Errorf("memory file path is required")
	}
	parentPrefix := ".." + string(filepath.Separator)
	if filepath.IsAbs(clean) ||
		clean == ".." ||
		strings.HasPrefix(clean, parentPrefix) {
		return "", fmt.Errorf("invalid memory file path")
	}
	if filepath.Base(clean) != memoryFileName {
		return "", fmt.Errorf("unsupported memory file: %s", clean)
	}
	return filepath.ToSlash(clean), nil
}

func readMemoryFileDetail(
	root string,
	relPath string,
) (memoryFileDetail, error) {
	return readMemoryFileDetailWithResolver(root, relPath, nil)
}

func readMemoryFileDetailWithResolver(
	root string,
	relPath string,
	resolver MemoryUserLabelResolver,
) (memoryFileDetail, error) {
	cleanRelPath, err := normalizeMemoryRelativePath(relPath)
	if err != nil {
		return memoryFileDetail{}, err
	}
	filePath, err := resolveMemoryFile(root, cleanRelPath)
	if err != nil {
		return memoryFileDetail{}, err
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return memoryFileDetail{}, fmt.Errorf(
			"read memory file: %w",
			err,
		)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return memoryFileDetail{}, fmt.Errorf(
			"stat memory file: %w",
			err,
		)
	}
	appName, userID := memoryScopeFromRelativePath(cleanRelPath)
	userLabel := resolveMemoryUserLabel(resolver, appName, userID)
	return memoryFileDetail{
		AppName:      appName,
		UserID:       userID,
		UserLabel:    userLabel,
		RelativePath: cleanRelPath,
		OpenURL: routeMemoryFile + "?" + url.Values{
			queryPath: {cleanRelPath},
		}.Encode(),
		LoadURL: routeMemoryFileAPI + "?" + url.Values{
			queryPath: {cleanRelPath},
		}.Encode(),
		Content:    string(raw),
		SizeBytes:  info.Size(),
		ModifiedAt: info.ModTime(),
	}, nil
}

func saveMemoryFile(
	ctx context.Context,
	store MemoryFileStore,
	relPath string,
	content string,
) error {
	root, configured, err := configuredMemoryRoot(store)
	if err != nil || !configured {
		return fmt.Errorf("memory file store is not configured")
	}
	filePath, err := resolveMemoryFile(root, relPath)
	if err != nil {
		return err
	}
	if saver, ok := store.(memoryFileSaver); ok {
		return saver.SaveResolvedMemoryFile(ctx, filePath, content)
	}
	return writeMemoryFileAtomic(filePath, []byte(content))
}

func memoryScopeFromRelativePath(relPath string) (string, string) {
	parts := strings.Split(
		filepath.ToSlash(strings.TrimSpace(relPath)),
		"/",
	)
	if len(parts) < 3 {
		return "", ""
	}
	return decodeMemoryPathPart(parts[0]),
		decodeMemoryPathPart(parts[1])
}

func buildMemorySearchValue(
	appName string,
	userID string,
	userLabel string,
	relPath string,
	preview string,
) string {
	return strings.Join(
		[]string{
			strings.TrimSpace(appName),
			strings.TrimSpace(userID),
			strings.TrimSpace(userLabel),
			strings.TrimSpace(relPath),
			strings.TrimSpace(preview),
		},
		" ",
	)
}

func resolveMemoryUserLabel(
	resolver MemoryUserLabelResolver,
	appName string,
	userID string,
) string {
	if resolver == nil {
		return ""
	}
	label := strings.TrimSpace(
		resolver.ResolveMemoryUserLabel(appName, userID),
	)
	if label == "" || label == strings.TrimSpace(userID) {
		return ""
	}
	return label
}

func memoryCardID(relPath string) string {
	trimmed := strings.TrimSpace(relPath)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return fmt.Sprintf(
		"%s%x",
		memoryCardIDPrefix,
		sum[:6],
	)
}

func writeMemoryFileAtomic(path string, data []byte) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("memory file path is required")
	}
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(
		dir,
		filepath.Base(path)+memoryTempPatternSuffix,
	)
	if err != nil {
		return fmt.Errorf("create temp memory file: %w", err)
	}
	tempPath := file.Name()
	removeTemp := true
	defer func() {
		_ = file.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write temp memory file: %w", err)
	}
	if err := file.Chmod(memoryFilePerm); err != nil {
		return fmt.Errorf("chmod temp memory file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temp memory file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace memory file: %w", err)
	}
	removeTemp = false
	return nil
}
