//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package uploads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultUploadsDir = "uploads"

	hostRefPrefix = "host://"

	defaultChannelDir = "unknown-channel"
	defaultUserDir    = "unknown-user"
	defaultSessionDir = "unknown-session"
	defaultFileName   = "attachment"

	maxFileNameRunes = 96
	hashPrefixBytes  = 12
	hashPrefixHexLen = hashPrefixBytes * 2

	fileMode = 0o600
	dirMode  = 0o755
)

// ListedFile describes one persisted upload entry.
type ListedFile struct {
	Scope        Scope
	Name         string
	Path         string
	HostRef      string
	RelativePath string
	SizeBytes    int64
	ModifiedAt   time.Time
}

// Scope identifies who owns a persisted upload.
type Scope struct {
	Channel   string
	UserID    string
	SessionID string
}

// SavedFile describes a persisted upload.
type SavedFile struct {
	Name    string
	Path    string
	HostRef string
}

// Store persists uploaded files under the OpenClaw state directory.
type Store struct {
	root string
}

// NewStore creates a new upload store rooted at stateDir/uploads.
func NewStore(stateDir string) (*Store, error) {
	root := filepath.Join(
		strings.TrimSpace(stateDir),
		defaultUploadsDir,
	)
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("uploads: empty state dir")
	}
	return &Store{root: root}, nil
}

// Root returns the uploads root directory.
func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// ScopeDir returns the stable host directory for one upload scope.
func (s *Store) ScopeDir(scope Scope) string {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return ""
	}
	return s.scopeDirPath(scope)
}

// Save persists data for the given scope and returns a stable host ref.
func (s *Store) Save(
	_ context.Context,
	scope Scope,
	name string,
	data []byte,
) (SavedFile, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return SavedFile{}, errors.New("uploads: store not configured")
	}
	if len(data) == 0 {
		return SavedFile{}, errors.New("uploads: empty file data")
	}

	safeName := sanitizeFileName(name)
	dir := s.scopeDirPath(scope)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return SavedFile{}, fmt.Errorf("uploads: create dir: %w", err)
	}

	sum := sha256.Sum256(data)
	base := hex.EncodeToString(sum[:hashPrefixBytes])
	filePath := filepath.Join(dir, base+"-"+safeName)
	if err := writeFileIfMissing(filePath, data); err != nil {
		return SavedFile{}, err
	}

	return SavedFile{
		Name:    safeName,
		Path:    filePath,
		HostRef: HostRef(filePath),
	}, nil
}

// DeleteUser removes all uploads for the given channel/user pair.
func (s *Store) DeleteUser(
	_ context.Context,
	channel string,
	userID string,
) error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return nil
	}
	dir := filepath.Join(
		s.root,
		sanitizeDirToken(channel, defaultChannelDir),
		sanitizeDirToken(userID, defaultUserDir),
	)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("uploads: delete user dir: %w", err)
	}
	return nil
}

// ListScope returns persisted uploads for one session scope, newest first.
func (s *Store) ListScope(scope Scope, limit int) ([]ListedFile, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return nil, nil
	}
	return listStoredFiles(
		s.root,
		s.scopeDirPath(scope),
		scope,
		limit,
	)
}

// ListAll returns persisted uploads across all users, newest first.
func (s *Store) ListAll(limit int) ([]ListedFile, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return nil, nil
	}
	return listStoredFiles(
		s.root,
		s.root,
		Scope{},
		limit,
	)
}

// HostRef converts an absolute path into a host:// ref.
func HostRef(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	return hostRefPrefix + trimmed
}

// PathFromHostRef returns the absolute host path for ref when possible.
func PathFromHostRef(ref string) (string, bool) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", false
	}
	if strings.HasPrefix(trimmed, hostRefPrefix) {
		path := strings.TrimPrefix(trimmed, hostRefPrefix)
		if filepath.IsAbs(path) {
			return path, true
		}
		return "", false
	}
	if filepath.IsAbs(trimmed) {
		return trimmed, true
	}
	return "", false
}

func (s *Store) scopeDirPath(scope Scope) string {
	return filepath.Join(
		s.root,
		sanitizeDirToken(scope.Channel, defaultChannelDir),
		sanitizeDirToken(scope.UserID, defaultUserDir),
		sanitizeDirToken(scope.SessionID, defaultSessionDir),
	)
}

func sanitizeDirToken(raw string, fallback string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback
	}
	var b strings.Builder
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return fallback
	}
	return out
}

func sanitizeFileName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	base := strings.TrimSpace(filepath.Base(trimmed))
	switch base {
	case "", ".", string(filepath.Separator):
		base = defaultFileName
	}

	var b strings.Builder
	count := 0
	for _, r := range base {
		if count >= maxFileNameRunes {
			break
		}
		switch {
		case r == 0:
			continue
		case r < 32:
			b.WriteByte('_')
		case r == '/', r == '\\':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
		count++
	}

	out := strings.TrimSpace(b.String())
	out = strings.Trim(out, ".")
	if out == "" {
		return defaultFileName
	}
	return out
}

func writeFileIfMissing(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("uploads: stat file: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, fileMode); err != nil {
		return fmt.Errorf("uploads: write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		if _, statErr := os.Stat(path); statErr == nil {
			return nil
		}
		return fmt.Errorf("uploads: rename temp file: %w", err)
	}
	return nil
}

func listStoredFiles(
	root string,
	walkRoot string,
	scope Scope,
	limit int,
) ([]ListedFile, error) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(walkRoot) == "" {
		return nil, nil
	}
	if _, err := os.Stat(walkRoot); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("uploads: stat dir: %w", err)
	}

	files := make([]ListedFile, 0)
	err := filepath.WalkDir(
		walkRoot,
		func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d == nil || d.IsDir() {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			fileScope := scope
			if fileScope == (Scope{}) {
				fileScope = scopeFromRelativePath(rel)
			}
			files = append(files, ListedFile{
				Scope:        fileScope,
				Name:         displayUploadName(d.Name()),
				Path:         path,
				HostRef:      HostRef(path),
				RelativePath: filepath.ToSlash(rel),
				SizeBytes:    info.Size(),
				ModifiedAt:   info.ModTime(),
			})
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("uploads: list files: %w", err)
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].ModifiedAt.Equal(files[j].ModifiedAt) {
			return files[i].RelativePath < files[j].RelativePath
		}
		return files[i].ModifiedAt.After(files[j].ModifiedAt)
	})
	if limit > 0 && len(files) > limit {
		files = files[:limit]
	}
	return files, nil
}

func displayUploadName(name string) string {
	trimmed := strings.TrimSpace(name)
	if len(trimmed) <= hashPrefixHexLen+1 {
		return trimmed
	}
	if trimmed[hashPrefixHexLen] != '-' {
		return trimmed
	}
	for i := 0; i < hashPrefixHexLen; i++ {
		r := trimmed[i]
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return trimmed
		}
	}
	return trimmed[hashPrefixHexLen+1:]
}

func scopeFromRelativePath(rel string) Scope {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 4 {
		return Scope{}
	}
	return Scope{
		Channel:   parts[0],
		UserID:    parts[1],
		SessionID: parts[2],
	}
}
