//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package pairing provides a small file-backed pairing store used by
// chat channels to fail-closed on first contact.
package pairing

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	storeVersion = 1

	defaultTTL = time.Hour

	codeDigits     = 6
	codeMod        = 1_000_000
	codeMaxAttempt = 32
)

// Request represents a pending pairing request.
type Request struct {
	Code      string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type state struct {
	Version  int                    `json:"version"`
	Approved map[string]int64       `json:"approved,omitempty"`
	Pending  map[string]pendingUser `json:"pending,omitempty"`
}

type pendingUser struct {
	UserID    string `json:"user_id"`
	CreatedAt int64  `json:"created_at_unix_ms"`
	ExpiresAt int64  `json:"expires_at_unix_ms"`
}

// FileStore persists pairing state in a local JSON file.
type FileStore struct {
	path string
	ttl  time.Duration

	mu       sync.Mutex
	loaded   bool
	modTime  time.Time
	fileInfo os.FileInfo
	state    state
}

// Option configures a FileStore.
type Option func(*FileStore)

// WithTTL sets the lifetime of a pending pairing request.
func WithTTL(ttl time.Duration) Option {
	return func(s *FileStore) {
		s.ttl = ttl
	}
}

// NewFileStore creates a file-backed store.
func NewFileStore(path string, opts ...Option) (*FileStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("pairing: empty store path")
	}

	s := &FileStore{
		path: path,
		ttl:  defaultTTL,
		state: state{
			Version:  storeVersion,
			Approved: map[string]int64{},
			Pending:  map[string]pendingUser{},
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if s.ttl <= 0 {
		return nil, errors.New("pairing: non-positive ttl")
	}
	return s, nil
}

// IsApproved returns whether userID is approved.
func (s *FileStore) IsApproved(
	ctx context.Context,
	userID string,
) (bool, error) {
	key := strings.TrimSpace(userID)
	if key == "" {
		return false, errors.New("pairing: empty user id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.reloadLocked(ctx); err != nil {
		return false, err
	}
	_, ok := s.state.Approved[key]
	return ok, nil
}

// Request ensures there is a pending request for userID and returns its code.
//
// If userID is already approved, approved is true and code is empty.
func (s *FileStore) Request(
	ctx context.Context,
	userID string,
) (code string, approved bool, err error) {
	key := strings.TrimSpace(userID)
	if key == "" {
		return "", false, errors.New("pairing: empty user id")
	}

	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.reloadLocked(ctx); err != nil {
		return "", false, err
	}
	if _, ok := s.state.Approved[key]; ok {
		return "", true, nil
	}

	if existing := s.pendingCodeLocked(now, key); existing != "" {
		return existing, false, nil
	}

	code, err = s.newCodeLocked()
	if err != nil {
		return "", false, err
	}

	createdAt := now.UTC()
	expiresAt := createdAt.Add(s.ttl)
	s.state.Pending[code] = pendingUser{
		UserID:    key,
		CreatedAt: createdAt.UnixMilli(),
		ExpiresAt: expiresAt.UnixMilli(),
	}
	if err := s.writeLocked(ctx); err != nil {
		return "", false, err
	}
	return code, false, nil
}

// Approve approves a pending pairing code.
func (s *FileStore) Approve(
	ctx context.Context,
	code string,
) (string, bool, error) {
	key := strings.TrimSpace(code)
	if key == "" {
		return "", false, errors.New("pairing: empty code")
	}

	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.reloadLocked(ctx); err != nil {
		return "", false, err
	}

	entry, ok := s.state.Pending[key]
	if !ok {
		return "", false, nil
	}
	if expired(now, entry.ExpiresAt) {
		delete(s.state.Pending, key)
		_ = s.writeLocked(ctx)
		return "", false, nil
	}

	delete(s.state.Pending, key)
	s.state.Approved[entry.UserID] = now.UTC().UnixMilli()
	if err := s.writeLocked(ctx); err != nil {
		return "", false, err
	}
	return entry.UserID, true, nil
}

// ListPending returns all non-expired pending requests.
func (s *FileStore) ListPending(ctx context.Context) ([]Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if err := s.reloadLocked(ctx); err != nil {
		return nil, err
	}

	out := make([]Request, 0, len(s.state.Pending))
	for code, entry := range s.state.Pending {
		if expired(now, entry.ExpiresAt) {
			continue
		}
		out = append(out, Request{
			Code:      code,
			UserID:    entry.UserID,
			CreatedAt: time.UnixMilli(entry.CreatedAt),
			ExpiresAt: time.UnixMilli(entry.ExpiresAt),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func expired(now time.Time, expiresAtUnixMs int64) bool {
	expiresAt := time.UnixMilli(expiresAtUnixMs)
	return !expiresAt.IsZero() && now.After(expiresAt)
}

func (s *FileStore) pendingCodeLocked(now time.Time, userID string) string {
	for code, entry := range s.state.Pending {
		if entry.UserID != userID {
			continue
		}
		if expired(now, entry.ExpiresAt) {
			continue
		}
		return code
	}
	return ""
}

func (s *FileStore) newCodeLocked() (string, error) {
	for i := 0; i < codeMaxAttempt; i++ {
		code, err := randomCode()
		if err != nil {
			return "", err
		}
		if _, ok := s.state.Pending[code]; ok {
			continue
		}
		return code, nil
	}
	return "", errors.New("pairing: failed to allocate code")
}

func randomCode() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("pairing: read random: %w", err)
	}
	n := binary.BigEndian.Uint32(b[:])
	code := int(n % codeMod)
	return fmt.Sprintf("%0*d", codeDigits, code), nil
}

func (s *FileStore) reloadLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	st, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.loaded = true
			s.modTime = time.Time{}
			s.fileInfo = nil
			s.cleanupExpiredLocked(time.Now())
			return nil
		}
		return fmt.Errorf("pairing: stat store: %w", err)
	}
	if s.loaded && s.sameFileLocked(st) &&
		!st.ModTime().After(s.modTime) {
		s.cleanupExpiredLocked(time.Now())
		return nil
	}

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("pairing: read store: %w", err)
	}

	var decoded state
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("pairing: decode store: %w", err)
	}
	if decoded.Version != storeVersion {
		return fmt.Errorf(
			"pairing: unexpected store version: %d",
			decoded.Version,
		)
	}
	if decoded.Approved == nil {
		decoded.Approved = map[string]int64{}
	}
	if decoded.Pending == nil {
		decoded.Pending = map[string]pendingUser{}
	}

	s.state = decoded
	s.loaded = true
	s.modTime = st.ModTime()
	s.fileInfo = st
	s.cleanupExpiredLocked(time.Now())
	return nil
}

func (s *FileStore) sameFileLocked(st os.FileInfo) bool {
	if s.fileInfo == nil {
		return false
	}
	return os.SameFile(s.fileInfo, st)
}

func (s *FileStore) cleanupExpiredLocked(now time.Time) {
	for code, entry := range s.state.Pending {
		if expired(now, entry.ExpiresAt) {
			delete(s.state.Pending, code)
		}
	}
}

func (s *FileStore) writeLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("pairing: create dir: %w", err)
	}

	payload, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("pairing: encode store: %w", err)
	}
	payload = append(payload, '\n')

	tmp := fmt.Sprintf("%s.%s.tmp", s.path, randomHex(8))
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return fmt.Errorf("pairing: write store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("pairing: rename store: %w", err)
	}

	st, err := os.Stat(s.path)
	if err == nil {
		s.modTime = st.ModTime()
		s.fileInfo = st
	}
	return nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return "rand"
	}
	return fmt.Sprintf("%x", b)
}
