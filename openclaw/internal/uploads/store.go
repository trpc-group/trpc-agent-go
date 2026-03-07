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
	"encoding/json"
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

	metadataSuffix = ".meta.json"

	defaultChannelDir = "unknown-channel"
	defaultUserDir    = "unknown-user"
	defaultSessionDir = "unknown-session"
	defaultFileName   = "attachment"

	maxFileNameRunes = 96
	hashPrefixBytes  = 12
	hashPrefixHexLen = hashPrefixBytes * 2
	hashPrefixSep    = '-'

	fileMode = 0o600
	dirMode  = 0o755
)

const MetadataSuffix = metadataSuffix

const (
	KindImage = "image"
	KindAudio = "audio"
	KindVideo = "video"
	KindPDF   = "pdf"
	KindFile  = "file"
)

const (
	displayPhotoName      = "photo"
	displayAudioName      = "audio"
	displayVideoName      = "video"
	displayAnimationName  = "animation"
	displayDocumentName   = "document"
	displayAttachmentName = "attachment"
)

const (
	SourceInbound = "inbound"
	SourceDerived = "derived"
)

// FileMetadata describes optional metadata stored with one persisted file.
type FileMetadata struct {
	MimeType string `json:"mime_type,omitempty"`
	Source   string `json:"source,omitempty"`
}

// ListedFile describes one persisted upload entry.
type ListedFile struct {
	Scope        Scope
	Name         string
	Path         string
	HostRef      string
	RelativePath string
	MimeType     string
	Source       string
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
	ctx context.Context,
	scope Scope,
	name string,
	data []byte,
) (SavedFile, error) {
	return s.SaveWithInfo(
		ctx,
		scope,
		name,
		FileMetadata{},
		data,
	)
}

// SaveWithMetadata persists data together with optional metadata.
func (s *Store) SaveWithMetadata(
	ctx context.Context,
	scope Scope,
	name string,
	mimeType string,
	data []byte,
) (SavedFile, error) {
	return s.SaveWithInfo(
		ctx,
		scope,
		name,
		FileMetadata{MimeType: mimeType},
		data,
	)
}

// SaveWithInfo persists data together with optional metadata.
func (s *Store) SaveWithInfo(
	_ context.Context,
	scope Scope,
	name string,
	meta FileMetadata,
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
	if err := writeMetadataIfNeeded(filePath, meta); err != nil {
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

// Annotate writes metadata for one existing file already stored inside the
// uploads root.
func (s *Store) Annotate(path string, meta FileMetadata) error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return errors.New("uploads: store not configured")
	}

	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "" {
		return errors.New("uploads: empty file path")
	}
	if !filepath.IsAbs(clean) {
		return errors.New("uploads: file path must be absolute")
	}

	root, err := filepath.Abs(s.root)
	if err != nil {
		return fmt.Errorf("uploads: resolve root: %w", err)
	}
	clean, err = filepath.Abs(clean)
	if err != nil {
		return fmt.Errorf("uploads: resolve file path: %w", err)
	}
	if !pathInsideRoot(clean, root) {
		return errors.New("uploads: file outside store root")
	}

	info, err := os.Stat(clean)
	if err != nil {
		return fmt.Errorf("uploads: stat file: %w", err)
	}
	if info.IsDir() {
		return errors.New("uploads: file path is a directory")
	}

	return writeMetadataIfNeeded(clean, meta)
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

func writeMetadataIfNeeded(path string, meta FileMetadata) error {
	meta = sanitizeFileMetadata(meta)
	if meta == (FileMetadata{}) {
		return nil
	}

	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("uploads: marshal metadata: %w", err)
	}
	metaPath := metadataPath(path)
	if err := writeFileIfMissing(metaPath, raw); err != nil {
		return err
	}
	return nil
}

func metadataPath(path string) string {
	return path + metadataSuffix
}

func isMetadataFileName(name string) bool {
	return strings.HasSuffix(strings.TrimSpace(name), metadataSuffix)
}

// IsMetadataPath reports whether path points to an uploads sidecar file.
func IsMetadataPath(path string) bool {
	return isMetadataFileName(filepath.Base(strings.TrimSpace(path)))
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
			if isMetadataFileName(d.Name()) {
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
			meta, err := readMetadata(path)
			if err != nil {
				return err
			}
			files = append(files, ListedFile{
				Scope:        fileScope,
				Name:         displayUploadName(d.Name()),
				Path:         path,
				HostRef:      HostRef(path),
				RelativePath: filepath.ToSlash(rel),
				MimeType:     strings.TrimSpace(meta.MimeType),
				Source:       strings.TrimSpace(meta.Source),
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

func readMetadata(path string) (FileMetadata, error) {
	metaPath := metadataPath(path)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileMetadata{}, nil
		}
		return FileMetadata{}, fmt.Errorf(
			"uploads: read metadata: %w",
			err,
		)
	}
	var meta FileMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return FileMetadata{}, fmt.Errorf(
			"uploads: parse metadata: %w",
			err,
		)
	}
	return sanitizeFileMetadata(meta), nil
}

func displayUploadName(name string) string {
	return StoredDisplayName(name)
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

// KindFromMeta returns a stable media kind from filename and MIME.
func KindFromMeta(name string, mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return KindImage
	case strings.HasPrefix(mimeType, "audio/"):
		return KindAudio
	case strings.HasPrefix(mimeType, "video/"):
		return KindVideo
	case mimeType == "application/pdf":
		return KindPDF
	}

	switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return KindImage
	case ".mp3", ".wav", ".ogg", ".oga", ".m4a":
		return KindAudio
	case ".mp4", ".mov", ".webm", ".mkv":
		return KindVideo
	case ".pdf":
		return KindPDF
	default:
		return KindFile
	}
}

// PreferredName returns a user-facing filename for one stored upload.
// It preserves meaningful names and rewrites generated Telegram placeholder
// names like "file_10.mp4" into stable names such as "video.mp4".
func PreferredName(name string, mimeType string) string {
	trimmed := StoredDisplayName(name)
	if trimmed != "" && !isGeneratedPlaceholderName(trimmed) {
		return trimmed
	}

	base := preferredNameBase(trimmed, mimeType)
	ext := strings.ToLower(filepath.Ext(trimmed))
	if ext == "" {
		ext = preferredNameExt(mimeType)
	}
	if ext == "" {
		return base
	}
	return base + ext
}

func preferredNameBase(name string, mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(name)))

	switch {
	case mimeType == "application/pdf" || ext == ".pdf":
		return displayDocumentName
	case mimeType == "image/gif" || ext == ".gif":
		return displayAnimationName
	case strings.HasPrefix(mimeType, "image/") ||
		ext == ".jpg" || ext == ".jpeg" ||
		ext == ".png" || ext == ".webp":
		return displayPhotoName
	case strings.HasPrefix(mimeType, "audio/") ||
		ext == ".mp3" || ext == ".wav" ||
		ext == ".ogg" || ext == ".oga" ||
		ext == ".m4a":
		return displayAudioName
	case strings.HasPrefix(mimeType, "video/") ||
		ext == ".mp4" || ext == ".mov" ||
		ext == ".webm" || ext == ".mkv":
		return displayVideoName
	default:
		return displayAttachmentName
	}
}

// StoredDisplayName strips one or more internal upload hash prefixes from a
// persisted filename while leaving normal filenames untouched.
func StoredDisplayName(name string) string {
	trimmed := strings.TrimSpace(name)
	for {
		next, ok := stripStoredHashPrefix(trimmed)
		if !ok {
			return trimmed
		}
		trimmed = next
	}
}

func stripStoredHashPrefix(name string) (string, bool) {
	trimmed := strings.TrimSpace(name)
	if len(trimmed) <= hashPrefixHexLen+1 {
		return trimmed, false
	}
	if trimmed[hashPrefixHexLen] != hashPrefixSep {
		return trimmed, false
	}
	for i := 0; i < hashPrefixHexLen; i++ {
		r := trimmed[i]
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return trimmed, false
		}
	}
	return trimmed[hashPrefixHexLen+1:], true
}

func pathInsideRoot(path string, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func preferredNameExt(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "application/pdf":
		return ".pdf"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/ogg":
		return ".ogg"
	case "audio/mp4", "audio/x-m4a":
		return ".m4a"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	default:
		return ""
	}
}

func isGeneratedPlaceholderName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	stem := trimmed
	if dot := strings.Index(trimmed, "."); dot >= 0 {
		stem = trimmed[:dot]
	}
	if !strings.HasPrefix(stem, "file_") {
		return false
	}
	suffix := strings.TrimPrefix(stem, "file_")
	return suffix != "" && digitsOnly(suffix)
}

func digitsOnly(raw string) bool {
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false
		}
	}
	return raw != ""
}

func sanitizeFileMetadata(meta FileMetadata) FileMetadata {
	meta.MimeType = strings.TrimSpace(meta.MimeType)
	meta.Source = sanitizeMetadataSource(meta.Source)
	return meta
}

func sanitizeMetadataSource(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case SourceInbound:
		return SourceInbound
	case SourceDerived:
		return SourceDerived
	default:
		return ""
	}
}
