//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package codeexecutor holds workspace metadata helpers and constants.
package codeexecutor

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Well-known subdirectories in a workspace.
const (
	// DirSkills contains session-scoped skill working copies. Skills
	// staged here are writable by default so third-party scripts can
	// emit cache files, temporary outputs, and Python bytecode next to
	// their source. Callers that need a stable, canonical skill tree
	// should treat the upstream skill repository as the source of
	// truth; the copy under this directory is a working copy tied to
	// the current session.
	DirSkills = "skills"
	// DirWork contains writable shared intermediates.
	DirWork = "work"
	// DirRuns contains per-run working directories.
	DirRuns = "runs"
	// DirOut contains collected outputs for artifacting.
	DirOut = "out"
	// MetaFileName is the metadata file name at workspace root.
	MetaFileName = "metadata.json"
)

// Additional environment variable keys injected at runtime.
const (
	EnvSkillsDir = "SKILLS_DIR"
	EnvWorkDir   = "WORK_DIR"
	EnvOutputDir = "OUTPUT_DIR"
	EnvRunDir    = "RUN_DIR"
	EnvSkillName = "SKILL_NAME"
)

const (
	metadataFileMode       fs.FileMode = 0o600
	legacyMetadataTmpName              = ".metadata.tmp"
	metadataTmpPrefix                  = ".metadata."
	metadataTmpSuffix                  = ".tmp"
	metadataTmpPartCount               = 4
	metadataRandomHexLen               = 16
	metadataNoRandomSuffix             = "norand"
	emptyMetadataLockKey               = "__empty_workspace__"
)

var (
	metadataTmpCounter uint64
	metadataLocks      = newWorkspaceMetadataLocker()
)

type workspaceMetadataLocker struct {
	mu    sync.Mutex
	locks map[string]*workspaceMetadataLock
}

type workspaceMetadataLock struct {
	ch   chan struct{}
	refs int
}

func newWorkspaceMetadataLocker() *workspaceMetadataLocker {
	return &workspaceMetadataLocker{
		locks: make(map[string]*workspaceMetadataLock),
	}
}

// MetadataTempFileName returns a unique workspace-relative temporary file
// name suitable for atomically replacing metadata.json.
func MetadataTempFileName() string {
	id := atomic.AddUint64(&metadataTmpCounter, 1)
	return fmt.Sprintf(
		"%s%d.%d.%d.%s%s",
		metadataTmpPrefix,
		os.Getpid(),
		time.Now().UnixNano(),
		id,
		metadataRandomSuffix(),
		metadataTmpSuffix,
	)
}

// NewWorkspaceMetadata returns a metadata value initialized with defaults.
func NewWorkspaceMetadata() WorkspaceMetadata {
	now := time.Now()
	return WorkspaceMetadata{
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
		LastAccess: now,
		Skills:     map[string]SkillMeta{},
	}
}

// IsMetadataCorruptError reports whether err came from decoding workspace
// metadata JSON.
func IsMetadataCorruptError(err error) bool {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}
	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &typeErr)
}

// IsMetadataTempFileName reports whether name is a metadata temp file created
// by MetadataTempFileName.
func IsMetadataTempFileName(name string) bool {
	base := filepath.Base(strings.TrimSpace(name))
	if base == legacyMetadataTmpName {
		return true
	}
	if !strings.HasPrefix(base, metadataTmpPrefix) ||
		!strings.HasSuffix(base, metadataTmpSuffix) {
		return false
	}
	body := strings.TrimPrefix(base, metadataTmpPrefix)
	body = strings.TrimSuffix(body, metadataTmpSuffix)
	parts := strings.Split(body, ".")
	if len(parts) != metadataTmpPartCount {
		return false
	}
	for _, part := range parts[:metadataTmpPartCount-1] {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil || n == 0 {
			return false
		}
	}
	return isMetadataRandomSuffix(parts[metadataTmpPartCount-1])
}

// IsRootMetadataTempPath reports whether rel identifies a root-level
// workspace metadata temp file.
func IsRootMetadataTempPath(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	return !strings.Contains(rel, "/") && IsMetadataTempFileName(rel)
}

func metadataRandomSuffix() string {
	var buf [8]byte
	if _, err := cryptorand.Read(buf[:]); err != nil {
		return metadataNoRandomSuffix
	}
	return hex.EncodeToString(buf[:])
}

func isMetadataRandomSuffix(s string) bool {
	if s == metadataNoRandomSuffix {
		return true
	}
	if len(s) != metadataRandomHexLen {
		return false
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			continue
		}
		if r >= 'a' && r <= 'f' {
			continue
		}
		return false
	}
	return true
}

// WithWorkspaceMetadataLock serializes metadata read-modify-write operations
// for the same workspace within this process. The callback should keep the
// critical section limited to metadata load, mutation, and save.
func WithWorkspaceMetadataLock(
	ctx context.Context,
	root string,
	fn func(context.Context) error,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	key := workspaceMetadataLockKey(root)
	unlock, err := metadataLocks.lock(ctx, key)
	if err != nil {
		return err
	}
	defer unlock()
	return fn(ctx)
}

func workspaceMetadataLockKey(root string) string {
	key := strings.TrimSpace(root)
	if key == "" {
		return emptyMetadataLockKey
	}
	if abs, err := filepath.Abs(key); err == nil {
		key = abs
	}
	return filepath.Clean(key)
}

func (k *workspaceMetadataLocker) lock(
	ctx context.Context,
	key string,
) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	k.mu.Lock()
	kl, ok := k.locks[key]
	if !ok {
		kl = &workspaceMetadataLock{ch: make(chan struct{}, 1)}
		k.locks[key] = kl
	}
	kl.refs++
	k.mu.Unlock()

	select {
	case kl.ch <- struct{}{}:
	case <-ctx.Done():
		k.releaseRef(key, kl)
		return nil, ctx.Err()
	}
	return func() {
		<-kl.ch
		k.releaseRef(key, kl)
	}, nil
}

func (k *workspaceMetadataLocker) releaseRef(
	key string,
	kl *workspaceMetadataLock,
) {
	k.mu.Lock()
	defer k.mu.Unlock()
	kl.refs--
	if kl.refs == 0 {
		delete(k.locks, key)
	}
}

// WorkspaceMetadata describes staged skills and recent activity.
type WorkspaceMetadata struct {
	Version    int                  `json:"version"`
	CreatedAt  time.Time            `json:"created_at"`
	UpdatedAt  time.Time            `json:"updated_at"`
	LastAccess time.Time            `json:"last_access"`
	Skills     map[string]SkillMeta `json:"skills"`
	Inputs     []InputRecord        `json:"inputs,omitempty"`
	Outputs    []OutputRecord       `json:"outputs,omitempty"`
	// Prepared records the last-known converged state for each
	// workspace requirement keyed by Requirement.Key(). It is used by
	// the workspaceprep reconciler to skip work whose fingerprint is
	// unchanged and whose sentinel (for example the target file) is
	// still present. The map is a local per-workspace cache; session
	// state remains the authoritative source of "what should exist".
	Prepared map[string]PreparedRecord `json:"prepared,omitempty"`
}

// PreparedRecord captures a single successfully-applied workspace
// requirement. It is written by the reconciler after a successful
// apply and read on subsequent reconciles to decide whether to skip.
type PreparedRecord struct {
	Key         string    `json:"key"`
	Kind        string    `json:"kind"`
	Fingerprint string    `json:"fingerprint"`
	Target      string    `json:"target,omitempty"`
	PreparedAt  time.Time `json:"prepared_at"`
}

// SkillMeta records a staged skill snapshot.
type SkillMeta struct {
	Name     string    `json:"name"`
	RelPath  string    `json:"rel_path"`
	Digest   string    `json:"digest"`
	Mounted  bool      `json:"mounted"`
	StagedAt time.Time `json:"staged_at"`
}

// InputRecord tracks a staged input resolution.
type InputRecord struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Resolved  string    `json:"resolved,omitempty"`
	Version   *int      `json:"version,omitempty"`
	Mode      string    `json:"mode,omitempty"`
	Timestamp time.Time `json:"ts"`
}

// OutputRecord tracks an output collection run.
type OutputRecord struct {
	Globs     []string  `json:"globs"`
	SavedAs   []string  `json:"saved_as,omitempty"`
	Versions  []int     `json:"versions,omitempty"`
	LimitsHit bool      `json:"limits_hit"`
	Timestamp time.Time `json:"ts"`
}

// EnsureLayout creates standard workspace subdirectories and a
// metadata file when absent. It returns full paths for convenience.
func EnsureLayout(root string) (map[string]string, error) {
	paths := map[string]string{
		DirSkills: filepath.Join(root, DirSkills),
		DirWork:   filepath.Join(root, DirWork),
		DirRuns:   filepath.Join(root, DirRuns),
		DirOut:    filepath.Join(root, DirOut),
	}
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return nil, err
		}
	}
	// Initialize metadata if missing.
	mf := filepath.Join(root, MetaFileName)
	if _, err := os.Stat(mf); err != nil {
		if os.IsNotExist(err) {
			md := NewWorkspaceMetadata()
			if err := SaveMetadata(root, md); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	return paths, nil
}

// LoadMetadata loads metadata.json from workspace root. When missing,
// an empty metadata with defaults is returned without error.
func LoadMetadata(root string) (WorkspaceMetadata, error) {
	mf := filepath.Join(root, MetaFileName)
	b, err := os.ReadFile(mf)
	if err != nil {
		if os.IsNotExist(err) {
			return NewWorkspaceMetadata(), nil
		}
		return WorkspaceMetadata{}, err
	}
	var md WorkspaceMetadata
	if err := json.Unmarshal(b, &md); err != nil {
		return WorkspaceMetadata{}, err
	}
	return md, nil
}

// SaveMetadata writes metadata.json to the workspace root.
func SaveMetadata(root string, md WorkspaceMetadata) error {
	md.UpdatedAt = time.Now()
	buf, err := json.MarshalIndent(md, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(root, MetadataTempFileName())
	f, err := os.OpenFile(
		tmp,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		metadataFileMode,
	)
	if err != nil {
		return err
	}
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(buf); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(root, MetaFileName)); err != nil {
		return err
	}
	removeTmp = false
	return nil
}

// DirDigest computes a stable digest of a directory tree. It walks
// the tree, sorts entries, and hashes relative path and contents.
func DirDigest(root string) (string, error) {
	var files []string
	err := filepath.WalkDir(
		root,
		func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			files = append(files, rel)
			return nil
		},
	)
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		// Normalize to slash for stability.
		k := strings.ReplaceAll(rel, string(os.PathSeparator), "/")
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return "", err
		}
		_, _ = h.Write(b)
		_, _ = h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum), nil
}
